package gacha

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type PoolStatistics struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	NameEN           string `json:"name_en"`
	Kind             string `json:"kind"`
	StartsAt         *int64 `json:"starts_at"`
	EndsAt           *int64 `json:"ends_at"`
	TotalDraws       string `json:"total_draws"`
	ParticipantCount string `json:"participant_count"`
	Available        bool   `json:"available"`
}
type CardStatistics struct {
	CardID                string  `json:"card_id"`
	Name                  string  `json:"name"`
	NameEN                string  `json:"name_en"`
	Rarity                *Rarity `json:"rarity"`
	Count                 string  `json:"count"`
	ParticipantCount      string  `json:"participant_count"`
	FirstAcquisitionCount string  `json:"first_acquisition_count"`
}
type RarityStatistics struct {
	Rarity                *Rarity `json:"rarity"`
	Count                 string  `json:"count"`
	FirstAcquisitionCount string  `json:"first_acquisition_count"`
}
type PoolStatisticsPage struct {
	Items      []PoolStatistics    `json:"items"`
	Pagination pagination.Metadata `json:"pagination"`
}
type CardStatisticsPage struct {
	Pool         PoolStatistics      `json:"pool"`
	Items        []CardStatistics    `json:"items"`
	Pagination   pagination.Metadata `json:"pagination"`
	RarityTotals []RarityStatistics  `json:"rarity_totals"`
	TopCards     []CardStatistics    `json:"top_cards"`
}
type StatisticsQuery struct {
	Page         pagination.Request
	Search, Sort string
}

// Live metadata wins while a pool exists; retained snapshots identify detached
// periods. A pool ID is never reused for a different period by this reader.
const statisticsPoolsSQL = `WITH pools AS (
 SELECT 'permanent' AS id,? AS config,1 AS available
 UNION ALL SELECT id,config,1 FROM gacha_pools
 UNION ALL SELECT pool_id,config,0 FROM gacha_pool_statistics s
 WHERE pool_id<>'permanent' AND NOT EXISTS(SELECT 1 FROM gacha_pools p WHERE p.id=s.pool_id)
), stats AS (
 SELECT p.id,p.config,p.available,COALESCE(s.total_draws,zeroblob(16)) AS total_draws,
 COALESCE(s.participant_count,zeroblob(16)) AS participant_count,COALESCE(s.roster,'[]') AS roster
 FROM pools p LEFT JOIN gacha_pool_statistics s ON s.pool_id=p.id
) `

func (s *Service) statisticsTx(ctx context.Context, actor int64) (*sql.Tx, string, error) {
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", err
	}
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, actor); err != nil {
		tx.Rollback()
		return nil, "", err
	}
	settings, err := readSettings(ctx, tx)
	if err != nil {
		tx.Rollback()
		return nil, "", err
	}
	return tx, jsonText(settings.permanentPool()), nil
}

func validStatisticsQuery(q StatisticsQuery, cards bool) bool {
	if !q.Page.Valid() || !utf8.ValidString(q.Search) || utf8.RuneCountInString(q.Search) > 128 {
		return false
	}
	if cards {
		return q.Sort == "" || q.Sort == "count" || q.Sort == "rarity" || q.Sort == "name"
	}
	return q.Sort == "" || q.Sort == "recent" || q.Sort == "draws"
}

func scanStatisticsPool(row interface{ Scan(...any) error }) (PoolStatistics, string, error) {
	var out PoolStatistics
	var pool Pool
	var raw, roster string
	var total, participants []byte
	if err := row.Scan(&out.ID, &raw, &out.Available, &total, &participants, &roster); err != nil {
		return out, "", err
	}
	if err := json.Unmarshal([]byte(raw), &pool); err != nil {
		return out, "", err
	}
	out.Name, out.NameEN, out.Kind, out.StartsAt, out.EndsAt = pool.Name, pool.NameEN, pool.Kind, pool.StartsAt, pool.EndsAt
	count, err := db.DecodeU128(total)
	if err != nil {
		return out, "", err
	}
	out.TotalDraws = count.Decimal()
	count, err = db.DecodeU128(participants)
	if err != nil {
		return out, "", err
	}
	out.ParticipantCount = count.Decimal()
	// Include zero-count cards from the current roster and all saved rosters.
	var saved []string
	if err = json.Unmarshal([]byte(roster), &saved); err != nil {
		return out, "", err
	}
	if out.Available {
		saved = append(saved, poolRoster(pool)...)
	}
	return out, jsonText(saved), nil
}

