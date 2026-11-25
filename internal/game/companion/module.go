package companion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
)

// Attach adds the companion surface to its existing game without changing human-game ownership or settlement.
func (s *Service) Attach(id string, module *host.Module) *host.Module {
	base := *module
	module.RegisterRoutes = func(r host.Registrars) error {
		if err := base.RegisterRoutes(r); err != nil {
			return err
		}
		return s.register(id, r)
	}
	module.StartWorker = func(ctx context.Context) error {
		if err := base.StartWorker(ctx); err != nil {
			return err
		}
		s.startWorkers(ctx)
		return nil
	}
	module.Close = func() error { s.stopWorkers(); return base.Close() }
	var recovered atomic.Bool
	module.RecoverBeforeListen = func(ctx context.Context, now int64, limit int, deadline time.Time) (host.WorkResult, error) {
		n := 0
		if !recovered.Load() {
			var err error
			n, err = s.drain(ctx, id, now, limit, true)
			if err != nil {
				return host.WorkResult{}, err
			}
			if n >= limit {
				return host.WorkResult{Processed: n, More: true}, nil
			}
			recovered.Store(true)
		}
		next, err := base.RecoverBeforeListen(ctx, now, limit-n, deadline)
		next.Processed += n
		return next, err
	}
	module.Retain = func(ctx context.Context, now int64, limit int, deadline time.Time) (host.WorkResult, error) {
		n, err := s.drain(ctx, id, now, limit, false)
		if err != nil {
			return host.WorkResult{}, err
		}
		if n < limit {
			more, err := s.programs.Retain(ctx, id, now, limit-n)
			if err != nil {
				return host.WorkResult{}, err
			}
			n += more
		}
		if n >= limit {
			return host.WorkResult{Processed: n, More: true}, nil
		}
		next, err := base.Retain(ctx, now, limit-n, deadline)
		next.Processed += n
		return next, err
	}
	module.HomeSummaryTx = func(ctx context.Context, tx *sql.Tx, user int64) (game.HomeSummary, error) {
		v, err := base.HomeSummaryTx(ctx, tx, user)
		if err != nil {
			return v, err
		}
		var mid string
		err = tx.QueryRowContext(ctx, "SELECT id FROM companion_matches WHERE user_id=? AND game=? AND status='active'", user, id).Scan(&mid)
		if errors.Is(err, sql.ErrNoRows) {
			return v, nil
		}
		if err == nil {
			v.Continue = append(v.Continue, game.ContinueItem{Game: id, ResourceID: mid, State: "active", RouteID: "game-" + id})
		}
		return v, err
	}
	module.ActiveCountsTx = func(ctx context.Context, tx *sql.Tx) (game.ActiveCounts, error) {
		v, err := base.ActiveCountsTx(ctx, tx)
		if err != nil {
			return v, err
		}
		rows, err := tx.QueryContext(ctx, "SELECT ai_state,COUNT(*) FROM companion_matches WHERE game=? AND status='active' GROUP BY ai_state", id)
		if err != nil {
			return v, err
		}
		defer rows.Close()
		for rows.Next() {
			var phase string
			var n int64
			if err := rows.Scan(&phase, &n); err != nil {
				return v, err
			}
			phase = "companion_" + phase
			v.Games = append(v.Games, game.GameCount{Game: id, Phase: &phase, Count: strconv.FormatInt(n, 10)})
		}
		return v, rows.Err()
	}
	module.ExportTx = func(ctx context.Context, tx *sql.Tx, user, now int64, limit int) (any, host.Finalizer, error) {
		old, end, err := base.ExportTx(ctx, tx, user, now, limit)
		if err != nil {
			return nil, end, err
		}
		v, ok := old.(duel.Export)
		if !ok {
			if end != nil {
				end.Abort()
			}
			return nil, nil, host.ErrInvariant
		}
		v.Companion, err = s.export(ctx, tx, id, user, now, limit)
		if err != nil {
			if end != nil {
				end.Abort()
			}
			return nil, nil, err
		}
		return v, end, nil
	}
	module.PrepareDeleteTx = func(ctx context.Context, tx *sql.Tx, user, now int64) (host.Finalizer, error) {
		var active string
		err := tx.QueryRowContext(ctx, "SELECT id FROM companion_matches WHERE user_id=? AND game=? AND status='active'", user, id).Scan(&active)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM companion_matches WHERE user_id=? AND game=?", user, id); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM companion_preferences WHERE user_id=? AND game=?", user, id); err != nil {
			return nil, err
		}
		end, err := base.PrepareDeleteTx(ctx, tx, user, now)
		if err != nil || active == "" {
			return end, err
		}
		return &deleteFinalizer{base: end, cancel: func() { s.cancelGame(active) }}, nil
	}
	return module
}
func (s *Service) drain(ctx context.Context, game string, now int64, limit int, restart bool) (int, error) {
	query := "SELECT id FROM companion_matches WHERE game=? AND ended_at>0 AND ended_at<=? ORDER BY ended_at,id LIMIT ?"
	args := []any{game, now - retention, limit}
	if restart {
		query = "SELECT id FROM companion_matches WHERE game=? AND status='active' ORDER BY id LIMIT ?"
		args = []any{game, limit}
	}
	var exists bool
	if err := s.shared.ReadDatabase.QueryRowContext(ctx, "SELECT EXISTS("+query+")", args...).Scan(&exists); err != nil || !exists {
		return 0, err
	}
	tx, err := s.shared.Database.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var res sql.Result
	if restart {
		res, err = tx.ExecContext(ctx, "UPDATE companion_matches SET status='cancelled',reason='server_restart',ended_at=?,ai_state='idle',record=json_set(record,'$.result.status','cancelled','$.result.reason','server_restart') WHERE id IN ("+query+")", append([]any{now}, args...)...)
		if err == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM companion_history_snapshot WHERE match_id IN (SELECT id FROM companion_matches WHERE game=? AND ended_at=? AND status='cancelled')", game, now)
		}
	} else {
		res, err = tx.ExecContext(ctx, "DELETE FROM companion_matches WHERE id IN ("+query+")", args...)
	}
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}
func (s *Service) CancelUserTx(ctx context.Context, tx *sql.Tx, user, now int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `UPDATE companion_matches SET status='cancelled',reason='account_unavailable',ended_at=?,ai_state='idle',record=json_set(record,'$.result.status','cancelled','$.result.reason','account_unavailable') WHERE user_id=? AND status='active' RETURNING id`, now, user)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, "DELETE FROM companion_history_snapshot WHERE match_id=?", id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}
func (s *Service) CancelDecisions(ids []string) {
	for _, id := range ids {
		s.cancelGame(id)
	}
}
func (s *Service) export(ctx context.Context, tx *sql.Tx, game string, user, now int64, limit int) (json.RawMessage, error) {
	p, err := getPrefs(ctx, tx, user, game)
	if err != nil {
		return nil, err
	}
	programs, err := s.programs.ExportTx(ctx, tx, user, game)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,memory,contribute FROM companion_matches WHERE user_id=? AND game=? AND (status='active' OR ended_at>?) ORDER BY created_at,id LIMIT ?", user, game, now-retention, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Obj{}
	size := 0
	for rows.Next() {
		if len(items) >= limit {
			return nil, host.ErrResourceLimit
		}
		var id, mode string
		var consent bool
		if err = rows.Scan(&id, &mode, &consent); err != nil {
			return nil, err
		}
		m, err := readMatch(ctx, tx, id, game, user)
		if err != nil {
			return nil, err
		}
		v, err := m.view(m.Seat)
		if err != nil {
			return nil, err
		}
		own := []Raw{}
		for _, w := range m.Record.Actions {
			if w.Actions[m.Seat] != nil {
				own = append(own, w.Actions[m.Seat])
			}
		}
		item := Obj{"match": v, "actions": own, "locked": m.Frame.Locked[m.Seat], "constraints": m.Record.Initial.Constraints, "memory": mode, "contribute": consent}
		size += len(encode(item))
		if size > 16<<20 {
			return nil, host.ErrResourceLimit
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(Obj{"preferences": p, "programs": programs, "matches": items})
}

// Cancel a worker only once the surrounding account deletion commits.
type deleteFinalizer struct {
	base   host.Finalizer
	cancel func()
	done   atomic.Bool
}

func (f *deleteFinalizer) Commit() bool {
	if !f.done.CompareAndSwap(false, true) {
		return false
	}
	if f.base != nil {
		f.base.Commit()
	}
	f.cancel()
	return true
}
func (f *deleteFinalizer) Abort() bool {
	if !f.done.CompareAndSwap(false, true) {
		return false
	}
	if f.base != nil {
		f.base.Abort()
	}
	return true
}
