package runtime

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

var ErrLifecycleResourceLimit = errors.New("game runtime: lifecycle resource limit")

// LifecycleAdapter is the account-lifecycle seam. DeletionGuard spans the coordinator's
// outer transaction so a new start cannot enter between cleanup and account
// deletion.
type LifecycleAdapter struct{ service *Service }

func (service *Service) Lifecycle() *LifecycleAdapter { return &LifecycleAdapter{service: service} }

type DeletionGuard struct {
	service  *Service
	userID   int64
	commit   func() bool
	abort    func() bool
	prepared bool
}

func (adapter *LifecycleAdapter) BeginUserDeletion(userID int64) (*DeletionGuard, error) {
	if adapter == nil || adapter.service == nil {
		return nil, ErrClosed
	}
	commit, abort, err := adapter.service.limiter.BeginUserDeletion(userID)
	if err != nil {
		return nil, mapLimiter(err)
	}
	return &DeletionGuard{service: adapter.service, userID: userID, commit: commit, abort: abort}, nil
}

// Prepare applies Fishing's delete side in the caller-owned account deletion
// transaction. The caller must delete the user and commit that same tx before
// calling Commit; otherwise it calls Abort.
func (guard *DeletionGuard) Prepare(ctx context.Context, tx *sql.Tx, at int64) error {
	if guard == nil || guard.service == nil || tx == nil || guard.prepared {
		return ErrInvalidRequest
	}
	if err := guard.service.prepareDeletion(ctx, tx, guard.userID, at); err != nil {
		return err
	}
	guard.prepared = true
	return nil
}
func (guard *DeletionGuard) Commit() bool {
	if guard == nil || !guard.prepared {
		return false
	}
	return guard.commit()
}
func (guard *DeletionGuard) Abort() bool {
	if guard == nil {
		return false
	}
	return guard.abort()
}

// PrepareDeleteTx joins the caller-owned account deletion transaction. The
// lifecycle coordinator already holds the shared StartLimiter retirement
// boundary, so this method must not acquire a second per-user marker.
func (adapter *LifecycleAdapter) PrepareDeleteTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, decisionNow int64,
) error {
	if adapter == nil || adapter.service == nil || ctx == nil || tx == nil || userID <= 0 ||
		decisionNow < 0 || decisionNow > 253402300799 {
		return ErrInvalidRequest
	}
	return adapter.service.prepareDeletion(ctx, tx, userID, decisionNow)
}

