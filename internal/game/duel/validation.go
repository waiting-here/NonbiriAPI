package duel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (s *Service) ValidatePersistedState(ctx context.Context) error {
	tx, _, err := s.beginRead(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := s.config(ctx, tx); err != nil {
		return err
	}
	for _, check := range []string{
		`SELECT COUNT(*) FROM game_duel_queue q WHERE q.game_key=? AND NOT EXISTS(SELECT 1 FROM game_duel_user_slots u WHERE u.queue_id=q.id AND u.user_id=q.user_id AND u.game_key=q.game_key)`,
		`SELECT COUNT(*) FROM game_duel_user_slots u WHERE u.game_key=? AND ((u.queue_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_queue q WHERE q.id=u.queue_id AND q.user_id=u.user_id AND q.game_key=u.game_key)) OR (u.session_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE g.id=u.session_id AND p.user_id=u.user_id AND g.game_key=u.game_key AND g.state='active')))`,
		`SELECT COUNT(*) FROM game_duel_sessions g WHERE g.game_key=? AND ((SELECT COUNT(*) FROM game_duel_seats p WHERE p.session_id=g.id)<>2 OR (g.state='active' AND (SELECT COUNT(*) FROM game_duel_user_slots u WHERE u.session_id=g.id AND u.game_key=g.game_key)<>2) OR (g.state='terminal' AND EXISTS(SELECT 1 FROM game_duel_user_slots u WHERE u.session_id=g.id)))`,
	} {
		var n int
		if err := tx.QueryRowContext(ctx, check, s.rules.ID()).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("duel parent ownership: %w", ErrInvariant)
		}
	}
	for _, kind := range []string{"queue", "session"} {
		table := "game_duel_queue"
		if kind == "session" {
			table = "game_duel_sessions"
		}
		after := ""
		for {
			rows, err := tx.QueryContext(ctx, `SELECT id FROM `+table+` WHERE game_key=? AND id>? ORDER BY id LIMIT 100`, s.rules.ID(), after)
			if err != nil {
				return err
			}
			ids := []string{}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				break
			}
			for _, id := range ids {
				if kind == "queue" {
					q, err := s.queue(ctx, tx, id)
					if err != nil {
						return err
					}
					if err := s.validateQueue(ctx, tx, q); err != nil {
						return err
					}
				} else {
					v, err := s.session(ctx, tx, id)
					if err != nil {
						return err
					}
					if err := s.validateSession(ctx, tx, v); err != nil {
						return err
					}
				}
			}
			after = ids[len(ids)-1]
		}
	}
	return s.validateArchives(ctx, tx)
}
func escrow(ctx context.Context, tx *sql.Tx, generalID, gameID int64, code string, general, game int64) error {
	for _, item := range []struct {
		id     int64
		asset  ledger.Asset
		amount int64
	}{{generalID, ledger.General, general}, {gameID, ledger.Game, game}} {
		a, err := ledger.ReadAccount(ctx, tx, item.id)
		if err != nil {
			return err
		}
		if a.Kind != ledger.AccountPlatform || a.Code != code || a.Asset != item.asset || a.Balance.Big().Cmp(big.NewInt(item.amount)) != 0 {
			return fmt.Errorf("duel escrow balance: %w", ErrInvariant)
		}
	}
	return nil
}
func (s *Service) validateCatalog(ctx context.Context, tx *sql.Tx, mode, hash string) error {
	selected, err := s.rulesFor(mode, hash)
	if err != nil {
		return err
	}
	c, err := selected.Catalog(mode)
	if err != nil || c.Hash != hash {
		return fmt.Errorf("duel rule identity: %w", ErrInvariant)
	}
	var json, design string
	var rules, schema int
	if err := tx.QueryRowContext(ctx, `SELECT catalog_json,design_version,rules_version,schema_version FROM game_duel_catalogs WHERE game_key=? AND content_hash=?`, s.rules.ID(), hash).Scan(&json, &design, &rules, &schema); err != nil {
		return err
	}
	if json != string(c.JSON) || design != c.DesignVersion || rules != 1 || schema != c.SchemaVersion {
		return fmt.Errorf("duel catalog snapshot: %w", ErrInvariant)
	}
	return nil
}
func (s *Service) validateQueue(ctx context.Context, tx *sql.Tx, q queueRecord) error {
	if err := s.validateCatalog(ctx, tx, q.Mode, q.Terms.ContentHash); err != nil {
		return err
	}
	if err := escrow(ctx, tx, q.GeneralAccount, q.GameAccount, "duel-queue:"+q.ID, q.Ticket-q.GamePaid, q.GamePaid); err != nil {
		return err
	}
	return operation(ctx, tx, q.Operation, q.ID, "duel_queue_reserve", "duel_queue", q.User)
}
func operation(ctx context.Context, tx *sql.Tx, id, source, kind, sourceType string, actor int64) error {
	var who any
	if actor > 0 {
		who = actor
	}
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM credit_operations WHERE id=? AND source_id=? AND kind=? AND source_type=? AND actor_user_id IS ? AND source_seq=?`, id, source, kind, sourceType, who, db.EncodeU128(db.U128{})).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("duel ledger fact: %w", ErrInvariant)
	}
	return nil
}
func (s *Service) validateSession(ctx context.Context, tx *sql.Tx, v sessionRecord) error {
	if err := s.validateCatalog(ctx, tx, v.Mode, v.Terms.ContentHash); err != nil {
		return err
	}
	general, game := int64(0), int64(0)
	info, err := v.rules.Inspect(v.Mode, v.Payload.Rules)
	if err != nil {
		return err
	}
	for seat, p := range v.Seats {
		if _, err := v.rules.Loadout(v.Mode, p.Loadout); err != nil {
			return fmt.Errorf("duel seat loadout: %w", ErrInvariant)
		}
		if v.State == "active" {
			general += p.GeneralPaid
			game += p.GamePaid
			if len(v.Payload.TerminalActions[seat]) != 0 && string(v.Payload.TerminalActions[seat]) != "null" {
				return ErrInvariant
			}
		}
		if v.Phase == "terminal" || v.Phase == "settlement" {
			if p.Locked || len(p.Action) != 0 {
				return ErrInvariant
			}
			continue
		}
		if !info.Required[seat] {
			expected, err := v.rules.Automatic(v.Mode, v.Payload.Rules, seat)
			if err != nil || !p.Locked || string(p.Action) != string(expected) {
				return fmt.Errorf("duel automatic lock: %w", ErrInvariant)
			}
		} else if p.Locked {
			accepted, err := v.rules.Accept(v.Mode, v.Payload.Rules, seat, p.Action)
			if err != nil || string(accepted) != string(p.Action) {
				return fmt.Errorf("duel accepted action: %w", ErrInvariant)
			}
		}
	}
	if err := escrow(ctx, tx, v.GeneralAccount, v.GameAccount, "duel-session:"+v.ID, general, game); err != nil {
		return err
	}
	if v.State == "terminal" {
		if err := operation(ctx, tx, v.Operation, v.ID, "duel_terminal", "duel_session", 0); err != nil {
			return err
		}
		if v.Reason != "surrender" && v.Reason != "server_restart" && v.Reason != "account_unavailable" {
			if info.Result == nil || info.Result.Reason != v.Reason || (info.Result.Winner == nil) != (v.Winner == nil) || v.Winner != nil && *v.Winner != *info.Result.Winner {
				return ErrInvariant
			}
		}
	}
	var count, max int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(round_no),0) FROM game_duel_rounds WHERE session_id=?`, v.ID).Scan(&count, &max); err != nil {
		return err
	}
	if count != max || max > v.Round || max < v.Round-1 {
		return fmt.Errorf("duel round sequence: %w", ErrInvariant)
	}
	rows, err := tx.QueryContext(ctx, `SELECT round_no,record_json FROM game_duel_rounds WHERE session_id=? ORDER BY round_no`, v.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		var body []byte
		if err := rows.Scan(&n, &body); err != nil {
			return err
		}
		var r roundRecord
		if Decode(body, &r) != nil || r.Round != n {
			return ErrInvariant
		}
		before, err := v.rules.Inspect(v.Mode, r.Before)
		if err != nil || before.Round != n || before.Result != nil {
			return ErrInvariant
		}
		after, err := v.rules.Inspect(v.Mode, r.After)
		if err != nil || after.Round < n || after.Round > n+1 {
			return ErrInvariant
		}
		if _, err := s.roundView(v.rules, v.Mode, body, 0, true); err != nil {
			return err
		}
		var facts struct {
			Round int `json:"round"`
		}
		if json.Unmarshal(r.Facts, &facts) != nil || facts.Round != n {
			return ErrInvariant
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func (s *Service) validateArchives(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT a.archive_id,a.mode,a.content_hash,a.header_json,r.round_no,r.record_json FROM game_duel_anonymous a LEFT JOIN game_duel_anonymous_rounds r ON r.archive_id=a.archive_id WHERE a.game_key=? ORDER BY a.archive_id,r.round_no`, s.rules.ID())
	if err != nil {
		return err
	}
	defer rows.Close()
	previous, round := "", 0
	for rows.Next() {
		var id, mode, hash, body string
		var n sql.NullInt64
		var record sql.NullString
		if err := rows.Scan(&id, &mode, &hash, &body, &n, &record); err != nil {
			return err
		}
		if id != previous {
			var h anonymousHeader
			if !db.ValidateOpaqueID(id, "dah_") || Decode([]byte(body), &h) != nil || h.Game != s.rules.ID() || h.Mode != mode || h.ContentHash != hash || h.RulesVersion != 1 || !validRates(h.Rake) {
				return ErrInvariant
			}
			if err := s.validateCatalog(ctx, tx, mode, hash); err != nil {
				return ErrInvariant
			}
			previous, round = id, 0
		}
		if n.Valid {
			round++
			var r RoundView
			if int(n.Int64) != round || !record.Valid || Decode([]byte(record.String), &r) != nil || r.Round != round {
				return ErrInvariant
			}
			selected, err := s.rulesFor(mode, hash)
			if err != nil {
				return err
			}
			if _, err := selected.RoundView(mode, r.Facts, 0, true); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}
