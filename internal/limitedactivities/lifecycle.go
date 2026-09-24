package limitedactivities

import (
	"context"
	"database/sql"
	"math/big"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type finalizerGroup struct {
	items []Finalizer
	done  atomic.Bool
}

func (g *finalizerGroup) Commit() bool {
	if g == nil || !g.done.CompareAndSwap(false, true) {
		return false
	}
	for _, f := range g.items {
		f.Commit()
	}
	return true
}
func (g *finalizerGroup) Abort() bool {
	if g == nil || !g.done.CompareAndSwap(false, true) {
		return false
	}
	for i := len(g.items) - 1; i >= 0; i-- {
		g.items[i].Abort()
	}
	return true
}

func (s *Service) prepare(ctx context.Context, tx *sql.Tx, now int64, fn func(Runtime) (Finalizer, error)) (Finalizer, error) {
	if s == nil || ctx == nil || tx == nil || now < 0 || now > maxUnix {
		return nil, ErrInvalid
	}
	g := &finalizerGroup{}
	for _, key := range s.registry.keys {
		runtime := s.registry.modules[key].runtime
		if nilInterface(runtime) {
			continue
		}
		f, err := fn(runtime)
		if !nilInterface(f) {
			g.items = append(g.items, f)
		}
		if err != nil {
			g.Abort()
			return nil, err
		}
	}
	return g, nil
}

func (s *Service) PrepareMaintenanceTx(ctx context.Context, tx *sql.Tx, now int64) (Finalizer, error) {
	return s.prepare(ctx, tx, now, func(r Runtime) (Finalizer, error) { return r.PrepareMaintenanceTx(ctx, tx, now) })
}
func (s *Service) PrepareBanTx(ctx context.Context, tx *sql.Tx, user, now int64) (Finalizer, error) {
	if user <= 0 {
		return nil, ErrInvalid
	}
	return s.prepare(ctx, tx, now, func(r Runtime) (Finalizer, error) { return r.PrepareBanTx(ctx, tx, user, now) })
}
func (s *Service) PrepareDeleteTx(ctx context.Context, tx *sql.Tx, user, now int64) (Finalizer, error) {
	if user <= 0 {
		return nil, ErrInvalid
	}
	f, err := s.prepare(ctx, tx, now, func(r Runtime) (Finalizer, error) { return r.PrepareDeleteTx(ctx, tx, user, now) })
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE activity_exchange_receipts SET user_id=NULL WHERE user_id=?`, user); err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE limited_activity_revisions SET actor_user_id=NULL WHERE actor_user_id=?`, user)
	}
	if err != nil {
		f.Abort()
		return nil, err
	}
	return f, nil
}

// ExportUserTx uses the lifecycle owner's existing authorization and snapshot.
// Configuration, counters, other users and runtime internals are not exported.
func (s *Service) ExportUserTx(ctx context.Context, tx *sql.Tx, user int64, limit int) (UserExport, error) {
	var out UserExport
	if s == nil || ctx == nil || tx == nil || user <= 0 || limit < 1 || limit > lifecycle.CollectionLimit {
		return out, ErrInvalid
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=?)`, user).Scan(&exists); err != nil {
		return out, err
	}
	if !exists {
		return out, ErrNotFound
	}
	w, err := walletTx(ctx, tx, user)
	if err != nil {
		return out, err
	}
	out.Wallet = w
	out.Exchanges = []Receipt{}
	rows, err := tx.QueryContext(ctx, `SELECT operation_id,activity_key,config_revision,asset_type,quantity_mag,unit_price_milli,cost_mag,ledger_seq,created_at FROM activity_exchange_receipts WHERE user_id=? ORDER BY created_at,operation_id LIMIT ?`, user, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Receipt
		var revision, unit, seq int64
		var quantityRaw, costRaw []byte
		if err = rows.Scan(&item.OperationID, &item.ActivityKey, &revision, &item.Asset, &quantityRaw, &unit, &costRaw, &seq, &item.CreatedAt); err != nil {
			return out, err
		}
		quantity, e := db.DecodeU128(quantityRaw)
		if e != nil {
			return out, ErrInvariant
		}
		cost, e := db.DecodeU128(costRaw)
		if e != nil {
			return out, ErrInvariant
		}
		item.ConfigRevision, item.LedgerSeq = strconv.FormatInt(revision, 10), strconv.FormatInt(seq, 10)
		item.Quantity, item.Cost, item.UnitPrice = quantity.Decimal(), points(cost.Big()), points(big.NewInt(unit))
		out.Exchanges = append(out.Exchanges, item)
		if len(out.Exchanges) > limit {
			return UserExport{}, ErrExportLimit
		}
	}
	return out, rows.Err()
}

func (s *Service) runModules(ctx context.Context, now int64, limit int, budget time.Duration, recovering bool) (lifecycle.WorkResult, error) {
	result := lifecycle.WorkResult{}
	if s == nil || ctx == nil || now < 0 || now > maxUnix || limit < 1 || limit > lifecycle.WorkerBatchLimit || budget <= 0 || budget > lifecycle.WorkerBudget {
		return result, ErrInvalid
	}
	started := time.Now()
	for _, key := range s.registry.keys {
		runtime := s.registry.modules[key].runtime
		if nilInterface(runtime) {
			continue
		}
		remaining := budget - time.Since(started)
		if remaining <= 0 || result.Processed >= limit {
			result.More = true
			break
		}
		var work lifecycle.WorkResult
		var err error
		if recovering {
			work, err = runtime.RecoverBeforeListener(ctx, now, limit-result.Processed, remaining)
		} else {
			work, err = runtime.Retain(ctx, now, limit-result.Processed, remaining)
		}
		if err != nil {
			return result, err
		}
		if work.Processed < 0 || work.Processed > limit-result.Processed {
			return result, ErrInvariant
		}
		result.Processed += work.Processed
		result.More = result.More || work.More
	}
	return result, nil
}
func (s *Service) RecoverBeforeListener(ctx context.Context, now int64, limit int, budget time.Duration) (lifecycle.WorkResult, error) {
	return s.runModules(ctx, now, limit, budget, true)
}
func (s *Service) Retain(ctx context.Context, now int64, limit int, budget time.Duration) (lifecycle.WorkResult, error) {
	return s.runModules(ctx, now, limit, budget, false)
}
