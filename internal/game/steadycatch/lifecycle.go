package steadycatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	catchconfig "github.com/waiting-here/NonbiriAPI/internal/game/steadycatch/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/steadycatch/engine"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

type ExportSession struct {
	View
	Seed    uint32        `json:"seed"`
	Batches []ReplayBatch `json:"batches"`
}
type ReplayBatch struct {
	Until  int            `json:"until_tick"`
	Inputs []engine.Input `json:"inputs"`
}
type Export struct {
	FirstCleared bool            `json:"first_cleared"`
	Sessions     []ExportSession `json:"sessions"`
}

func (s *Service) Module() *host.Module {
	available := func(mode, spec string) bool { return !s.closed.Load() && mode == "" && spec == "" }
	return &host.Module{
		ValidatePersistedState: s.validate,
		RecoverBeforeListen: func(ctx context.Context, now int64, limit int, deadline time.Time) (host.WorkResult, error) {
			r, err := s.work(ctx, now, limit, deadline, !s.recovered.Load())
			if err == nil && !r.More {
				s.recovered.Store(true)
			}
			return r, err
		},
		RegisterRoutes: func(r host.Registrars) error {
			if err := s.registerRoutes(r); err != nil {
				return err
			}
			return r.Maintenance.Register("steadycatch_session", s.registration())
		},
		StartWorker: s.startWorker, Close: s.Close, Available: available, ReadyTx: func(context.Context, *sql.Tx) bool { return !s.closed.Load() && s.recovered.Load() },
		UserSnapshotTx: func(ctx context.Context, tx *sql.Tx, user, now int64, value game.ConfigValue) (game.UserSnapshot, error) {
			cleared, err := continuity.HasEligibilityTx(ctx, tx, user, continuity.GameOnboarding, clearScope, "v1", now)
			if err != nil {
				return game.UserSnapshot{}, err
			}
			var wire catchconfig.Wire
			if err = json.Unmarshal(value.UserWire(available), &wire); err != nil {
				return game.UserSnapshot{}, err
			}
			return game.UserSnapshot{Config: game.ConfigJSON(struct {
				catchconfig.Wire
				Available    bool `json:"available"`
				FirstCleared bool `json:"first_cleared"`
			}{wire, available("", ""), cleared})}, nil
		},
		HomeSummaryTx: func(ctx context.Context, tx *sql.Tx, user int64) (game.HomeSummary, error) {
			out := game.HomeSummary{Continue: []game.ContinueItem{}, PendingResults: []game.PendingResult{}}
			var id string
			err := tx.QueryRowContext(ctx, `SELECT id FROM game_catch_sessions WHERE user_id=? AND status IN ('playing','paused')`, user).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				return out, nil
			}
			if err == nil {
				out.Continue = append(out.Continue, game.ContinueItem{Game: game.SteadyCatchID, ResourceID: id, State: "active", RouteID: "game-steady-catch"})
			}
			return out, err
		},
		ActiveCountsTx: func(ctx context.Context, tx *sql.Tx) (game.ActiveCounts, error) {
			var n int64
			err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_catch_sessions WHERE status IN ('playing','paused')`).Scan(&n)
			out := game.ActiveCounts{Games: []game.GameCount{}, Queues: []game.QueueCount{}}
			if err == nil && n > 0 {
				out.Games = append(out.Games, game.GameCount{Game: game.SteadyCatchID, Count: strconv.FormatInt(n, 10)})
			}
			return out, err
		},
		ExportTx: func(ctx context.Context, tx *sql.Tx, user, now int64, limit int) (any, host.Finalizer, error) {
			v, err := s.export(ctx, tx, user, now, limit)
			return v, nil, err
		},
		PrepareDeleteTx: func(ctx context.Context, tx *sql.Tx, user, now int64) (host.Finalizer, error) {
			return nil, s.deleteUser(ctx, tx, user, now)
		},
		Retain: s.retain,
	}
}
func (s *Service) validate(ctx context.Context) error {
	rows, err := s.database.QueryContext(ctx, `SELECT engine_json,tick FROM game_catch_sessions WHERE status IN ('playing','paused')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		var tick int
		var state engine.State
		if err = rows.Scan(&body, &tick); err != nil {
			return err
		}
		if json.Unmarshal([]byte(body), &state) != nil || state.Version != engine.Version || state.Tick != tick || state.Finished() {
			return ErrUnavailable
		}
	}
	return rows.Err()
}
func (s *Service) work(ctx context.Context, now int64, limit int, deadline time.Time, restart bool) (host.WorkResult, error) {
	out := host.WorkResult{}
	for out.Processed < limit && time.Now().Before(deadline) {
		tx, err := s.database.BeginTx(ctx, nil)
		if err != nil {
			return out, err
		}
		var id string
		var user int64
		err = tx.QueryRowContext(ctx, `SELECT id,user_id FROM game_catch_sessions WHERE status IN ('playing','paused') AND (expires_at<=? OR (? AND status='playing')) ORDER BY expires_at,id LIMIT 1`, now, restart).Scan(&id, &user)
		if errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			return out, nil
		}
		if err == nil {
			var r record
			r, err = s.load(ctx, tx, id, user)
			if err == nil {
				previous := r.Revision
				r.Revision++
				r.LastHash = nil
				if now >= r.ExpiresAt {
					err = s.finish(ctx, tx, &r, "abandoned", previous, now)
				} else {
					r.Status = "paused"
					r.AnchorTick = r.State.Tick
					r.AnchorMS = now * 1000
					err = s.save(ctx, tx, &r, previous, now, "")
				}
			}
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return out, err
		}
		out.Processed++
	}
	err := s.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_catch_sessions WHERE status IN ('playing','paused') AND (expires_at<=? OR (? AND status='playing')))`, now, restart).Scan(&out.More)
	return out, err
}
func (s *Service) startWorker(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() || s.cancel != nil {
		return ErrUnavailable
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, err := s.work(ctx, s.now().Unix(), 100, time.Now().Add(time.Second), false)
				if err != nil && ctx.Err() == nil && s.report != nil {
					s.report(err)
				}
			}
		}
	}()
	return nil
}
func (s *Service) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	return nil
}
func (s *Service) export(ctx context.Context, tx *sql.Tx, user, now int64, limit int) (Export, error) {
	out := Export{Sessions: []ExportSession{}}
	var err error
	out.FirstCleared, err = continuity.HasEligibilityTx(ctx, tx, user, continuity.GameOnboarding, clearScope, "v1", now)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM game_catch_sessions WHERE user_id=? AND (terminal_at IS NULL OR terminal_at>?) ORDER BY created_at,id LIMIT ?`, user, now-retentionSeconds, limit+1)
	if err != nil {
		return out, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
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
		return out, host.ErrResourceLimit
	}
	count := len(ids)
	for _, id := range ids {
		r, err := s.load(ctx, tx, id, user)
		if err != nil {
			return out, err
		}
		v := ExportSession{View: r.View, Seed: r.Seed, Batches: []ReplayBatch{}}
		rows, err := tx.QueryContext(ctx, `SELECT until_tick,controls_json FROM game_catch_inputs WHERE session_id=? ORDER BY until_tick`, id)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var batch ReplayBatch
			var raw string
			if err = rows.Scan(&batch.Until, &raw); err != nil {
				rows.Close()
				return out, err
			}
			if err = json.Unmarshal([]byte(raw), &batch.Inputs); err != nil {
				rows.Close()
				return out, err
			}
			count++
			if count > limit {
				rows.Close()
				return out, host.ErrResourceLimit
			}
			v.Batches = append(v.Batches, batch)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		out.Sessions = append(out.Sessions, v)
	}
	return out, nil
}
func (s *Service) deleteUser(ctx context.Context, tx *sql.Tx, user, now int64) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM game_catch_sessions WHERE user_id=? AND status IN ('playing','paused')`, user).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		r, err := s.load(ctx, tx, id, user)
		if err != nil {
			return err
		}
		previous := r.Revision
		r.Revision++
		if err = s.finish(ctx, tx, &r, "cancelled", previous, now); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM game_catch_sessions WHERE user_id=?`, user)
	return err
}
func (s *Service) retain(ctx context.Context, now int64, limit int, _ time.Time) (host.WorkResult, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return host.WorkResult{}, err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `DELETE FROM game_catch_sessions WHERE id IN (SELECT id FROM game_catch_sessions WHERE terminal_at<=? ORDER BY terminal_at,id LIMIT ?)`, now-retentionSeconds, limit)
	if err != nil {
		return host.WorkResult{}, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return host.WorkResult{}, err
	}
	out := host.WorkResult{Processed: int(n)}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM game_catch_sessions WHERE terminal_at<=?)`, now-retentionSeconds).Scan(&out.More); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Service) registration() maintenance.ContinuationRegistration {
	return maintenance.ContinuationRegistration{
		Authority: func(ctx context.Context, tx *sql.Tx, r maintenance.ContinuationRequest) (bool, error) {
			if r.Kind != "steadycatch_session" || r.Authority != maintenance.ContinuationSession || r.ResourceRef != r.AcceptedRef || (r.Action != "read" && r.Action != "controls") {
				return false, nil
			}
			now := s.now().Unix()
			var n int
			err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_catch_sessions g JOIN users u ON u.id=g.user_id JOIN sessions a ON a.user_id=u.id WHERE g.id=? AND u.id=? AND (g.terminal_at IS NULL OR g.terminal_at>?) AND a.token_hash=? AND a.expires_at>? AND a.absolute_expires_at>? AND u.is_admin=0 AND (u.is_banned=0 OR u.banned_until<=?)`, r.ResourceRef, r.ActorUserID, now-retentionSeconds, r.SessionBinding, now, now, now).Scan(&n)
			return n == 1, err
		},
		Snapshot: func(ctx context.Context, tx *sql.Tx, r maintenance.ContinuationRequest) (maintenance.ContinuationSnapshot, error) {
			var revision, expires int64
			err := tx.QueryRowContext(ctx, `SELECT g.revision,min(a.expires_at,a.absolute_expires_at) FROM game_catch_sessions g JOIN sessions a ON a.user_id=g.user_id WHERE g.id=? AND a.token_hash=?`, r.ResourceRef, r.SessionBinding).Scan(&revision, &expires)
			return maintenance.ContinuationSnapshot{Revision: strconv.FormatInt(revision, 10), ExpiresAt: &expires, Payload: game.ConfigJSON(struct {
				ID string `json:"id"`
			}{r.ResourceRef})}, err
		},
	}
}
func (s *Service) authorizeExisting(ctx context.Context, tx *sql.Tx, r record, i Identity, action string) error {
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM maintenance_state WHERE id=1`).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	snapshot, err := s.continuation.AuthorizeContinuation(ctx, tx, maintenance.ContinuationRequest{Kind: "steadycatch_session", Authority: maintenance.ContinuationSession, AcceptedRef: r.ID, ActorUserID: i.UserID, SessionBinding: i.SessionBinding, ResourceRef: r.ID, Action: action})
	if err != nil {
		return err
	}
	if snapshot.ExpiresAt == nil || *snapshot.ExpiresAt <= s.now().Unix() || snapshot.Revision != strconv.FormatInt(r.Revision, 10) {
		return maintenance.ErrContinuationDenied
	}
	return nil
}