func (s *Service) AdminPoolStatistics(ctx context.Context, actor int64, q StatisticsQuery) (PoolStatisticsPage, error) {
	out := PoolStatisticsPage{Items: []PoolStatistics{}}
	if !validStatisticsQuery(q, false) {
		return out, ErrInvalid
	}
	tx, permanent, err := s.statisticsTx(ctx, actor)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	where := ` WHERE instr(lower(id||' '||COALESCE(json_extract(config,'$.name'),'')||' '||COALESCE(json_extract(config,'$.name_en'),'')),lower(?))>0`
	var total int64
	if err = tx.QueryRowContext(ctx, statisticsPoolsSQL+`SELECT count(*) FROM stats`+where, permanent, strings.TrimSpace(q.Search)).Scan(&total); err != nil {
		return out, err
	}
	meta, offset, err := q.Page.Window(total)
	if err != nil {
		return out, err
	}
	out.Pagination = meta
	order := `(id='permanent') DESC,COALESCE(json_extract(config,'$.starts_at'),0) DESC,id DESC`
	if q.Sort == "draws" {
		order = `(id='permanent') DESC,total_draws DESC,id`
	}
	rows, err := tx.QueryContext(ctx, statisticsPoolsSQL+`SELECT id,config,available,total_draws,participant_count,roster FROM stats`+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, permanent, strings.TrimSpace(q.Search), q.Page.Size, offset)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		item, _, err := scanStatisticsPool(rows)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func statisticsPoolTx(ctx context.Context, tx *sql.Tx, permanent, id string) (PoolStatistics, string, error) {
	if id != "permanent" && !db.ValidateOpaqueID(id, "gp_") {
		return PoolStatistics{}, "", ErrNotFound
	}
	out, roster, err := scanStatisticsPool(tx.QueryRowContext(ctx, statisticsPoolsSQL+`SELECT id,config,available,total_draws,participant_count,roster FROM stats WHERE id=?`, permanent, id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return out, roster, err
}

const statisticsCardsSQL = `WITH ids AS (
 SELECT value AS card_id FROM json_each(?) UNION SELECT card_id FROM gacha_card_statistics WHERE pool_id=?
), descriptions AS (
 SELECT json_extract(value,'$.id') AS id,json_extract(value,'$.name') AS name,json_extract(value,'$.name_en') AS name_en,json_extract(value,'$.rarity') AS rarity FROM json_each(?,'$.cards')
), cards AS (
 SELECT ids.card_id,COALESCE(d.name,ids.card_id) AS name,COALESCE(d.name_en,'') AS name_en,d.rarity,
 COALESCE(s.draw_count,zeroblob(16)) AS draw_count,COALESCE(s.participant_count,zeroblob(16)) AS participant_count,
 COALESCE(s.first_acquisition_count,zeroblob(16)) AS first_acquisition_count
 FROM ids LEFT JOIN descriptions d ON d.id=ids.card_id LEFT JOIN gacha_card_statistics s ON s.pool_id=? AND s.card_id=ids.card_id
) `
const statisticsCardColumns = `card_id,name,name_en,rarity,draw_count,participant_count,first_acquisition_count`
const statisticsRarityOrder = `CASE rarity WHEN 'SP' THEN 5 WHEN 'SSR' THEN 4 WHEN 'SR' THEN 3 WHEN 'R' THEN 2 WHEN 'N' THEN 1 ELSE 0 END DESC`

func statisticsCardArgs(id, roster string) []any { return []any{roster, id, string(catalogJSON), id} }
func scanCardStatistics(row interface{ Scan(...any) error }) (CardStatistics, error) {
	var out CardStatistics
	var count, participants, first []byte
	if err := row.Scan(&out.CardID, &out.Name, &out.NameEN, &out.Rarity, &count, &participants, &first); err != nil {
		return out, err
	}
	for _, v := range []struct {
		raw []byte
		to  *string
	}{{count, &out.Count}, {participants, &out.ParticipantCount}, {first, &out.FirstAcquisitionCount}} {
		n, err := db.DecodeU128(v.raw)
		if err != nil {
			return out, err
		}
		*v.to = n.Decimal()
	}
	return out, nil
}
func readCardStatistics(ctx context.Context, tx *sql.Tx, query string, args []any) ([]CardStatistics, error) {
	out := []CardStatistics{}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanCardStatistics(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func rarityStatisticsTx(ctx context.Context, tx *sql.Tx, id, roster string) ([]RarityStatistics, error) {
	return readRarityStatistics(ctx, tx, statisticsCardsSQL, statisticsCardArgs(id, roster))
}
func readRarityStatistics(ctx context.Context, tx *sql.Tx, query string, args []any) ([]RarityStatistics, error) {
	out := []RarityStatistics{}
	rows, err := tx.QueryContext(ctx, query+`SELECT rarity,nbi_u128_sum(draw_count),nbi_u128_sum(first_acquisition_count) FROM cards GROUP BY rarity ORDER BY `+statisticsRarityOrder, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item RarityStatistics
		var count, first []byte
		if err = rows.Scan(&item.Rarity, &count, &first); err != nil {
			return nil, err
		}
		n, e := db.DecodeU128(count)
		if e != nil {
			return nil, e
		}
		item.Count = n.Decimal()
		n, e = db.DecodeU128(first)
		if e != nil {
			return nil, e
		}
		item.FirstAcquisitionCount = n.Decimal()
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Service) AdminCardStatistics(ctx context.Context, actor int64, id string, q StatisticsQuery) (CardStatisticsPage, error) {
	out := CardStatisticsPage{Items: []CardStatistics{}}
	if !validStatisticsQuery(q, true) {
		return out, ErrInvalid
	}
	tx, permanent, err := s.statisticsTx(ctx, actor)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var roster string
	out.Pool, roster, err = statisticsPoolTx(ctx, tx, permanent, id)
	if err != nil {
		return out, err
	}
	args := append(statisticsCardArgs(id, roster), strings.TrimSpace(q.Search))
	where := ` WHERE instr(lower(card_id||' '||name||' '||name_en),lower(?))>0`
	var total int64
	if err = tx.QueryRowContext(ctx, statisticsCardsSQL+`SELECT count(*) FROM cards`+where, args...).Scan(&total); err != nil {
		return out, err
	}
	meta, offset, err := q.Page.Window(total)
	if err != nil {
		return out, err
	}
	out.Pagination = meta
	order := `draw_count DESC,` + statisticsRarityOrder + `,card_id`
	if q.Sort == "rarity" {
		order = statisticsRarityOrder + `,draw_count DESC,card_id`
	}
	if q.Sort == "name" {
		order = `name COLLATE NOCASE,card_id`
	}
	out.Items, err = readCardStatistics(ctx, tx, statisticsCardsSQL+`SELECT `+statisticsCardColumns+` FROM cards`+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, q.Page.Size, offset))
	if err != nil {
		return out, err
	}
	out.RarityTotals, err = rarityStatisticsTx(ctx, tx, id, roster)
	if err != nil {
		return out, err
	}
	out.TopCards, err = readCardStatistics(ctx, tx, statisticsCardsSQL+`SELECT `+statisticsCardColumns+` FROM cards WHERE draw_count>zeroblob(16) ORDER BY draw_count DESC,card_id LIMIT 10`, statisticsCardArgs(id, roster))
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
