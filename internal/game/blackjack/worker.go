package blackjack

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
)

func (s *Service) Ready(context.Context, *sql.Tx) bool { return s != nil && !s.closed.Load() }
func (s *Service) Available(mode, spec string) bool {
	return mode == "table" && spec == "" && s != nil && !s.closed.Load() && s.recovered.Load()
}
func (s *Service) ValidatePersistedState(ctx context.Context) error {
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := s.config(ctx, tx); err != nil {
		return err
	}
	var bad int
	for _, query := range []string{
		`SELECT COUNT(*) FROM game_blackjack_entries e WHERE (state IN ('waiting','seated','playing') AND NOT EXISTS(SELECT 1 FROM game_blackjack_payments p WHERE p.entry_id=e.id AND p.kind='base' AND p.state='reserved')) OR (state IN ('settled','released') AND EXISTS(SELECT 1 FROM game_blackjack_payments p WHERE p.entry_id=e.id AND p.state='reserved'))`,
		`SELECT COUNT(*) FROM game_blackjack_payments p JOIN game_blackjack_entries e ON e.id=p.entry_id WHERE p.amount_milli<>e.stake_milli OR p.state='reserved' AND e.state NOT IN ('waiting','seated','playing')`,
		`SELECT COUNT(*) FROM game_blackjack_entries e JOIN game_blackjack_sessions g ON g.id=e.session_id WHERE (e.state='seated' AND g.phase<>'seating') OR (e.state='playing' AND g.phase<>'decision') OR (e.state='settled' AND g.phase<>'result') OR (e.state='released' AND g.phase<>'cancelled')`,
	} {
		if err := tx.QueryRowContext(ctx, query).Scan(&bad); err != nil {
			return err
		}
		if bad != 0 {
			return ErrInvariant
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_blackjack_entries WHERE state='waiting'`).Scan(&bad); err != nil {
		return err
	}
	if bad > 4096 {
		return ErrInvariant
	}
	v, err := currentSession(ctx, tx)
	if noRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	list, err := sessionEntries(ctx, tx, v.ID)
	if err != nil {
		return err
	}
	if len(list) < 1 || len(list) > engine.MaxSeats {
		return ErrInvariant
	}
	if v.Phase == "decision" {
		state, err := decodeState(v)
		if err != nil || state.Finished || len(state.Seats) != len(list) {
			return ErrInvariant
		}
		for i, e := range list {
			if state.Seats[i].Number != int(e.Seat.Int64) {
				return ErrInvariant
			}
			units := 0
			for _, h := range state.Seats[i].Hands {
				units += h.Units
			}
			if e.Pending.Valid {
				var action engine.Action
				if json.Unmarshal([]byte(e.Pending.String), &action) != nil {
					return ErrInvariant
				}
				// Persisted actions from the former 60-second schedule are validated
				// before recovery cancels the table and refunds its original assets.
				additional, err := state.AdditionalUnits(action)
				if err != nil || action.Seat != int(e.Seat.Int64) || e.Batch.Int64 <= v.LastBatch || e.Batch.Int64 > v.StartedAt+45 {
					return ErrInvariant
				}
				units += additional
			}
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_blackjack_payments WHERE entry_id=? AND state='reserved'`, e.ID).Scan(&count); err != nil {
				return err
			}
			if count != units {
				return ErrInvariant
			}
		}
	}
	return nil
}

