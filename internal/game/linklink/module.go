package linklink

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	linklinkconfig "github.com/waiting-here/NonbiriAPI/internal/game/linklink/config"
)

func (service *Service) Module() *host.Module {
	available := func(mode, spec string) bool {
		return !service.closed.Load() && mode == "" && linklinkconfig.Descriptor().ResolveSpec(spec) == nil
	}
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
		StartWorker: service.StartWorker, Close: service.Close, Available: available, ReadyTx: func(context.Context, *sql.Tx) bool { return !service.closed.Load() && service.recovered.Load() },
		UserSnapshotTx: func(ctx context.Context, tx *sql.Tx, userID, now int64, value game.ConfigValue) (game.UserSnapshot, error) {
			if service.closed.Load() {
				return game.UserSnapshot{}, host.ErrServiceUnavailable
			}
			return game.UserSnapshot{Config: value.UserWire(available)}, nil
		},
		HomeSummaryTx: service.ModuleHomeSummaryTx, ActiveCountsTx: service.ModuleActiveCountsTx,
		ExportTx: func(ctx context.Context, tx *sql.Tx, userID, now int64, limit int) (any, host.Finalizer, error) {
			value, finalizer, err := service.Lifecycle().ExportTx(ctx, tx, userID, now, limit)
			var end host.Finalizer
			if finalizer != nil {
				end = finalizer
			}
			if errors.Is(err, ErrLifecycleResourceLimit) {
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
	value, err := service.HomeSummaryTx(ctx, tx, HomeSummaryInput{UserID: userID})
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
	if len(value.Continue) > 1 {
		return result, host.ErrInvariant
	}
	for _, item := range value.Continue {
		if item.State != "active" {
			return result, host.ErrInvariant
		}
		result.Continue = append(result.Continue, game.ContinueItem{Game: game.LinkLinkID, ResourceID: item.ResourceID, State: item.State, RouteID: "game-linklink"})
	}
	return result, nil
}
func (service *Service) ModuleActiveCountsTx(ctx context.Context, tx *sql.Tx) (game.ActiveCounts, error) {
	result := game.ActiveCounts{Games: []game.GameCount{}, Queues: []game.QueueCount{}}

	rows, err := tx.QueryContext(ctx, `SELECT spec,COUNT(*) FROM game_linklink_sessions GROUP BY spec ORDER BY spec`)
	if err != nil {
		return result, classifyDB(err)
	}
	defer rows.Close()
	for rows.Next() {
		var spec string
		var count int64
		if err := rows.Scan(&spec, &count); err != nil {
			return result, classifyDB(err)
		}
		if linklinkconfig.Descriptor().ResolveSpec(spec) != nil {
			return result, host.ErrInvariant
		}
		result.Games = append(result.Games, game.GameCount{Game: game.LinkLinkID, Spec: &spec, Count: strconv.FormatInt(count, 10)})
	}
	return result, classifyDB(rows.Err())
}
