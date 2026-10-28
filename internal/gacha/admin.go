package gacha

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/artwork"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func validateShop(shop ShopSettings) (ShopSettings, error) {
	p, err := price(shop.PermanentPrice)
	if err != nil {
		return shop, err
	}
	l, err := price(shop.LimitedPrice)
	if err != nil {
		return shop, err
	}
	shop.PermanentPrice = points(p.Big())
	shop.LimitedPrice = points(l.Big())
	for _, n := range []int64{shop.NTicket, shop.RTicket, shop.SRTicket, shop.SSRTickets, shop.SRCard, shop.SSRCard, shop.SPCard, shop.ChibiN, shop.ChibiR} {
		if n < 1 || n > maxBalance {
			return shop, ErrInvalid
		}
	}
	if shop.PermanentCap < 0 || shop.PermanentCap > maxBalance || shop.LimitedCap < 0 || shop.LimitedCap > maxBalance {
		return shop, ErrInvalid
	}
	return shop, nil
}
func validatePool(p Pool) (Pool, error) {
	if strings.TrimSpace(p.Name) == "" || utf8.RuneCountInString(p.Name) > 80 || utf8.RuneCountInString(p.NameEN) > 80 || p.Kind != "limited" {
		return p, ErrInvalid
	}
	if !slices.Contains(poolCoverIDs(), p.Cover) {
		return p, ErrInvalid
	}
	if err := p.Rules.Validate(); err != nil {
		return p, err
	}
	if p.StartsAt != nil && (*p.StartsAt < 0 || *p.StartsAt > 253402300799) || p.EndsAt != nil && (*p.EndsAt < 0 || *p.EndsAt > 253402300799) {
		return p, ErrInvalid
	}
	if p.Published && (p.StartsAt == nil || p.EndsAt == nil) || p.StartsAt != nil && p.EndsAt != nil && *p.EndsAt <= *p.StartsAt {
		return p, ErrInvalid
	}
	if len(p.Featured) < 1 || len(p.Featured) > len(catalog.Cards) {
		return p, ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range p.Featured {
		c, ok := cardsByID[id]
		if !ok || c.Source != "limited" || seen[id] {
			return p, ErrInvalid
		}
		seen[id] = true
	}
	p.Group = p.Rules.Group()
	p.Rates, _ = ConsolidatedRates(p.Rules)
	return p, nil
}
func (s *Service) configuration(ctx context.Context, tx *sql.Tx, at int64) (Configuration, error) {
	var out Configuration
	out.ServerTime = at
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return out, err
	}
	out.Rules, out.Shop, out.Revision, out.EntryEnabled = settings.rules, settings.shop, settings.revision, settings.entryEnabled
	_, _, out.TimeZone, err = calendar(ctx, tx, at)
	if err != nil {
		return out, err
	}
	out.Pools, err = readPools(ctx, tx, at, true, settings)
	if err != nil {
		return out, err
	}
	out.Presets = []Preset{}
	rows, err := tx.QueryContext(ctx, "SELECT id,name,config,revision FROM gacha_presets ORDER BY id")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Preset
		var raw string
		if err = rows.Scan(&p.ID, &p.Name, &raw, &p.Revision); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &p.Pool); err != nil {
			return out, err
		}
		out.Presets = append(out.Presets, p)
	}
	return out, rows.Err()
}
func (s *Service) AdminConfig(ctx context.Context, actor int64) (Configuration, error) {
	at, err := s.now()
	if err != nil {
		return Configuration{}, err
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Configuration{}, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, actor); err != nil {
		return Configuration{}, err
	}
	out, err := s.configuration(ctx, tx, at)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) AdminSave(ctx context.Context, actor int64, key string, input AdminInput) (Configuration, error) {
	var out Configuration
	if input.ExpectedRevision < 0 || input.ExpectedRevision >= maxBalance {
		return out, ErrInvalid
	}
	at, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, actor); err != nil {
		return out, err
	}
	actorHash, err := idempotency.ActorScopeHash("admin", strconv.FormatInt(actor, 10))
	if err != nil {
		return out, err
	}
	request := sha256.Sum256([]byte("gacha/config/" + jsonText(input)))
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeControlMutation, ActorHash: actorHash, Key: key, RequestHash: request, DecisionNow: at})
	if err != nil {
		return out, err
	}
	if decision.Kind == idempotency.Replay {
		// Configuration reads show the latest state; the mutation is never repeated.
		out, err = s.configuration(ctx, tx, at)
		if err != nil {
			return out, err
		}
		return out, tx.Commit()
	}
	var target string
	var stored any
	revision := input.ExpectedRevision + 1
	switch input.Kind {
	case "settings":
		if input.Rules == nil || input.Shop == nil || input.Pool != nil || input.Preset != nil || input.ExpectedRevision < 1 {
			return out, ErrInvalid
		}
		if err = input.Rules.Validate(); err != nil {
			return out, err
		}
		shop, e := validateShop(*input.Shop)
		if e != nil {
			return out, e
		}
		shop.Revision = revision
		entryEnabled, e := readEntryEnabled(ctx, tx)
		if e != nil {
			return out, e
		}
		if input.EntryEnabled != nil {
			entryEnabled = *input.EntryEnabled
		}
		storedShop := struct {
			ShopSettings
			EntryEnabled bool `json:"entry_enabled"`
		}{shop, entryEnabled}
		r, e := tx.ExecContext(ctx, "UPDATE gacha_settings SET rules=?,shop=?,revision=? WHERE id=1 AND revision=?", jsonText(input.Rules), jsonText(storedShop), revision, input.ExpectedRevision)
		if e != nil {
			return out, e
		}
		if n, e := r.RowsAffected(); e != nil || n != 1 {
			return out, ErrConflict
		}
		// Persist shorter limits even if no user draws between two admin edits.
		_, err = tx.ExecContext(ctx, `UPDATE gacha_pity SET sr_limit=CASE WHEN sr=0 THEN ? ELSE min(sr_limit,?) END,ssr_limit=CASE WHEN ssr=0 THEN ? ELSE min(ssr_limit,?) END WHERE group_id='permanent'`,
			input.Rules.SRPity, input.Rules.SRPity, input.Rules.SSRPity, input.Rules.SSRPity)
		target = "settings"
		stored = struct {
			Rules        Rules        `json:"rules"`
			Shop         ShopSettings `json:"shop"`
			EntryEnabled bool         `json:"entry_enabled"`
		}{*input.Rules, shop, entryEnabled}
	case "pool":
		if input.Pool == nil || input.EntryEnabled != nil || input.Rules != nil || input.Shop != nil || input.Preset != nil {
			return out, ErrInvalid
		}
		pool, e := validatePool(*input.Pool)
		if e != nil || !db.ValidateOpaqueID(pool.ID, "gp_") {
			return out, ErrInvalid
		}
		var raw, previousCover string
		var current int64
		e = tx.QueryRowContext(ctx, "SELECT config,revision FROM gacha_pools WHERE id=?", pool.ID).Scan(&raw, &current)
		if e == nil {
			if current != input.ExpectedRevision {
				return out, ErrConflict
			}
			var previous Pool
			if e = json.Unmarshal([]byte(raw), &previous); e != nil {
				return out, e
			}
			previousCover = previous.Cover
			if previous.Published && previous.StartsAt != nil && *previous.StartsAt <= at {
				if !pool.Published || pool.Rules != previous.Rules || !sameSet(pool.Featured, previous.Featured) ||
					pool.StartsAt == nil || *pool.StartsAt != *previous.StartsAt || pool.EndsAt == nil || previous.EndsAt == nil || *pool.EndsAt < *previous.EndsAt {
					return out, ErrConflict
				}
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return out, e
		} else if input.ExpectedRevision != 0 {
			return out, ErrConflict
		} else {
			var recorded bool
			if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM gacha_pool_statistics WHERE pool_id=?)", pool.ID).Scan(&recorded); err != nil {
				return out, err
			}
			if recorded {
				return out, ErrConflict
			}
		}
		if err = s.checkCoverSelection(ctx, tx, pool.Cover, previousCover); err != nil {
			return out, err
		}
		pool.Revision = revision
		_, err = tx.ExecContext(ctx, `INSERT INTO gacha_pools(id,config,revision,created_at) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET config=excluded.config,revision=excluded.revision`,
			pool.ID, jsonText(pool), revision, at)
		if err == nil {
			err = saveStatisticsPoolTx(ctx, tx, pool, poolRoster(pool))
		}
		target = pool.ID
		stored = pool
	case "preset":
		if input.Preset == nil || input.EntryEnabled != nil || input.Rules != nil || input.Shop != nil || input.Pool != nil {
			return out, ErrInvalid
		}
		p := *input.Preset
		if !db.ValidateOpaqueID(p.ID, "gt_") && !builtInPreset(p.ID) || strings.TrimSpace(p.Name) == "" || utf8.RuneCountInString(p.Name) > 80 {
			return out, ErrInvalid
		}
		p.Pool.Published = false
		p.Pool.StartsAt = nil
		p.Pool.EndsAt = nil
		p.Pool.ID = ""
		p.Pool.Revision = 0
		p.Pool, err = validatePool(p.Pool)
		if err != nil {
			return out, err
		}
		var current int64
		var previousRaw, previousCover string
		e := tx.QueryRowContext(ctx, "SELECT revision,config FROM gacha_presets WHERE id=?", p.ID).Scan(&current, &previousRaw)
		if e == nil && current != input.ExpectedRevision || errors.Is(e, sql.ErrNoRows) && input.ExpectedRevision != 0 {
			return out, ErrConflict
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if e == nil {
			var previous Pool
			if err = json.Unmarshal([]byte(previousRaw), &previous); err != nil {
				return out, err
			}
			previousCover = previous.Cover
		}
		if err = s.checkCoverSelection(ctx, tx, p.Pool.Cover, previousCover); err != nil {
			return out, err
		}
		p.Revision = revision
		_, err = tx.ExecContext(ctx, `INSERT INTO gacha_presets(id,name,config,revision) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,config=excluded.config,revision=excluded.revision`, p.ID, p.Name, jsonText(p.Pool), revision)
		target = "preset:" + p.ID
		stored = p
	default:
		return out, ErrInvalid
	}
	if err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO gacha_config_events(actor_user_id,target,revision,config,created_at) VALUES(?,?,?,?,?)", actor, target, revision, jsonText(stored), at)
	if err != nil {
		return out, err
	}
	managementlog.SetItemsResult(ctx, []managementlog.Item{{Action: "configure", TargetKind: "gacha_configuration", TargetID: target, Outcome: managementlog.Succeeded, Changes: []managementlog.Change{{Field: "configuration", Operation: "updated"}}}})
	if err = idempotency.Complete(ctx, tx, decision, http.StatusOK, []byte("{}")); err != nil {
		return out, err
	}
	out, err = s.configuration(ctx, tx, at)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left, right := map[string]bool{}, map[string]bool{}
	for _, id := range a {
		left[id] = true
	}
	for _, id := range b {
		right[id] = true
	}
	return reflect.DeepEqual(left, right)
}

func (s *Service) checkCoverSelection(ctx context.Context, tx *sql.Tx, selected, previous string) error {
	if selected == previous {
		return nil
	}
	disabled, err := artwork.R18DisabledTx(ctx, tx)
	if err != nil {
		return err
	}
	if !s.config.Artwork.Allowed("cover:"+selected, disabled) {
		return authz.ErrForbidden
	}
	return nil
}
