package duel

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
)

type WorkResult struct {
	Processed int
	More      bool
}

func (s *Service) Ready(context.Context, *sql.Tx) bool {
	return s != nil && !s.closed.Load() && s.recovered.Load()
}
func (s *Service) Available(mode, spec string) bool {
	return s != nil && !s.closed.Load() && s.recovered.Load() && spec == "" && s.descriptor.ResolveMode(mode) == nil
}
func (s *Service) Tick(ctx context.Context) (WorkResult, error) {
	return s.work(ctx, false, 100, time.Now().Add(2*time.Second))
}
func (s *Service) RecoverBeforeListenAt(ctx context.Context, _ int64, limit int, deadline time.Time) (WorkResult, error) {
	result, err := s.work(ctx, true, limit, deadline)
	if err == nil && !result.More {
		s.recovered.Store(true)
	}
	return result, err
}
func (s *Service) work(ctx context.Context, recovery bool, limit int, deadline time.Time) (WorkResult, error) {
	if limit < 1 || limit > 100 || deadline.IsZero() {
		return WorkResult{}, ErrInvalidRequest
	}
	result := WorkResult{}
	for result.Processed < limit && time.Now().Before(deadline) {
		worked, err := s.workOne(ctx, recovery)
		if err != nil {
			return result, err
		}
		if !worked {
			return result, nil
		}
		result.Processed++
	}
	result.More = true
	return result, nil
}
func (s *Service) workOne(ctx context.Context, recovery bool) (bool, error) {
	tx, now, err := s.begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var qid string
	if recovery {
		err = tx.QueryRowContext(ctx, `SELECT id FROM game_duel_queue WHERE game_key=? ORDER BY created_at,id LIMIT 1`, s.rules.ID()).Scan(&qid)
	} else {
		maintenance, e := maintenanceOn(ctx, tx)
		if e != nil {
			return false, e
		}
		cfg, e := s.config(ctx, tx)
		if e != nil {
			return false, e
		}
		closed := []string{}
		for mode, value := range cfg.Modes {
			if !value.Enabled {
				closed = append(closed, mode)
			}
		}
		predicate := "0"
		args := []any{s.rules.ID(), now, now}
		if len(closed) > 0 {
			predicate = `mode IN (?` + strings.Repeat(",?", len(closed)-1) + `)`
			for _, mode := range closed {
				args = append(args, mode)
			}
		}
		if maintenance || !cfg.Enabled {
			predicate = "1"
			args = args[:3]
		}
		err = tx.QueryRowContext(ctx, `SELECT id FROM game_duel_queue q WHERE game_key=? AND (deadline<=? OR NOT EXISTS(SELECT 1 FROM users u WHERE u.id=q.user_id AND u.is_admin=0 AND u.discord_id IS NOT NULL AND u.discord_id<>'' AND (u.is_banned=0 OR u.banned_until<=?)) OR `+predicate+`) ORDER BY created_at,id LIMIT 1`, args...).Scan(&qid)
	}
	facts := activities.PublishFacts{}
	if err == nil {
		q, err := s.queue(ctx, tx, qid)
		if err != nil {
			return false, err
		}
		if err := s.releaseQueue(ctx, tx, q, 0, now); err != nil {
			return false, err
		}
		facts.AccountIDs = []int64{q.User}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	} else {
		var sid string
		if recovery {
			err = tx.QueryRowContext(ctx, `SELECT id FROM game_duel_sessions WHERE game_key=? AND state='active' ORDER BY started_at,id LIMIT 1`, s.rules.ID()).Scan(&sid)
		} else {
			err = tx.QueryRowContext(ctx, `SELECT id FROM game_duel_sessions WHERE game_key=? AND state='active' AND phase_deadline<=? ORDER BY phase_deadline,id LIMIT 1`, s.rules.ID(), now).Scan(&sid)
		}
		if err == nil {
			v, err := s.session(ctx, tx, sid)
			if err != nil {
				return false, err
			}
			if recovery {
				expected := v.Revision
				v.Revision, err = increment(v.Revision)
				if err != nil {
					return false, err
				}
				v.PhaseSeq, err = increment(v.PhaseSeq)
				if err != nil {
					return false, err
				}
				facts, err = s.terminal(ctx, tx, &v, expected, now, nil, "server_restart", true)
				if err != nil {
					return false, err
				}
			} else {
				var changed bool
				facts, changed, err = s.advance(ctx, tx, &v, now)
				if err != nil {
					return false, err
				}
				if !changed {
					return false, ErrInvariant
				}
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		} else if recovery {
			return false, nil
		} else {
			matched, err := s.match(ctx, tx, now)
			if err != nil {
				return false, err
			}
			if !matched {
				return false, nil
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return false, classify(err)
	}
	s.publish(ctx, facts)
	return true, nil
}
func (s *Service) StartWorker(ctx context.Context) error {
	if s == nil || !s.Ready(ctx, nil) {
		return ErrUnavailable
	}
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	if s.closed.Load() {
		return ErrUnavailable
	}
	if s.workerCancel != nil {
		return ErrConflict
	}
	worker, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.workerCancel = cancel
	s.workerDone = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-worker.Done():
				return
			case <-ticker.C:
				if _, err := s.Tick(worker); err != nil && !errors.Is(err, context.Canceled) && s.reportError != nil {
					s.reportError(err)
				}
			}
		}
	}()
	return nil
}
func (s *Service) Close() error {
	if s == nil || !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	s.workerMu.Lock()
	cancel, done := s.workerCancel, s.workerDone
	s.workerCancel = nil
	s.workerDone = nil
	s.workerMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	s.actionMu.Lock()
	clear(s.actions)
	s.actionMu.Unlock()
	return nil
}
