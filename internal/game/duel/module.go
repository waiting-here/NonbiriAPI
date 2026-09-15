package duel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

func (s *Service) Module() *host.Module {
	return &host.Module{
		ValidatePersistedState: s.ValidatePersistedState,
		RecoverBeforeListen: func(ctx context.Context, now int64, limit int, deadline time.Time) (host.WorkResult, error) {
			// The coordinator reuses this capability during periodic recovery.
			// Only the first startup drain may cancel pre-existing games.
			if s.recovered.Load() {
				r, err := s.work(ctx, false, limit, deadline)
				return host.WorkResult{Processed: r.Processed, More: r.More}, err
			}
			r, err := s.RecoverBeforeListenAt(ctx, now, limit, deadline)
			return host.WorkResult{Processed: r.Processed, More: r.More}, err
		},
		RegisterRoutes: func(r host.Registrars) error {
			if err := s.RegisterRoutes(r.User, r.Continuation); err != nil {
				return err
			}
			if err := s.RegisterAdminRoutes(r.Admin); err != nil {
				return err
			}
			return r.Maintenance.Register(maintenance.ContinuationKind(s.rules.ID()+"_session"), s.ContinuationRegistration())
		},
		StartWorker: s.StartWorker, Close: s.Close, Available: s.Available, ReadyTx: s.Ready,
		UserSnapshotTx: s.UserSnapshotTx, HomeSummaryTx: s.HomeSummaryTx, ActiveCountsTx: s.ActiveCountsTx,
		ExportTx: func(ctx context.Context, tx *sql.Tx, user, now int64, limit int) (any, host.Finalizer, error) {
			value, f, err := s.ExportTx(ctx, tx, user, now, limit)
			if errors.Is(err, ErrResourceLimit) {
				err = host.ErrResourceLimit
			}
			return value, f, err
		},
		PrepareDeleteTx: s.PrepareDeleteTx,
		Retain: func(ctx context.Context, now int64, limit int, deadline time.Time) (host.WorkResult, error) {
			r, err := s.Retain(ctx, now, limit, deadline)
			return host.WorkResult{Processed: r.Processed, More: r.More}, err
		},
	}
}
func (s *Service) UserSnapshotTx(_ context.Context, _ *sql.Tx, _ int64, _ int64, value game.ConfigValue) (game.UserSnapshot, error) {
	var result map[string]json.RawMessage
	if json.Unmarshal(value.UserWire(s.Available), &result) != nil {
		return game.UserSnapshot{}, ErrInvariant
	}
	var config configuration
	if Decode(value.Wire(), &config) != nil {
		return game.UserSnapshot{}, ErrInvariant
	}
	var modes map[string]map[string]json.RawMessage
	if json.Unmarshal(result["modes"], &modes) != nil {
		return game.UserSnapshot{}, ErrInvariant
	}
	for mode, m := range config.Modes {
		terms, hash, _, err := s.terms(mode, m)
		if err != nil {
			return game.UserSnapshot{}, err
		}
		if modes[mode] == nil {
			return game.UserSnapshot{}, ErrInvariant
		}
		modes[mode]["terms_hash"], _ = json.Marshal(hash)
		modes[mode]["content_hash"], _ = json.Marshal(terms.ContentHash)
	}
	result["modes"], _ = json.Marshal(modes)
	body, err := Encode(result)
	return game.UserSnapshot{Config: body, Fields: map[string]json.RawMessage{}}, err
}
func (s *Service) HomeSummaryTx(ctx context.Context, tx *sql.Tx, user int64) (game.HomeSummary, error) {
	result := game.HomeSummary{Continue: []game.ContinueItem{}, PendingResults: []game.PendingResult{}}
	var queue, session sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT queue_id,session_id FROM game_duel_user_slots WHERE user_id=? AND game_key=?`, user, s.rules.ID()).Scan(&queue, &session)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	id, state := queue.String, "waiting"
	if session.Valid {
		id = session.String
		state = "active"
	}
	result.Continue = append(result.Continue, game.ContinueItem{Game: s.rules.ID(), ResourceID: id, State: state, RouteID: s.descriptor.HomeRouteID})
	return result, nil
}
func (s *Service) ActiveCountsTx(ctx context.Context, tx *sql.Tx) (game.ActiveCounts, error) {
	result := game.ActiveCounts{Games: []game.GameCount{}, Queues: []game.QueueCount{}}
	rows, err := tx.QueryContext(ctx, `SELECT mode,phase,COUNT(*) FROM game_duel_sessions WHERE game_key=? AND state='active' GROUP BY mode,phase ORDER BY mode,phase`, s.rules.ID())
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var mode, phase string
		var n int64
		if err := rows.Scan(&mode, &phase, &n); err != nil {
			rows.Close()
			return result, err
		}
		result.Games = append(result.Games, game.GameCount{Game: s.rules.ID(), Mode: &mode, Phase: &phase, Count: strconv.FormatInt(n, 10)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT mode,COUNT(*) FROM game_duel_queue WHERE game_key=? GROUP BY mode ORDER BY mode`, s.rules.ID())
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var mode string
		var n int64
		if err := rows.Scan(&mode, &n); err != nil {
			return result, err
		}
		result.Queues = append(result.Queues, game.QueueCount{Mode: mode, Count: strconv.FormatInt(n, 10)})
	}
	return result, rows.Err()
}