func (s *Service) RecoverBeforeListen(ctx context.Context, now int64, limit int, deadline time.Time) (host.WorkResult, error) {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	if limit < 1 || deadline.IsZero() || now < 0 || now > maxTime {
		return host.WorkResult{}, ErrInvalid
	}
	if s.recovered.Load() {
		return s.work(ctx, limit)
	}
	tx, sampled, err := s.begin(ctx)
	if err != nil {
		return host.WorkResult{}, err
	}
	defer tx.Rollback()
	facts, err := s.cancelCurrent(ctx, tx, max(now, sampled), "server_restart")
	if err != nil {
		return host.WorkResult{}, err
	}
	processed, more, err := s.finance.RestoreOnboarding(ctx, tx, limit, max(now, sampled))
	if err != nil {
		return host.WorkResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return host.WorkResult{}, err
	}
	s.recovered.Store(!more)
	s.publish(ctx, facts)
	return host.WorkResult{Processed: max(1, processed), More: more}, nil
}
func (s *Service) work(ctx context.Context, limit int) (host.WorkResult, error) {
	tx, now, err := s.begin(ctx)
	if err != nil {
		return host.WorkResult{}, err
	}
	defer tx.Rollback()
	facts, err := s.progress(ctx, tx, now, true)
	if err != nil {
		return host.WorkResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return host.WorkResult{}, err
	}
	s.publish(ctx, facts)
	return host.WorkResult{Processed: min(1, limit)}, nil
}
func (s *Service) StartWorker(ctx context.Context) error {
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	if s.closed.Load() || !s.recovered.Load() || s.workerCancel != nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	s.workerCancel = cancel
	s.workerDone = make(chan struct{})
	go func() {
		defer close(s.workerDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				work, stop := context.WithTimeout(ctx, 5*time.Second)
				_, err := s.work(work, 128)
				stop()
				if err != nil && ctx.Err() == nil && s.reportError != nil {
					s.reportError(err)
				}
			}
		}
	}()
	return nil
}
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.closed.Store(true)
	s.workerMu.Lock()
	cancel, done := s.workerCancel, s.workerDone
	s.workerMu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	return nil
}

type finalizer struct {
	once   sync.Once
	commit func()
}

func (f *finalizer) Commit() bool {
	ran := false
	f.once.Do(func() {
		ran = true
		if f.commit != nil {
			f.commit()
		}
	})
	return ran
}
func (f *finalizer) Abort() bool { ran := false; f.once.Do(func() { ran = true }); return ran }

func (s *Service) stopUserTx(ctx context.Context, tx *sql.Tx, user, now int64, deleting bool) (host.Finalizer, error) {
	if tx == nil || user <= 0 || now < 0 || now > maxTime {
		return nil, ErrInvalid
	}
	if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_clock SET observed_at=max(observed_at,?) WHERE id=1`, now); err != nil {
		return nil, err
	}
	var sampled int64
	if err := tx.QueryRowContext(ctx, `SELECT observed_at FROM game_blackjack_clock WHERE id=1`).Scan(&sampled); err != nil {
		return nil, err
	}
	now = max(sampled, s.now().UTC().Unix())
	if now > maxTime {
		return nil, ErrInvariant
	}
	if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_clock SET observed_at=? WHERE id=1`, now); err != nil {
		return nil, err
	}
	facts, err := s.progress(ctx, tx, now, false)
	if err != nil {
		return nil, err
	}
	e, err := currentEntry(ctx, tx, user)
	if err != nil && !noRows(err) {
		return nil, err
	}
	if err == nil {
		if e.State == "playing" {
			if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_entries SET stopped=1 WHERE id=?`, e.ID); err != nil {
				return nil, err
			}
		} else {
			if err := s.releaseEntry(ctx, tx, e, now); err != nil {
				return nil, err
			}
			facts.AccountIDs = append(facts.AccountIDs, user)
		}
	}
	if deleting {
		if err := s.finance.ReleaseOnboarding(ctx, tx, user); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_entries SET user_id=NULL WHERE user_id=?`, user); err != nil {
			return nil, err
		}
		ids := facts.AccountIDs[:0]
		for _, id := range facts.AccountIDs {
			if id != user {
				ids = append(ids, id)
			}
		}
		facts.AccountIDs = ids
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_blackjack_sessions WHERE phase='seating' AND NOT EXISTS(SELECT 1 FROM game_blackjack_entries e WHERE e.session_id=game_blackjack_sessions.id)`); err != nil {
		return nil, err
	}
	return &finalizer{commit: func() { s.publish(ctx, facts) }}, nil
}
func (s *Service) CancelUserTx(ctx context.Context, tx *sql.Tx, user, now int64) (host.Finalizer, error) {
	return s.stopUserTx(ctx, tx, user, now, false)
}
func (s *Service) PrepareDeleteTx(ctx context.Context, tx *sql.Tx, user, now int64) (host.Finalizer, error) {
	return s.stopUserTx(ctx, tx, user, now, true)
}