func (service *Service) prepareDeletion(ctx context.Context, tx *sql.Tx, userID, at int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,operation_id,state,attempt_count,next_attempt_at,retry_exhausted,created_at,settled_at,revealed_at FROM game_fishing_batches WHERE user_id=? ORDER BY created_at,id`, userID)
	if err != nil {
		return classifyDB(err)
	}
	records := make([]batchRecord, 0)
	for rows.Next() {
		record, scanErr := scanBatch(rows)
		if scanErr != nil {
			rows.Close()
			return classifyDB(scanErr)
		}
		records = append(records, record)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return classifyDB(err)
	}
	rows.Close()
	for _, record := range records {
		if record.State != "reserved" {
			continue
		}
		userAccount, readErr := ledger.UserAccount(ctx, tx, userID)
		if readErr != nil {
			return ErrInvariant
		}
		reserveAccount, readErr := ledger.CodedAccount(ctx, tx, "game_fishing_reserve")
		if readErr != nil {
			return ErrInvariant
		}
		plan, planErr := ledger.NewFishingRelease(ledger.Meta{OperationID: record.OperationID, ActorUserID: 0, CreatedAt: at}, record.ID, reserveAccount.ID, userAccount.ID, ledger.AmountFromMilli(record.EntryTotal))
		if planErr != nil {
			return ErrInvariant
		}
		ref, _ := ledger.FishingReservation(record.ID)
		zero := db.EncodeU128(db.U128{})
		_, consumeErr := ledger.ConsumeReserved(ctx, tx, ref, plan, func(ctx context.Context, tx *sql.Tx) error {
			if _, deleteErr := tx.ExecContext(ctx, `DELETE FROM game_fishing_outcomes WHERE batch_id=?`, record.ID); deleteErr != nil {
				return classifyDB(deleteErr)
			}
			result, updateErr := tx.ExecContext(ctx, `UPDATE game_fishing_batches SET state='released',ledger_rows_remaining=?,attempt_count=0,next_attempt_at=NULL,last_error_class=NULL,retry_exhausted=0,settled_at=? WHERE id=? AND user_id=? AND state='reserved'`, zero, at, record.ID, userID)
			if updateErr != nil {
				return classifyDB(updateErr)
			}
			changed, _ := result.RowsAffected()
			if changed != 1 {
				return ErrConflict
			}
			if _, deleteErr := tx.ExecContext(ctx, `DELETE FROM game_fishing_batches WHERE id=? AND user_id=? AND state='released'`, record.ID, userID); deleteErr != nil {
				return classifyDB(deleteErr)
			}
			return nil
		})
		if consumeErr != nil {
			return mapLedger(consumeErr)
		}
	}
	for _, query := range []string{`DELETE FROM game_fishing_rank_facts WHERE user_id=?`, `DELETE FROM game_fishing_rank_aggregates WHERE user_id=?`, `DELETE FROM game_fishing_best WHERE user_id=?`, `DELETE FROM game_fishing_batches WHERE user_id=?`, `DELETE FROM game_user_preferences WHERE user_id=?`} {
		if _, err = tx.ExecContext(ctx, query, userID); err != nil {
			return classifyDB(err)
		}
	}
	return nil
}

// ExportUser preserves the package-local lifecycle seam for existing callers.
func (adapter *LifecycleAdapter) ExportUser(ctx context.Context, tx *sql.Tx, userID, now int64) (UserExport, error) {
	return adapter.ExportTx(ctx, tx, userID, now, 10_000)
}

// ExportTx reads only safe Fishing projections in the caller transaction.
// It applies the frozen collection limit independently to pending and terminal
// arrays and uses only decisionNow for lifecycle cutoffs.
func (adapter *LifecycleAdapter) ExportTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, decisionNow int64,
	limit int,
) (UserExport, error) {
	if adapter == nil || adapter.service == nil || ctx == nil || tx == nil || userID <= 0 ||
		decisionNow < 0 || decisionNow > 253402300799 || limit < 1 || limit > 10_000 {
		return UserExport{}, ErrInvalidRequest
	}
	if err := adapter.service.expireRankFactsInTx(ctx, tx, decisionNow, adapter.service.budgetNow().Add(rankBudget)); err != nil {
		return UserExport{}, err
	}
	result := UserExport{Pending: []FishingSettlementPending{}, Terminal: []FishingTerminalExport{}}
	rows, err := tx.QueryContext(ctx, `SELECT id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,operation_id,state,attempt_count,next_attempt_at,retry_exhausted,created_at,settled_at,revealed_at FROM game_fishing_batches WHERE user_id=? AND state='reserved' ORDER BY created_at,id LIMIT ?`, userID, limit+1)
	if err != nil {
		return result, classifyDB(err)
	}
	pendingRecords := make([]batchRecord, 0, 1)
	for rows.Next() {
		record, scanErr := scanBatch(rows)
		if scanErr != nil {
			rows.Close()
			return result, classifyDB(scanErr)
		}
		pendingRecords = append(pendingRecords, record)
		if len(pendingRecords) > limit {
			_ = rows.Close()
			return result, ErrLifecycleResourceLimit
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return result, classifyDB(err)
	}
	rows.Close()
	for _, record := range pendingRecords {
		result.Pending = append(result.Pending, *pendingFromRecord(record))
	}
	cutoff := decisionNow - int64(rankWindow/time.Second)
	rows, err = tx.QueryContext(ctx, `SELECT id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,operation_id,state,attempt_count,next_attempt_at,retry_exhausted,created_at,settled_at,revealed_at FROM game_fishing_batches WHERE user_id=? AND state='committed' AND settled_at>? ORDER BY created_at,id LIMIT ?`, userID, cutoff, limit+1)
	if err != nil {
		return result, classifyDB(err)
	}
	for rows.Next() {
		record, scanErr := scanBatch(rows)
		if scanErr != nil {
			_ = rows.Close()
			return result, classifyDB(scanErr)
		}
		terminal, loadErr := loadResultTx(ctx, tx, record, false)
		if loadErr != nil {
			_ = rows.Close()
			return result, loadErr
		}
		var revealedAt *int64
		if record.RevealedAt.Valid {
			value := record.RevealedAt.Int64
			revealedAt = &value
		}
		result.Terminal = append(result.Terminal, FishingTerminalExport{
			BatchID: terminal.BatchID, Bait: terminal.Bait, Count: terminal.Count,
			UnitPrice: terminal.UnitPrice, EntryTotal: terminal.EntryTotal, Outcomes: terminal.Outcomes,
			PayoutTotal: terminal.PayoutTotal, SettledAt: terminal.SettledAt, RevealedAt: revealedAt,
		})
		if len(result.Terminal) > limit {
			_ = rows.Close()
			return result, ErrLifecycleResourceLimit
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return result, classifyDB(err)
	}
	if err = rows.Close(); err != nil {
		return result, classifyDB(err)
	}
	single, err := querySingleLeaderboard(ctx, tx, userID, decisionNow)
	if err != nil {
		return result, err
	}
	result.Single = ownLeaderboardRow(single)
	total, err := queryTotalLeaderboard(ctx, tx, userID, decisionNow)
	if err != nil {
		return result, err
	}
	result.Total = ownLeaderboardRow(total)
	return result, nil
}

type RetentionResult struct {
	Processed int
	More      bool
}

// Retain processes at most limit due rank facts and terminal batches in one
// bounded domain transaction. Reserved batches are never retention targets.
func (adapter *LifecycleAdapter) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (RetentionResult, error) {
	if adapter == nil || adapter.service == nil || ctx == nil || decisionNow < 0 || decisionNow > 253402300799 ||
		limit < 1 || limit > workerBatchSize || budgetDeadline.IsZero() {
		return RetentionResult{}, ErrInvalidRequest
	}
	workerCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()
	tx, err := adapter.service.database.BeginTx(workerCtx, nil)
	if err != nil {
		return RetentionResult{}, classifyDB(err)
	}
	defer tx.Rollback()
	processed := 0
	rows, err := tx.QueryContext(workerCtx, `SELECT batch_id_text,user_id,expires_at,payout_total
FROM game_fishing_rank_facts
WHERE aggregate_applied=1 AND expires_at<=?
ORDER BY expires_at,batch_id_text LIMIT ?`, decisionNow, limit)
	if err != nil {
		return RetentionResult{}, classifyDB(err)
	}
	facts := make([]expiringFact, 0, limit)
	for rows.Next() {
		var fact expiringFact
		var payout []byte
		if err = rows.Scan(&fact.BatchID, &fact.UserID, &fact.ExpiresAt, &payout); err != nil {
			_ = rows.Close()
			return RetentionResult{}, classifyDB(err)
		}
		fact.Payout, err = db.DecodeU128(payout)
		if err != nil {
			_ = rows.Close()
			return RetentionResult{}, ErrInvariant
		}
		facts = append(facts, fact)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return RetentionResult{}, classifyDB(err)
	}
	if err = rows.Close(); err != nil {
		return RetentionResult{}, classifyDB(err)
	}
	for _, fact := range facts {
		if err := expireOneFact(workerCtx, tx, fact); err != nil {
			return RetentionResult{}, err
		}
	}
	processed += len(facts)

	cutoff := decisionNow - int64(rankWindow/time.Second)
	if processed < limit {
		rows, err = tx.QueryContext(workerCtx, `SELECT id FROM game_fishing_batches
WHERE state IN ('committed','released') AND settled_at<=?
ORDER BY settled_at,id LIMIT ?`, cutoff, limit-processed)
		if err != nil {
			return RetentionResult{}, classifyDB(err)
		}
		ids := make([]string, 0, limit-processed)
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				_ = rows.Close()
				return RetentionResult{}, classifyDB(err)
			}
			ids = append(ids, id)
		}
		if err = rows.Err(); err != nil {
			_ = rows.Close()
			return RetentionResult{}, classifyDB(err)
		}
		if err = rows.Close(); err != nil {
			return RetentionResult{}, classifyDB(err)
		}
		for _, id := range ids {
			result, deleteErr := tx.ExecContext(workerCtx, `DELETE FROM game_fishing_batches
WHERE id=? AND state IN ('committed','released') AND settled_at<=?`, id, cutoff)
			if deleteErr != nil {
				return RetentionResult{}, classifyDB(deleteErr)
			}
			changed, rowsErr := result.RowsAffected()
			if rowsErr != nil {
				return RetentionResult{}, classifyDB(rowsErr)
			}
			if changed != 1 {
				return RetentionResult{}, ErrConflict
			}
		}
		processed += len(ids)
	}
	var more int
	if err := tx.QueryRowContext(workerCtx, `SELECT
EXISTS(SELECT 1 FROM game_fishing_rank_facts WHERE aggregate_applied=1 AND expires_at<=?) OR
EXISTS(SELECT 1 FROM game_fishing_batches WHERE state IN ('committed','released') AND settled_at<=?)`,
		decisionNow, cutoff).Scan(&more); err != nil {
		return RetentionResult{}, classifyDB(err)
	}
	if more != 0 && more != 1 {
		return RetentionResult{}, ErrInvariant
	}
	if err := tx.Commit(); err != nil {
		return RetentionResult{}, classifyDB(err)
	}
	return RetentionResult{Processed: processed, More: more == 1}, nil
}

func ownLeaderboardRow(board FishingLeaderboard) *FishingLeaderboardRow {
	if board.Me != nil {
		copy := *board.Me
		return &copy
	}
	for _, row := range board.Entries {
		if row.IsMe {
			copy := row
			return &copy
		}
	}
	return nil
}

// Cleanup runs the bounded Fishing retention rail for the lifecycle owner.
// The background worker uses the same reducers; exposing this adapter lets
// the root six-hour maintenance pass request an immediate catch-up.
func (adapter *LifecycleAdapter) Cleanup(ctx context.Context, now int64) (int, error) {
	if adapter == nil || adapter.service == nil {
		return 0, ErrClosed
	}
	if err := adapter.service.expireRankFacts(ctx, now, adapter.service.budgetNow().Add(rankBudget)); err != nil {
		return 0, err
	}
	return adapter.service.cleanupTerminalBatches(ctx, now)
}
