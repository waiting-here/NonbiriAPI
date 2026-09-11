package runtime

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
)

func (service *Service) Module() *host.Module {
	return &host.Module{
		ValidatePersistedState: service.ValidatePersistedState,
		RecoverBeforeListen: func(ctx context.Context, now int64, limit int, deadline time.Time) (host.WorkResult, error) {
			result, err := service.RecoverBeforeListenAt(ctx, now, limit, deadline)
			return host.WorkResult{Processed: result.Processed, More: result.More}, err
		},
		RegisterRoutes: func(registrars host.Registrars) error {
			return RegisterUserRoutes(registrars.User, service)
		},
		StartWorker: service.StartWorker, Close: service.Close,
		Available: func(mode, spec string) bool {
			return mode == "" && spec == "" && !service.closed.Load() && service.available()
		},
		ReadyTx: func(context.Context, *sql.Tx) bool { return !service.closed.Load() && service.recovered.Load() },
		UserSnapshotTx: func(ctx context.Context, tx *sql.Tx, userID, now int64, value game.ConfigValue) (game.UserSnapshot, error) {
			if service.closed.Load() {
				return game.UserSnapshot{}, host.ErrServiceUnavailable
			}
			return game.UserSnapshot{Config: value.UserWire(func(mode, spec string) bool {
				return mode == "" && spec == "" && service.available()
			})}, nil
		},
		HomeSummaryTx: service.ModuleHomeSummaryTx, ActiveCountsTx: service.ModuleActiveCountsTx,
		ExportTx: func(ctx context.Context, tx *sql.Tx, userID, now int64, limit int) (any, host.Finalizer, error) {
			value, err := service.Lifecycle().ExportTx(ctx, tx, userID, now, limit)
			if errors.Is(err, ErrLifecycleResourceLimit) {
				err = host.ErrResourceLimit
			}
			return value, nil, err
		},
		PrepareDeleteTx: func(ctx context.Context, tx *sql.Tx, userID, now int64) (host.Finalizer, error) {
			return nil, service.Lifecycle().PrepareDeleteTx(ctx, tx, userID, now)
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
	value, err := HomeSummaryTx(ctx, tx, userID)
	if err != nil {
		switch {
		case errors.Is(err, ErrHomeUnavailable):
			err = host.ErrServiceUnavailable
		case errors.Is(err, ErrHomeResourceLimit):
			err = host.ErrResourceLimit
		case errors.Is(err, ErrHomeInvalid), errors.Is(err, ErrHomeInvariant):
			err = host.ErrInvariant
		}
		return result, err
	}
	for _, item := range value.Continue {
		if item.State != HomeStateSettlementPending && item.State != HomeStateRecoveryRequired {
			return result, host.ErrInvariant
		}
		result.Continue = append(result.Continue, game.ContinueItem{Game: game.FishingID, ResourceID: item.ResourceID, State: item.State, RouteID: "game-fishing"})
	}
	for _, item := range value.PendingResults {
		result.PendingResults = append(result.PendingResults, game.PendingResult{Game: game.FishingID, ResourceID: item.ResourceID, CreatedAt: item.CreatedAt, RouteID: "game-fishing"})
	}
	return result, nil
}

func (service *Service) ModuleActiveCountsTx(ctx context.Context, tx *sql.Tx) (game.ActiveCounts, error) {
	result := game.ActiveCounts{Games: []game.GameCount{}, Queues: []game.QueueCount{}}
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_fishing_batches WHERE state='reserved'`).Scan(&count); err != nil {
		return result, classifyDB(err)
	}
	if count > 0 {
		result.Games = append(result.Games, game.GameCount{Game: game.FishingID, Count: strconv.FormatInt(count, 10)})
	}
	return result, nil
}
