package linklink

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"sync/atomic"
	"time"
)

var (
	ErrLifecycleResourceLimit = errors.New("linklink: lifecycle resource limit")
	ErrPostCommitRequired     = errors.New("linklink: lifecycle post-commit finalizer required")
)

type LifecycleAdapter struct{ service *Service }

func (service *Service) Lifecycle() *LifecycleAdapter { return &LifecycleAdapter{service: service} }

func NewLifecycleAdapter(service *Service) *LifecycleAdapter {
	if service == nil {
		return &LifecycleAdapter{}
	}
	return service.Lifecycle()
}

type DeletionFinalizer struct {
	service *Service
	userID  int64
	done    atomic.Bool
}

type ExportFinalizer struct {
	service    *Service
	sessionIDs []string
	done       atomic.Bool
}

type RetentionResult struct {
	Processed int
	More      bool
}

// PrepareUserDeletion removes every LinkLink-owned row in the caller's user
// deletion transaction. The caller invokes Commit only after that transaction
// commits; a rollback needs no compensating database write. The caller already
// holds the deletion guard from the single shared game StartLimiter; this
// adapter must not begin a second marker on that same limiter.
func (adapter *LifecycleAdapter) PrepareUserDeletion(ctx context.Context, tx *sql.Tx, userID int64) (*DeletionFinalizer, error) {
	return adapter.prepareDeleteTx(ctx, tx, userID)
}

// PrepareDeleteTx joins the caller-owned account deletion transaction and
// validates the coordinator's transaction-wide frozen decision time. LinkLink
// has no time-dependent delete branch, so the value is deliberately not used
// after validation and no adapter-local clock is read.
func (adapter *LifecycleAdapter) PrepareDeleteTx(ctx context.Context, tx *sql.Tx, userID, decisionNow int64) (*DeletionFinalizer, error) {
	if decisionNow < 0 || decisionNow > 253402300799 {
		return nil, ErrInvalidRequest
	}
	return adapter.prepareDeleteTx(ctx, tx, userID)
}

