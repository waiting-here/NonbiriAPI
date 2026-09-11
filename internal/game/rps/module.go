package rps

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
)

func (service *Service) Module() *host.Module {
	available := func(mode, spec string) bool { return service.Available(game.RPSID, mode, spec) }
	return &host.Module{
		ValidatePersistedState: service.ValidatePersistedState,
		RecoverBeforeListen: func(ctx context.Context, now int64, limit int, deadline time.Time) (host.WorkResult, error) {
			result, err := service.RecoverBeforeListenAt(ctx, now, limit, deadline)
			return host.WorkResult{Processed: result.Processed, More: result.More}, err
		},
		RegisterRoutes: func(registrars host.Registrars) error {
			if err := RegisterRoutes(registrars.User, registrars.Continuation, service); err != nil {
				return err
			}
			return RegisterContinuation(registrars.Maintenance, service)
		},
		StartWorker: service.StartWorker, Close: service.Close, Available: available, ReadyTx: service.Ready,
		UserSnapshotTx: func(ctx context.Context, tx *sql.Tx, userID, now int64, value game.ConfigValue) (game.UserSnapshot, error) {
			if service.closed.Load() {
				return game.UserSnapshot{}, host.ErrServiceUnavailable
			}
			var tutorial int
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT tutorial_rps_seen FROM game_user_preferences WHERE user_id=?),0)`, userID).Scan(&tutorial); err != nil {
				return game.UserSnapshot{}, classifyDB(err)
			}
			if tutorial != 0 && tutorial != 1 {
				return game.UserSnapshot{}, host.ErrInvariant
			}
			return game.UserSnapshot{Config: value.UserWire(available), Fields: map[string]json.RawMessage{"tutorial_rps_seen": game.ConfigJSON(tutorial == 1)}}, nil
		},
		HomeSummaryTx: service.ModuleHomeSummaryTx, ActiveCountsTx: service.ModuleActiveCountsTx,
		ExportTx: func(ctx context.Context, tx *sql.Tx, userID, now int64, limit int) (any, host.Finalizer, error) {
			value, finalizer, err := service.Lifecycle().ExportTx(ctx, tx, userID, now, limit)
			var end host.Finalizer
			if finalizer != nil {
				end = finalizer
			}
			if errors.Is(err, ErrResourceLimit) {
				err = host.ErrResourceLimit
			}
			return value, end, err
		},
		PrepareDeleteTx: func(ctx context.Context, tx *sql.Tx, userID, now int64) (host.Finalizer, error) {
			finalizer, err := service.Lifecycle().PrepareDeleteTx(ctx, tx, userID, now)
			if finalizer == nil {
				return nil, err
			}
			return finalizer, err
		},
		Retain: func(ctx context.Context, now int64, limit int, deadline time.Time) (host.WorkResult, error) {
			result, err := service.Lifecycle().Retain(ctx, now, limit, deadline)
			return host.WorkResult{Processed: result.Processed, More: result.More}, err
		},
	}
}
func (service *Service) ModuleHomeSummaryTx(ctx context.Context, tx *sql.Tx, userID int64) (game.HomeSummary, error) {
	result := game.HomeSummary{Continue: []game.ContinueItem{}, PendingResults: []game.PendingResult{}}
	if service.closed.Load() {
		return result, host.ErrServiceUnavailable
	}
	value, err := service.HomeSummaryTx(ctx, tx, userID)
	if err != nil {
		if errors.Is(err, ErrServiceUnavailable) || errors.Is(err, ErrClosed) {
			err = host.ErrServiceUnavailable
		}
		return result, err
	}
	return ProjectHomeSummary(value)
}

// ProjectHomeSummary checks the game-owned state union before host aggregation.
func ProjectHomeSummary(value HomeSummary) (game.HomeSummary, error) {
	result := game.HomeSummary{Continue: []game.ContinueItem{}, PendingResults: []game.PendingResult{}}
	if len(value.Continue) > 1 || len(value.PendingResults) > 1 || len(value.Continue) > 0 && len(value.PendingResults) > 0 {
		return result, host.ErrInvariant
	}
	for _, item := range value.Continue {
		if item.State != StateStarted && item.State != StateTerminalProcessing {
			return result, host.ErrInvariant
		}
		result.Continue = append(result.Continue, game.ContinueItem(item))
	}
	for _, item := range value.PendingResults {
		result.PendingResults = append(result.PendingResults, game.PendingResult(item))
	}
	return result, nil
}
func (service *Service) ModuleActiveCountsTx(ctx context.Context, tx *sql.Tx) (game.ActiveCounts, error) {
	result := game.ActiveCounts{Games: []game.GameCount{}, Queues: []game.QueueCount{}}
	rows, err := tx.QueryContext(ctx, `SELECT mode,phase,COUNT(*) FROM game_rps_sessions GROUP BY mode,phase ORDER BY mode,phase`)
	if err != nil {
		return result, classifyDB(err)
	}
	for rows.Next() {
		var mode, phase string
		var count int64
		if err := rows.Scan(&mode, &phase, &count); err != nil {
			rows.Close()
			return result, classifyDB(err)
		}
		if !validPersistentPhase(phase) {
			rows.Close()
			return result, host.ErrInvariant
		}
		result.Games = append(result.Games, game.GameCount{Game: game.RPSID, Mode: &mode, Phase: &phase, Count: strconv.FormatInt(count, 10)})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return result, classifyDB(err)
	}
	rows, err = tx.QueryContext(ctx, `SELECT mode,COUNT(*) FROM game_rps_queue GROUP BY mode ORDER BY mode`)
	if err != nil {
		return result, classifyDB(err)
	}
	defer rows.Close()
	for rows.Next() {
		var mode string
		var count int64
		if err := rows.Scan(&mode, &count); err != nil {
			return result, classifyDB(err)
		}
		result.Queues = append(result.Queues, game.QueueCount{Mode: mode, Count: strconv.FormatInt(count, 10)})
	}
	return result, classifyDB(rows.Err())
}
