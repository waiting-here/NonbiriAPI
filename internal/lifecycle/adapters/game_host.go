package adapters

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

// The fixed export sections are compatibility DTOs. All game work is dispatched
// through a registered capability bound to the lifecycle-owned transaction.
func exportRegisteredGame[T any](service *host.Service, id string, ctx context.Context, tx *sql.Tx, request lifecycle.ExportRequest) (T, host.Finalizer, error) {
	var zero T
	bound, err := service.BindTx(ctx, tx, id)
	if err != nil {
		return zero, nil, err
	}
	value, finalizer, err := bound.Export(request.UserID, request.DecisionNow, request.Limit)
	if errors.Is(err, host.ErrResourceLimit) {
		err = lifecycle.ErrTooLarge
	}
	if err != nil {
		return zero, finalizer, err
	}
	out, ok := value.(T)
	if !ok {
		return zero, finalizer, lifecycle.ErrInvariant
	}
	return out, finalizer, nil
}

func deleteRegisteredGame(service *host.Service, id string, ctx context.Context, tx *sql.Tx, request lifecycle.DeleteRequest) (host.Finalizer, error) {
	bound, err := service.BindTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return bound.PrepareDelete(request.UserID, request.DecisionNow)
}

func retainRegisteredGame(service *host.Service, id string, ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	result, err := service.RetainModule(ctx, id, now, limit, deadline)
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, err
}

type RegisteredGameRecovery struct {
	service *host.Service
	id      string
}

func NewRegisteredGameRecovery(service *host.Service, id string) *RegisteredGameRecovery {
	return &RegisteredGameRecovery{service: service, id: id}
}

func (adapter *RegisteredGameRecovery) RecoverBeforeListener(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.service == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	result, err := adapter.service.RecoverModule(ctx, adapter.id, now, limit, deadline)
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, err
}
