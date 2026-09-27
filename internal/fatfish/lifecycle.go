package fatfish

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
)

type ActivityRuntime struct{ service *Service }
type LifecycleAdapter struct{ service *Service }

func (s *Service) ActivityRuntime() *ActivityRuntime   { return &ActivityRuntime{service: s} }
func (s *Service) LifecycleAdapter() *LifecycleAdapter { return &LifecycleAdapter{service: s} }

var _ limitedactivities.Runtime = (*ActivityRuntime)(nil)
var _ lifecycle.FatFishLifecycle = (*LifecycleAdapter)(nil)

type jobFinalizer struct {
	mu      sync.Mutex
	service *Service
	ids     []string
	done    bool
}

func (f *jobFinalizer) Commit() bool {
	if f == nil {
		return true
	}
	f.mu.Lock()
	if f.done {
		f.mu.Unlock()
		return false
	}
	f.done = true
	ids := append([]string(nil), f.ids...)
	f.mu.Unlock()
	for _, id := range ids {
		f.service.cancelJobAndWait(id)
	}
	return true
}
func (f *jobFinalizer) Abort() bool {
	if f == nil {
		return true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.done {
		return false
	}
	f.done = true
	return true
}

func (a *ActivityRuntime) ReadyTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	if a == nil || a.service == nil || tx == nil {
		return false, ErrInvalid
	}
	var id int
	err := tx.QueryRowContext(ctx, `SELECT id FROM fatfish_capacity WHERE id=1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (a *ActivityRuntime) PreparePauseTx(ctx context.Context, tx *sql.Tx, now int64) (limitedactivities.Finalizer, error) {
	return a.service.expirePreparedTx(ctx, tx, now, "activity_paused")
}
func (a *ActivityRuntime) PrepareMaintenanceTx(ctx context.Context, tx *sql.Tx, now int64) (limitedactivities.Finalizer, error) {
	return a.service.expirePreparedTx(ctx, tx, now, "maintenance")
}
func (a *ActivityRuntime) PrepareBanTx(ctx context.Context, tx *sql.Tx, user, now int64) (limitedactivities.Finalizer, error) {
	return a.service.CancelUserTx(ctx, tx, user, now, "account_banned")
}

// The source-aware lifecycle adapter has already handled refunds in this same
// transaction. A second runtime pass can only assert that no live slot remains.
func (a *ActivityRuntime) PrepareDeleteTx(ctx context.Context, tx *sql.Tx, user, now int64) (limitedactivities.Finalizer, error) {
	if a == nil || a.service == nil || tx == nil || user <= 0 || now < 0 {
		return nil, ErrInvalid
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fatfish_challenges WHERE user_id=? AND state IN ('prepared','active','verifying')`, user).Scan(&count); err != nil {
		return nil, err
	}
	if count != 0 {
		return nil, ErrInvariant
	}
	return nil, nil
}

func (s *Service) expirePreparedTx(ctx context.Context, tx *sql.Tx, now int64, reason string) (limitedactivities.Finalizer, error) {
	if s == nil || tx == nil || now < 0 || now > maximumUnix {
		return nil, ErrInvalid
	}
	_, err := tx.ExecContext(ctx, `UPDATE fatfish_challenges SET state='expired',terminal_at_ms=MAX(?,prepared_at_ms),terminal_reason=?,revision=revision+1 WHERE state='prepared'`, now*1000, reason)
	return nil, err
}

// CancelUserTx joins the caller's ban transaction. Only the finalizer may
// cancel process-local replay work, after the authoritative state commits.
func (s *Service) CancelUserTx(ctx context.Context, tx *sql.Tx, userID, now int64, reason string) (limitedactivities.Finalizer, error) {
	if s == nil || tx == nil || userID <= 0 || now < 0 || now > maximumUnix || len(reason) == 0 || len(reason) > 128 {
		return nil, ErrInvalid
	}
	return s.cancelUserTx(ctx, tx, userID, now*1000, reason, true)
}

func (s *Service) cancelUserTx(ctx context.Context, tx *sql.Tx, userID, nowMS int64, reason string, refund bool) (*jobFinalizer, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM fatfish_challenges WHERE user_id=? AND state IN ('prepared','active','verifying')`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return &jobFinalizer{service: s}, nil
	}
	if err != nil {
		return nil, err
	}
	c, err := readChallengeTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err = s.cancelChallengeTx(ctx, tx, c, nowMS, reason, refund); err != nil {
		return nil, err
	}
	return &jobFinalizer{service: s, ids: []string{id}}, nil
}

func (s *Service) cancelChallengeTx(ctx context.Context, tx *sql.Tx, c challengeRow, nowMS int64, reason string, refund bool) error {
	if len(reason) == 0 || len(reason) > 128 {
		return ErrInvalid
	}
	if c.state != "prepared" && c.state != "active" && c.state != "verifying" {
		return nil
	}
	if c.playtest {
		refund = false
	}
	if nowMS < c.prepared {
		nowMS = c.prepared
	}
	state := "abandoned"
	var op sql.NullString
	if c.state == "prepared" {
		state = "expired"
	} else if refund {
		var err error
		op, err = s.refundTx(ctx, tx, c, nowMS)
		if err != nil {
			return err
		}
		state = "cancelled_refunded"
	}
	res, err := tx.ExecContext(ctx, `UPDATE fatfish_challenges SET state=?,terminal_at_ms=?,terminal_reason=?,refund_operation_id=?,revision=revision+1
 WHERE id=? AND state=? AND revision=?`, state, nowMS, reason, op, c.id, c.state, c.revision)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Service) refundSystemFault(ctx context.Context, id, reason string) error {
	nowMS, err := s.nowMS()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := readChallengeTx(ctx, tx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.state != "active" && c.state != "verifying" {
		return tx.Commit()
	}
	if err = s.cancelChallengeTx(ctx, tx, c, nowMS, reason, true); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *LifecycleAdapter) PrepareDelete(ctx context.Context, tx *sql.Tx, request lifecycle.DeleteRequest) (lifecycle.DeleteFinalizer, error) {
	if a == nil || a.service == nil || tx == nil || request.UserID <= 0 || !request.Source.Valid() || request.DecisionNow < 0 || request.DecisionNow > maximumUnix {
		return nil, ErrInvalid
	}
	return a.service.cancelUserTx(ctx, tx, request.UserID, request.DecisionNow*1000, "account_deleted", request.Source != lifecycle.DeleteSelf)
}

func (a *ActivityRuntime) RecoverBeforeListener(ctx context.Context, now int64, limit int, budget time.Duration) (lifecycle.WorkResult, error) {
	return a.service.recover(ctx, now, limit, time.Now().Add(budget))
}
func (a *ActivityRuntime) Retain(ctx context.Context, now int64, limit int, budget time.Duration) (lifecycle.WorkResult, error) {
	return a.service.retain(ctx, now, limit, time.Now().Add(budget))
}
func (a *LifecycleAdapter) RecoverBeforeListener(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	return a.service.recover(ctx, now, limit, deadline)
}
func (a *LifecycleAdapter) Retain(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	return a.service.retain(ctx, now, limit, deadline)
}
