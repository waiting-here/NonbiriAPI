package runtime

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

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

// ExportUser reads only safe Fishing projections in the caller transaction.
func (adapter *LifecycleAdapter) ExportUser(ctx context.Context, tx *sql.Tx, userID, now int64) (UserExport, error) {
	if adapter == nil || adapter.service == nil || tx == nil || userID <= 0 {
		return UserExport{}, ErrInvalidRequest
	}
	if err := adapter.service.expireRankFactsInTx(ctx, tx, now, adapter.service.budgetNow().Add(rankBudget)); err != nil {
		return UserExport{}, err
	}
	result := UserExport{Pending: []FishingSettlementPending{}, Terminal: []FishingBatchResult{}}
	rows, err := tx.QueryContext(ctx, `SELECT id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,operation_id,state,attempt_count,next_attempt_at,retry_exhausted,created_at,settled_at,revealed_at FROM game_fishing_batches WHERE user_id=? AND (state='reserved' OR (state='committed' AND settled_at>?)) ORDER BY created_at,id`, userID, now-int64(rankWindow.Seconds()))
	if err != nil {
		return result, classifyDB(err)
	}
	records := make([]batchRecord, 0)
	for rows.Next() {
		record, scanErr := scanBatch(rows)
		if scanErr != nil {
			rows.Close()
			return result, classifyDB(scanErr)
		}
		records = append(records, record)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return result, classifyDB(err)
	}
	rows.Close()
	for _, record := range records {
		if record.State == "reserved" {
			result.Pending = append(result.Pending, *pendingFromRecord(record))
			continue
		}
		terminal, loadErr := loadResultTx(ctx, tx, record, false)
		if loadErr != nil {
			return result, loadErr
		}
		result.Terminal = append(result.Terminal, *terminal)
	}
	single, err := querySingleLeaderboard(ctx, tx, userID, now)
	if err != nil {
		return result, err
	}
	result.Single = ownLeaderboardRow(single)
	total, err := queryTotalLeaderboard(ctx, tx, userID, now)
	if err != nil {
		return result, err
	}
	result.Total = ownLeaderboardRow(total)
	return result, nil
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
