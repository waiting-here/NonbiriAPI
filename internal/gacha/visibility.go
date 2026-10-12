package gacha

import (
	"context"
	"database/sql"
	"encoding/json"
)

type PoolEntry struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	NameEN   string   `json:"name_en,omitempty"`
	Cover    string   `json:"cover"`
	Featured []string `json:"featured"`
	StartsAt int64    `json:"starts_at"`
	EndsAt   int64    `json:"ends_at"`
}

type Entry struct {
	Enabled bool        `json:"enabled"`
	Entered bool        `json:"entered"`
	Pools   []PoolEntry `json:"pools"`
}

func readEntryEnabled(ctx context.Context, tx *sql.Tx) (bool, error) {
	var enabled bool
	err := tx.QueryRowContext(ctx, "SELECT COALESCE(json_extract(shop,'$.entry_enabled'),0) FROM gacha_settings WHERE id=1").Scan(&enabled)
	return enabled, err
}

func (s *Service) Entry(ctx context.Context, user int64) (Entry, error) {
	out := Entry{Pools: []PoolEntry{}}
	at, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, user, at); err != nil {
		return out, err
	}
	out.Enabled, err = readEntryEnabled(ctx, tx)
	if err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM gacha_profiles WHERE user_id=? AND welcome_granted=1)", user).Scan(&out.Entered); err != nil {
		return out, err
	}
	if out.Enabled {
		out.Pools, err = readPoolEntries(ctx, tx, at)
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}

func readPoolEntries(ctx context.Context, tx *sql.Tx, at int64) ([]PoolEntry, error) {
	out := []PoolEntry{}
	rows, err := tx.QueryContext(ctx, "SELECT config FROM gacha_pools ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var pool Pool
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &pool); err != nil {
			return nil, err
		}
		if pool.Kind == "limited" && pool.Open(at) {
			out = append(out, PoolEntry{ID: pool.ID, Name: pool.Name, NameEN: pool.NameEN, Cover: pool.Cover,
				Featured: pool.Featured, StartsAt: *pool.StartsAt, EndsAt: *pool.EndsAt})
		}
	}
	return out, rows.Err()
}

// CardWasOfferedTx uses saved opening intervals, including completed pools.
func (s *Service) CardWasOfferedTx(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	card, ok := cardsByID[id]
	if !ok {
		return false, nil
	}
	if card.Source != "limited" {
		return true, nil
	}
	at, err := s.now()
	if err != nil {
		return false, err
	}
	offered, err := readOfferedCards(ctx, tx, at)
	return offered[id], err
}

// Each configuration applies from its save time until the next revision.
// A scheduled pool paused before its opening time has never been open.
func readOfferedCards(ctx context.Context, tx *sql.Tx, at int64) (map[string]bool, error) {
	offered := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `
  SELECT config FROM (
   SELECT config,created_at,lead(created_at,1,?) OVER (PARTITION BY target ORDER BY seq) AS next_at
   FROM gacha_config_events WHERE substr(target,1,3)='gp_' AND created_at<=?
  ) WHERE json_extract(config,'$.published')=1 AND json_extract(config,'$.paused')=0
   AND max(created_at,json_extract(config,'$.starts_at')) < min(next_at,json_extract(config,'$.ends_at'))
   AND json_extract(config,'$.starts_at')<=?`, at+1, at, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var pool Pool
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &pool); err != nil {
			return nil, err
		}
		for _, id := range pool.Featured {
			if _, ok := cardsByID[id]; ok {
				offered[id] = true
			}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return offered, nil
}

func readVisibleThemes(ctx context.Context, tx *sql.Tx, at int64, owned map[string]Owned) ([]string, error) {
	visible := map[string]bool{}
	for id := range owned {
		if card, ok := cardsByID[id]; ok {
			visible[card.Theme] = true
		}
	}
	offered, err := readOfferedCards(ctx, tx, at)
	if err != nil {
		return nil, err
	}
	for id := range offered {
		visible[cardsByID[id].Theme] = true
	}
	out := []string{}
	for _, theme := range catalog.Themes {
		limitedOnly := true
		for _, card := range catalog.Cards {
			if card.Theme == theme.ID && card.Source != "limited" {
				limitedOnly = false
				break
			}
		}
		if !limitedOnly || visible[theme.ID] {
			out = append(out, theme.ID)
		}
	}
	return out, nil
}
