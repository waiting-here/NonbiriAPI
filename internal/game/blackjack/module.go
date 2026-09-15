package blackjack

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
)

func (s *Service) Module() *host.Module {
	return &host.Module{ValidatePersistedState: s.ValidatePersistedState, RecoverBeforeListen: s.RecoverBeforeListen, RegisterRoutes: s.RegisterRoutes, StartWorker: s.StartWorker, Close: s.Close, Available: s.Available, ReadyTx: s.Ready, UserSnapshotTx: s.UserSnapshotTx, HomeSummaryTx: s.HomeSummaryTx, ActiveCountsTx: s.ActiveCountsTx, PrepareDeleteTx: s.PrepareDeleteTx, Retain: s.Retain, ExportTx: func(ctx context.Context, tx *sql.Tx, user, now int64, limit int) (any, host.Finalizer, error) {
		value, err := s.exportTx(ctx, tx, user, now, limit)
		if errors.Is(err, ErrLimit) {
			err = host.ErrResourceLimit
		}
		return value, nil, err
	}}
}
func (s *Service) UserSnapshotTx(_ context.Context, _ *sql.Tx, _ int64, _ int64, value game.ConfigValue) (game.UserSnapshot, error) {
	var result map[string]json.RawMessage
	if json.Unmarshal(value.UserWire(s.Available), &result) != nil {
		return game.UserSnapshot{}, ErrInvariant
	}
	var cfg config.Wire
	if json.Unmarshal(value.Wire(), &cfg) != nil {
		return game.UserSnapshot{}, ErrInvariant
	}
	body := value.Wire()
	hash := hashBytes(body)
	result["config_hash"], _ = marshal(hash)
	out, err := marshal(result)
	return game.UserSnapshot{Config: out, Fields: map[string]json.RawMessage{}}, err
}
func (s *Service) HomeSummaryTx(ctx context.Context, tx *sql.Tx, user int64) (game.HomeSummary, error) {
	out := game.HomeSummary{Continue: []game.ContinueItem{}, PendingResults: []game.PendingResult{}}
	e, err := currentEntry(ctx, tx, user)
	if noRows(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	id, state := e.ID, "waiting"
	if e.Session.Valid {
		id = e.Session.String
		state = "active"
	}
	out.Continue = append(out.Continue, game.ContinueItem{Game: config.ID, ResourceID: id, State: state, RouteID: "game-blackjack"})
	return out, nil
}
func (s *Service) ActiveCountsTx(ctx context.Context, tx *sql.Tx) (game.ActiveCounts, error) {
	out := game.ActiveCounts{Games: []game.GameCount{}, Queues: []game.QueueCount{}}
	rows, err := tx.QueryContext(ctx, `SELECT phase,COUNT(*) FROM game_blackjack_sessions WHERE phase IN ('seating','decision') GROUP BY phase ORDER BY phase`)
	if err != nil {
		return out, err
	}
	mode := "table"
	for rows.Next() {
		var phase string
		var n int64
		if err := rows.Scan(&phase, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.Games = append(out.Games, game.GameCount{Game: config.ID, Mode: &mode, Phase: &phase, Count: decimal(n)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	var n int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_blackjack_entries WHERE state='waiting'`).Scan(&n); err != nil {
		return out, err
	}
	if n > 0 {
		out.Queues = append(out.Queues, game.QueueCount{Game: config.ID, Mode: mode, Count: decimal(n)})
	}
	return out, nil
}
