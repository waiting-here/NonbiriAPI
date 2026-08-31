package linklink

import (
	"context"
	"database/sql"
	"strconv"
	"sync/atomic"
	"time"
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

// PrepareUserDeletion removes every LinkLink-owned row in the caller's user
// deletion transaction. The caller invokes Commit only after that transaction
// commits; a rollback needs no compensating database write. The caller already
// holds the deletion guard from the single shared game StartLimiter; this
// adapter must not begin a second marker on that same limiter.
func (adapter *LifecycleAdapter) PrepareUserDeletion(ctx context.Context, tx *sql.Tx, userID int64) (*DeletionFinalizer, error) {
	if adapter == nil || adapter.service == nil || tx == nil || userID <= 0 {
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
	if adapter == nil || adapter.service == nil || tx == nil || userID <= 0 || now < 0 || now > 253402300799 {
		return UserExport{}, ErrInvalidRequest
	}
	result := UserExport{Summaries: []Summary{}}
	record, found, err := loadSessionByUser(ctx, tx, userID)
	if err != nil {
		return UserExport{}, err
	}
	if found && now >= record.Deadline {
		if _, err := terminalize(ctx, tx, record, TerminalTimedOut, now); err != nil {
			return UserExport{}, err
		}
		found = false
	}
	if found {
		result.Active = &SafeActiveExport{
			SessionID: record.ID, Spec: record.Spec, Price: stateFromRecord(record, now).Price, State: "active",
			PairsRemoved: record.PairsRemoved, TotalPairs: record.Board.definition.totalPairs(),
			StartedAt: record.CreatedAt, Deadline: record.Deadline,
		}
	}
	cutoff := now - int64(summaryWindow/time.Second)
	if cutoff < 0 {
		cutoff = -1
	}
	rows, err := tx.QueryContext(ctx, `
SELECT session_id FROM game_linklink_summaries
WHERE user_id=? AND terminal_at>? ORDER BY terminal_at,session_id`, userID, cutoff)
	if err != nil {
		return UserExport{}, classifyDB(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return UserExport{}, classifyDB(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return UserExport{}, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return UserExport{}, classifyDB(err)
	}
	for _, id := range ids {
		summary, found, err := loadSummary(ctx, tx, userID, id)
		if err != nil || !found {
			if err == nil {
				err = ErrInvariant
			}
			return UserExport{}, err
		}
		result.Summaries = append(result.Summaries, summary)
	}
	return result, nil
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