func (adapter *LifecycleAdapter) prepareDeleteTx(ctx context.Context, tx *sql.Tx, userID int64) (*DeletionFinalizer, error) {
	if adapter == nil || adapter.service == nil || ctx == nil || tx == nil || userID <= 0 {
		return nil, ErrInvalidRequest
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_online_leases WHERE user_id=? AND substr(session_id,1,3)='ll_'`, userID); err != nil {
		return nil, classifyDB(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_linklink_sessions WHERE user_id=?`, userID); err != nil {
		return nil, classifyDB(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_linklink_summaries WHERE user_id=?`, userID); err != nil {
		return nil, classifyDB(err)
	}
	if err := deleteUserIdempotency(ctx, tx, userID); err != nil {
		return nil, err
	}
	return &DeletionFinalizer{service: adapter.service, userID: userID}, nil
}

func (finalizer *DeletionFinalizer) Commit() bool {
	if finalizer == nil || finalizer.service == nil || !finalizer.done.CompareAndSwap(false, true) {
		return false
	}
	finalizer.service.forgetUser(finalizer.userID)
	return true
}

func (finalizer *DeletionFinalizer) Abort() bool {
	return finalizer != nil && finalizer.done.CompareAndSwap(false, true)
}

func (adapter *LifecycleAdapter) ExportUser(ctx context.Context, tx *sql.Tx, userID, now int64) (UserExport, error) {
	result, finalizer, err := adapter.ExportTx(ctx, tx, userID, now, 10000)
	if err != nil {
		return UserExport{}, err
	}
	if finalizer != nil {
		_ = finalizer.Abort()
		return UserExport{}, ErrPostCommitRequired
	}
	return result, nil
}

// ExportTx reads LinkLink's safe export slice from the caller-owned
// transaction. Observing an expired active game terminalizes it using the
// supplied decisionNow. The returned finalizer must be committed only after
// the outer transaction commits, or aborted when that transaction rolls back.
func (adapter *LifecycleAdapter) ExportTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, decisionNow int64,
	limit int,
) (UserExport, *ExportFinalizer, error) {
	if adapter == nil || adapter.service == nil || ctx == nil || tx == nil || userID <= 0 ||
		decisionNow < 0 || decisionNow > 253402300799 || limit < 1 || limit > 10000 {
		return UserExport{}, nil, ErrInvalidRequest
	}
	result := UserExport{Summaries: []Summary{}}
	record, found, err := loadSessionByUser(ctx, tx, userID)
	if err != nil {
		return UserExport{}, nil, err
	}
	var finalizer *ExportFinalizer
	if found && decisionNow >= record.Deadline {
		if _, err := terminalize(ctx, tx, record, TerminalTimedOut, decisionNow); err != nil {
			return UserExport{}, nil, err
		}
		finalizer = &ExportFinalizer{service: adapter.service, sessionIDs: []string{record.ID}}
		found = false
	}
	if found {
		result.Active = &SafeActiveExport{
			SessionID: record.ID, Spec: record.Spec, Price: stateFromRecord(record, decisionNow).Price, State: "active",
			PairsRemoved: record.PairsRemoved, TotalPairs: record.Board.definition.totalPairs(),
			StartedAt: record.CreatedAt, Deadline: record.Deadline,
		}
	}
	cutoff := decisionNow - int64(summaryWindow/time.Second)
	if cutoff < 0 {
		cutoff = -1
	}
	rows, err := tx.QueryContext(ctx, `
SELECT session_id FROM game_linklink_summaries
WHERE user_id=? AND terminal_at>? ORDER BY terminal_at,session_id LIMIT ?`, userID, cutoff, limit+1)
	if err != nil {
		return UserExport{}, nil, classifyDB(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return UserExport{}, nil, classifyDB(err)
		}
		ids = append(ids, id)
		if len(ids) > limit {
			_ = rows.Close()
			return UserExport{}, nil, ErrLifecycleResourceLimit
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return UserExport{}, nil, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return UserExport{}, nil, classifyDB(err)
	}
	for _, id := range ids {
		summary, found, err := loadSummary(ctx, tx, userID, id)
		if err != nil || !found {
			if err == nil {
				err = ErrInvariant
			}
			return UserExport{}, nil, err
		}
		result.Summaries = append(result.Summaries, summary)
	}
	return result, finalizer, nil
}

func (finalizer *ExportFinalizer) Commit() bool {
	if finalizer == nil || finalizer.service == nil || !finalizer.done.CompareAndSwap(false, true) {
		return false
	}
	for _, sessionID := range finalizer.sessionIDs {
		finalizer.service.forgetSession(sessionID)
	}
	return true
}

func (finalizer *ExportFinalizer) Abort() bool {
	return finalizer != nil && finalizer.done.CompareAndSwap(false, true)
}

// Retain removes at most limit expired LinkLink summaries and technical lease
// rows in one domain-owned transaction. Deadline catch-up remains the recovery
// owner's job and is intentionally not duplicated here.
func (adapter *LifecycleAdapter) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (RetentionResult, error) {
	if adapter == nil || adapter.service == nil || ctx == nil || decisionNow < 0 || decisionNow > 253402300799 ||
		limit < 1 || limit > workerBatchSize {
		return RetentionResult{}, ErrInvalidRequest
	}
	service := adapter.service
	if service.closed.Load() || !service.recovered.Load() {
		return RetentionResult{}, ErrClosed
	}
	workerCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()
	tx, err := service.database.BeginTx(workerCtx, nil)
	if err != nil {
		return RetentionResult{}, classifyDB(err)
	}
	defer tx.Rollback()
	processed := 0
	cutoff := decisionNow - int64(summaryWindow/time.Second)
	if cutoff >= 0 && !retentionBudgetExpired(budgetDeadline) {
		result, err := tx.ExecContext(workerCtx, `DELETE FROM game_linklink_summaries WHERE session_id IN (
 SELECT session_id FROM game_linklink_summaries WHERE terminal_at<=? ORDER BY terminal_at,session_id LIMIT ?
)`, cutoff, limit-processed)
		if err != nil {
			return RetentionResult{}, classifyDB(err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return RetentionResult{}, classifyDB(err)
		}
		processed += int(changed)
	}
	if processed < limit && !retentionBudgetExpired(budgetDeadline) {
		result, err := tx.ExecContext(workerCtx, `DELETE FROM game_online_leases WHERE rowid IN (
 SELECT rowid FROM game_online_leases
 WHERE substr(session_id,1,3)='ll_' AND (expires_at<=? OR health_epoch<>?)
 ORDER BY expires_at,session_id,user_id LIMIT ?
)`, decisionNow, service.healthEpoch, limit-processed)
		if err != nil {
			return RetentionResult{}, classifyDB(err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return RetentionResult{}, classifyDB(err)
		}
		processed += int(changed)
	}
	var more int
	if err := tx.QueryRowContext(workerCtx, `SELECT EXISTS(
 SELECT 1 FROM game_linklink_summaries WHERE terminal_at<=?
) OR EXISTS(
 SELECT 1 FROM game_online_leases
 WHERE substr(session_id,1,3)='ll_' AND (expires_at<=? OR health_epoch<>?)
)`, cutoff, decisionNow, service.healthEpoch).Scan(&more); err != nil {
		return RetentionResult{}, classifyDB(err)
	}
	if more != 0 && more != 1 {
		return RetentionResult{}, ErrInvariant
	}
	if err := tx.Commit(); err != nil {
		return RetentionResult{}, classifyDB(err)
	}
	service.forgetExpiredLeases(decisionNow)
	return RetentionResult{Processed: processed, More: more == 1}, nil
}

func retentionBudgetExpired(deadline time.Time) bool {
	return !deadline.IsZero() && !time.Now().Before(deadline)
}

func (adapter *LifecycleAdapter) Cleanup(ctx context.Context, now int64) (int, error) {
	if adapter == nil || adapter.service == nil || now < 0 || now > 253402300799 {
		return 0, ErrInvalidRequest
	}
	for {
		processed, err := adapter.service.runDue(ctx, now)
		if err != nil {
			return 0, err
		}
		if processed < workerBatchSize {
			break
		}
	}
	cutoff := now - int64(summaryWindow/time.Second)
	if cutoff < 0 {
		return 0, nil
	}
	result, err := adapter.service.database.ExecContext(ctx, `
DELETE FROM game_linklink_summaries WHERE session_id IN (
 SELECT session_id FROM game_linklink_summaries WHERE terminal_at<=? ORDER BY terminal_at,session_id LIMIT ?
)`, cutoff, workerBatchSize)
	if err != nil {
		return 0, classifyDB(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, classifyDB(err)
	}
	return int(changed), nil
}

func (service *Service) ActiveCounts(ctx context.Context) ([]ActiveCount, error) {
	if service == nil || service.closed.Load() {
		return nil, ErrClosed
	}
	now, err := service.decisionNow()
	if err != nil {
		return nil, err
	}
	for {
		processed, err := service.runDue(ctx, now)
		if err != nil {
			return nil, err
		}
		if processed < workerBatchSize {
			break
		}
	}
	counts := map[string]int64{"6x8": 0, "8x8": 0, "10x10": 0}
	rows, err := service.database.QueryContext(ctx, `SELECT spec,COUNT(*) FROM game_linklink_sessions GROUP BY spec`)
	if err != nil {
		return nil, classifyDB(err)
	}
	defer rows.Close()
	for rows.Next() {
		var spec string
		var count int64
		if err := rows.Scan(&spec, &count); err != nil {
			return nil, classifyDB(err)
		}
		if _, known := counts[spec]; !known || count < 0 {
			return nil, ErrInvariant
		}
		counts[spec] = count
	}
	if err := rows.Err(); err != nil {
		return nil, classifyDB(err)
	}
	result := make([]ActiveCount, 0, 3)
	for _, spec := range []string{"6x8", "8x8", "10x10"} {
		result = append(result, ActiveCount{Spec: spec, Count: strconv.FormatInt(counts[spec], 10)})
	}
	return result, nil
}
