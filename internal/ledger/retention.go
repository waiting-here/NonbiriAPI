package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const DetailRetentionSeconds int64 = 30 * 24 * 60 * 60

type RetentionResult struct {
	Processed int
	More      bool
}

type retentionOperation struct {
	id, source, sourceID string
	seq, at              int64
	compacted            bool
}

// RetainDetails advances a contiguous opening balance, then revisits older
// protected operations and referenced receipts once per maintenance pass.
// Projection and deletion share SQLite's write transaction with settlement.
func RetainDetails(ctx context.Context, database *sql.DB, now int64, limit int) (RetentionResult, error) {
	if ctx == nil || database == nil || !validUnix(now) || limit < 1 || limit > 100 {
		return RetentionResult{}, ErrInvalidPlan
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return RetentionResult{}, err
	}
	defer tx.Rollback()
	var through, before, sweepAt, sweepAfter, projected int64
	err = tx.QueryRowContext(ctx, `SELECT through_seq,details_before,sweep_at,sweep_after_seq FROM credit_compaction WHERE id=1`).Scan(&through, &before, &sweepAt, &sweepAfter)
	if err != nil {
		return RetentionResult{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT last_ledger_seq FROM economy_audit_checkpoint WHERE id=1`).Scan(&projected); err != nil {
		return RetentionResult{}, err
	}
	if through > projected {
		return RetentionResult{}, ErrInvariant
	}
	batch, err := retentionBatch(ctx, tx, through, projected, limit)
	if err != nil {
		return RetentionResult{}, err
	}
	cutoff := now - DetailRetentionSeconds
	count := 0
	for _, operation := range batch {
		if operation.at > cutoff {
			break
		}
		if operation.seq != through+1 || operation.compacted {
			return RetentionResult{}, ErrInvariant
		}
		result, err := loadResult(ctx, tx, operation.id)
		if err != nil {
			return RetentionResult{}, err
		}
		if err = addOpeningBalances(ctx, tx, result); err != nil {
			return RetentionResult{}, err
		}
		through = operation.seq
		before = max(before, operation.at+1)
		if _, err = tx.ExecContext(ctx, `UPDATE credit_compaction SET through_seq=?,details_before=? WHERE id=1`, through, before); err != nil {
			return RetentionResult{}, err
		}
		if err = compactOperation(ctx, tx, operation); err != nil {
			return RetentionResult{}, err
		}
		count++
	}
	if count > 0 {
		if err = tx.Commit(); err != nil {
			return RetentionResult{}, err
		}
		return RetentionResult{Processed: count, More: true}, nil
	}
	if sweepAt != now {
		sweepAfter = 0
	}
	batch, err = retentionBatch(ctx, tx, sweepAfter, through, limit)
	if err != nil {
		return RetentionResult{}, err
	}
	for _, operation := range batch {
		if err = compactOperation(ctx, tx, operation); err != nil {
			return RetentionResult{}, err
		}
		sweepAfter = operation.seq
	}
	if _, err = tx.ExecContext(ctx, `UPDATE credit_compaction SET sweep_at=?,sweep_after_seq=? WHERE id=1`, now, sweepAfter); err != nil {
		return RetentionResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return RetentionResult{}, err
	}
	return RetentionResult{Processed: len(batch), More: len(batch) == limit}, nil
}

func retentionBatch(ctx context.Context, tx *sql.Tx, after, through int64, limit int) ([]retentionOperation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,ledger_seq,created_at,source_type,source_id,compacted FROM credit_operations WHERE ledger_seq>? AND ledger_seq<=? ORDER BY ledger_seq LIMIT ?`, after, through, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	batch := make([]retentionOperation, 0, limit)
	for rows.Next() {
		var operation retentionOperation
		if err := rows.Scan(&operation.id, &operation.seq, &operation.at, &operation.source, &operation.sourceID, &operation.compacted); err != nil {
			return nil, err
		}
		batch = append(batch, operation)
	}
	return batch, rows.Err()
}

func addOpeningBalances(ctx context.Context, tx *sql.Tx, operation Result) error {
	for _, entry := range operation.Entries {
		if entry.AccountID == 0 {
			continue
		}
		var sign int
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT balance_sign,balance_mag FROM credit_opening_balances WHERE account_id=?`, entry.AccountID).Scan(&sign, &raw)
		balance := Amount{}
		if err == nil {
			balance, err = amountFromParts(sign, raw)
		} else if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		if err != nil {
			return err
		}
		balance, err = addAmounts(balance, entry.Delta)
		if err != nil {
			return err
		}
		if entry.BalanceAfter != nil && balance.Big().Cmp(entry.BalanceAfter.Big()) != 0 {
			return ErrInvariant
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO credit_opening_balances(account_id,balance_sign,balance_mag) VALUES(?,?,?) ON CONFLICT(account_id) DO UPDATE SET balance_sign=excluded.balance_sign,balance_mag=excluded.balance_mag`, entry.AccountID, balance.Sign(), db.EncodeU128(balance.value.Mag)); err != nil {
			return err
		}
	}
	return nil
}

func compactOperation(ctx context.Context, tx *sql.Tx, operation retentionOperation) error {
	protected, sourceExists, err := retentionSource(ctx, tx, operation)
	if err != nil || protected {
		return err
	}
	if !operation.compacted {
		_, err = tx.ExecContext(ctx, `UPDATE credit_operations SET compacted=1,actor_user_id=NULL,donation_credit_user_id=NULL,donation_credit_delta_sign=0,donation_credit_delta_mag=zeroblob(16),donation_credit_after=NULL,reason=NULL WHERE id=?`, operation.id)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM credit_entries WHERE operation_id=?`, operation.id); err != nil {
			return err
		}
	}
	if sourceExists {
		return nil
	}
	var referenced bool
	if err = tx.QueryRowContext(ctx, retainedOperationReferenceQuery, operation.id).Scan(&referenced); err != nil {
		return err
	}
	if !referenced {
		_, err = tx.ExecContext(ctx, `DELETE FROM credit_operations WHERE id=?`, operation.id)
	}
	return err
}

// These references preserve existing domain receipts, including older fields
// that intentionally do not have foreign keys. They retain no ledger lines.
var retainedOperationReferences = [][2]string{
	{"checkins", "operation_id"}, {"welfare_claims", "operation_id"}, {"game_checkins", "operation_id"},
	{"game_onboarding_completions", "operation_id"}, {"activity_loans", "operation_id"},
	{"game_fishing_batches", "operation_id"}, {"game_linklink_sessions", "operation_id"},
	{"game_rps_queue", "reservation_operation_id"}, {"game_rps_sessions", "terminal_operation_id"},
	{"game_duel_queue", "reservation_operation_id"}, {"game_duel_sessions", "terminal_operation_id"},
	{"game_blackjack_payments", "reserve_operation_id"}, {"game_blackjack_payments", "terminal_operation_id"},
	{"abuse_actions", "operation_id"}, {"image_activity_tasks", "reserve_operation_id"}, {"image_activity_tasks", "terminal_operation_id"},
	{"activity_exchange_receipts", "operation_id"}, {"inactivity_runs", "ledger_operation_id"},
	{"fatfish_challenges", "ticket_operation_id"}, {"fatfish_challenges", "refund_operation_id"},
	{"fatfish_progress", "unlock_operation_id"}, {"fatfish_reward_claims", "operation_id"}, {"fatfish_financial_receipts", "operation_id"},
	{"lake_notes_entitlements", "ledger_operation_id"}, {"lake_notes_exchange_receipts", "ledger_operation_id"},
}

var retainedOperationReferenceQuery = func() string {
	parts := make([]string, 0, len(retainedOperationReferences))
	for _, ref := range retainedOperationReferences {
		parts = append(parts, fmt.Sprintf("EXISTS(SELECT 1 FROM %s WHERE %s=?1)", ref[0], ref[1]))
	}
	return "SELECT " + strings.Join(parts, " OR ")
}()

func retentionSource(ctx context.Context, tx *sql.Tx, operation retentionOperation) (protected, exists bool, err error) {
	var table, active string
	switch sourceType(operation.source) {
	case sourceLogicalRequest:
		table, active = "logical_requests", "state IN ('accepted','running') OR ledger_rows_remaining<>zeroblob(16)"
	case sourceDispatchClaim:
		table, active = "dispatch_claims", "state IN ('claimed','dispatched') OR donor_reward_state='pending'"
	case sourcePeriod:
		table, active = "thursday_periods", "state IN ('configured','open','settling','configuration_error')"
	case sourceFishingBatch:
		table, active = "game_fishing_batches", "state='reserved'"
	case sourceLinkLinkSession:
		table, active = "game_linklink_sessions", "state='active'"
	case sourceRPSQueue:
		table, active = "game_rps_queue", "1"
	case sourceRPSSession:
		table, active = "game_rps_sessions", "1"
	case sourceDuelQueue:
		table, active = "game_duel_queue", "1"
	case sourceDuelSession:
		table, active = "game_duel_sessions", "state='active'"
	case sourceBlackjackPayment:
		table, active = "game_blackjack_payments", "state='reserved'"
	case sourceImageTask:
		table, active = "image_activity_tasks", "finance_state='reserved'"
	case sourceOperation:
	default:
		return false, false, ErrInvariant
	}
	if table != "" {
		err = tx.QueryRowContext(ctx, "SELECT "+active+" FROM "+table+" WHERE id=?", operation.sourceID).Scan(&protected)
		if err == nil {
			exists = true
		} else if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		if err != nil || protected {
			return
		}
	}
	// A request hold protects its full financial evidence, and a donation hold
	// protects the associated donor reward until the hold ends.
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM request_logs l JOIN legal_holds h ON h.object_kind='request_log' AND h.object_ref=CAST(l.id AS TEXT) AND h.state='active'
 WHERE l.logical_request_id=?1 OR l.logical_request_id=(SELECT logical_request_id FROM dispatch_claims WHERE id=?1)
 OR (?2='operation' AND l.logical_request_id='req_'||substr(?1,4)))
 OR EXISTS(SELECT 1 FROM dispatch_claims c JOIN donation_keys k ON k.id=c.donation_key_id
 JOIN legal_holds h ON h.object_kind='donation' AND h.object_ref=CAST(k.donation_id AS TEXT) AND h.state='active' WHERE c.id=?1)
 OR EXISTS(SELECT 1 FROM fatfish_challenges WHERE ticket_operation_id=?3 AND terminal_at_ms IS NULL)`, operation.sourceID, operation.source, operation.id).Scan(&protected)
	return
}
