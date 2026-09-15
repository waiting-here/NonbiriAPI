package blackjack

import (
	"context"
	"crypto/rand"
	"database/sql"
	"math/big"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/host"
)

type PersonalExport struct {
	Current *Home           `json:"current"`
	History []HistoryDetail `json:"history"`
}

func (s *Service) exportTx(ctx context.Context, tx *sql.Tx, user, now int64, limit int) (PersonalExport, error) {
	out := PersonalExport{History: []HistoryDetail{}}
	if tx == nil || user <= 0 || now < 0 || limit < 1 || limit > 10000 {
		return out, ErrInvalid
	}
	remaining, size := limit, 0
	charge := func(value any) error {
		body, err := marshal(value)
		if err != nil {
			return err
		}
		remaining--
		size += len(body)
		if remaining < 0 || size > 16<<20 {
			return ErrLimit
		}
		return nil
	}
	// Export is a safe snapshot; it cannot drive gameplay or disclose a shoe.
	if _, err := currentEntry(ctx, tx, user); err == nil {
		home, err := s.homeTx(ctx, tx, Identity{UserID: user}, now)
		if err != nil {
			return out, err
		}
		out.Current = &home
		if err := charge(home); err != nil {
			return out, err
		}
	} else if !noRows(err) {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT g.id FROM game_blackjack_sessions g WHERE g.terminal_at>? AND EXISTS(SELECT 1 FROM game_blackjack_entries e WHERE e.session_id=g.id AND e.user_id=?) ORDER BY g.started_at DESC LIMIT ?`, now-retentionSeconds, user, remaining+1)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		v, err := readSession(ctx, tx, id)
		if err != nil {
			return out, err
		}
		e, err := participant(ctx, tx, id, user)
		if err != nil {
			return out, err
		}
		view, err := projectTable(ctx, tx, v, now)
		if err != nil {
			return out, err
		}
		summary, err := ownSummary(ctx, tx, e, view)
		if err != nil {
			return out, err
		}
		d := HistoryDetail{Summary: summary, Table: view}
		if err := charge(d); err != nil {
			return out, err
		}
		out.History = append(out.History, d)
	}
	return out, nil
}
func (s *Service) Retain(ctx context.Context, now int64, limit int, deadline time.Time) (host.WorkResult, error) {
	out := host.WorkResult{}
	if now < 0 || now > maxTime || limit < 1 || limit > 100 || deadline.IsZero() {
		return out, ErrInvalid
	}
	rows, err := s.database.QueryContext(ctx, `SELECT id FROM game_blackjack_sessions WHERE terminal_at<=? ORDER BY terminal_at,id LIMIT ?`, now-retentionSeconds, limit+1)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) > limit {
		out.More = true
		ids = ids[:limit]
	}
	// Archive insertion order must not preserve the original game chronology.
	for i := len(ids) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return out, err
		}
		ids[i], ids[j.Int64()] = ids[j.Int64()], ids[i]
	}
	for _, id := range ids {
		if !time.Now().Before(deadline) {
			out.More = true
			return out, nil
		}
		tx, err := s.database.BeginTx(ctx, nil)
		if err != nil {
			return out, err
		}
		err = func() error {
			defer tx.Rollback()
			if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_clock SET observed_at=observed_at WHERE id=1`); err != nil {
				return err
			}
			v, err := readSession(ctx, tx, id)
			if noRows(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if !v.TerminalAt.Valid || v.TerminalAt.Int64+retentionSeconds > now {
				return ErrInvariant
			}
			fact, err := anonymizeFact(v)
			if err != nil {
				return err
			}
			body, err := marshal(fact)
			if err != nil {
				return err
			}
			archive, err := s.generate("bja_")
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO game_blackjack_anonymous(archive_id,public_json) VALUES(?,?)`, archive, string(body)); err != nil {
				return err
			}
			for _, query := range []string{`DELETE FROM game_blackjack_payments WHERE entry_id IN (SELECT id FROM game_blackjack_entries WHERE session_id=?)`, `DELETE FROM game_blackjack_entries WHERE session_id=?`, `DELETE FROM game_blackjack_events WHERE session_id=?`, `DELETE FROM game_blackjack_sessions WHERE id=?`} {
				if _, err := tx.ExecContext(ctx, query, id); err != nil {
					return err
				}
			}
			return tx.Commit()
		}()
		if err != nil {
			return out, err
		}
		out.Processed++
	}
	if out.Processed >= limit || !time.Now().Before(deadline) {
		return out, nil
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	list, err := entries(ctx, tx, `state='released' AND session_id IS NULL AND resolved_at<=? ORDER BY ordinal LIMIT ?`, now-retentionSeconds, limit-out.Processed+1)
	if err != nil {
		return out, err
	}
	if len(list) > limit-out.Processed {
		out.More = true
		list = list[:limit-out.Processed]
	}
	for _, e := range list {
		if _, err := tx.ExecContext(ctx, `DELETE FROM game_blackjack_payments WHERE entry_id=?`, e.ID); err != nil {
			return out, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM game_blackjack_entries WHERE id=?`, e.ID); err != nil {
			return out, err
		}
		out.Processed++
	}
	return out, tx.Commit()
}
