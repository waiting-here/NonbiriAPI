package host

import (
	"context"
	"database/sql"
	"time"
)

// TxServices binds a registered module to a transaction owned by the caller.
// It exposes no commit, rollback or ability to switch the selected module.
type TxServices struct {
	module *Module
	tx     *sql.Tx
	ctx    context.Context
}

func (service *Service) BindTx(ctx context.Context, tx *sql.Tx, id string) (TxServices, error) {
	if service == nil || ctx == nil || tx == nil {
		return TxServices{}, ErrInvalidRequest
	}
	if service.closed.Load() {
		return TxServices{}, ErrClosed
	}
	module, ok := service.modules[id]
	if !ok {
		return TxServices{}, ErrInvalidRequest
	}
	return TxServices{module: module, tx: tx, ctx: ctx}, nil
}

func (bound TxServices) Export(userID, now int64, limit int) (any, Finalizer, error) {
	if bound.module == nil || bound.tx == nil || bound.ctx == nil || userID <= 0 || !validTime(now) || limit < 1 || limit > 10000 {
		return nil, nil, ErrInvalidRequest
	}
	return bound.module.ExportTx(bound.ctx, bound.tx, userID, now, limit)
}

func (bound TxServices) PrepareDelete(userID, now int64) (Finalizer, error) {
	if bound.module == nil || bound.tx == nil || bound.ctx == nil || userID <= 0 || !validTime(now) {
		return nil, ErrInvalidRequest
	}
	return bound.module.PrepareDeleteTx(bound.ctx, bound.tx, userID, now)
}

func (service *Service) RetainModule(ctx context.Context, id string, now int64, limit int, deadline time.Time) (WorkResult, error) {
	if service == nil || ctx == nil || !validWork(now, limit, deadline) {
		return WorkResult{}, ErrInvalidRequest
	}
	if service.closed.Load() {
		return WorkResult{}, ErrClosed
	}
	module, ok := service.modules[id]
	if !ok {
		return WorkResult{}, ErrInvalidRequest
	}
	result, err := module.Retain(ctx, now, limit, deadline)
	if err != nil {
		return WorkResult{}, err
	}
	if result.Processed < 0 || result.Processed > limit {
		return WorkResult{}, ErrInvariant
	}
	return result, nil
}

// RecoverBeforeListen is useful to standalone hosts; the application lifecycle
// coordinator uses RecoverModule to preserve its existing bounded schedule.
func (service *Service) RecoverBeforeListen(ctx context.Context) error {
	if err := service.ValidatePersistedState(ctx); err != nil {
		return err
	}
	for _, descriptor := range service.registry.Descriptors() {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			result, err := service.RecoverModule(ctx, descriptor.ID, service.services.Now().UTC().Unix(), 100, time.Now().Add(2*time.Second))
			if err != nil {
				return err
			}
			if !result.More {
				break
			}
		}
	}
	return nil
}
