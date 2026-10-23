package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const terminalDeletionRequestsSQL = `SELECT
r.route_kind,r.state,r.attempt_limit,r.accounting_state,r.account_reserved_milli,
r.settlement_destination,r.ledger_rows_remaining,r.rejection_stage IS NOT NULL,
EXISTS(SELECT 1 FROM dispatch_claims c WHERE c.logical_request_id=r.id)
FROM logical_requests r
WHERE r.user_id=? AND r.state='terminal'
ORDER BY r.created_at,r.id`

const terminalDeletionClaimsSQL = `SELECT c.state,c.purpose,c.attempt_seq,c.secret_ref_id,l.id
FROM logical_requests r
JOIN dispatch_claims c ON c.logical_request_id=r.id
LEFT JOIN request_logs l ON l.logical_request_id=r.id
WHERE r.user_id=? AND r.state='terminal'`

const detachTerminalRequestsSQL = `UPDATE logical_requests SET user_id=NULL
WHERE user_id=? AND state='terminal'`

// PrepareAccountDeletion joins a caller-owned transaction and linearizes all
// logical requests still attached to userID. The caller owns commit/rollback.
// Dispatched work remains completable from its frozen claim and secret facts;
// work that never dispatched is released through the ordinary terminal path.
func (s *Service) PrepareAccountDeletion(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	decisionNow int64,
) error {
	if s == nil || s.db == nil || ctx == nil || tx == nil || userID <= 0 ||
		decisionNow < 0 || decisionNow > maxUnixSecond {
		return ErrInvalidInput
	}
	if err := detachTerminalRequestsTx(ctx, tx, userID); err != nil {
		return err
	}

	for {
		var requestID string
		err := tx.QueryRowContext(ctx, `SELECT id
FROM logical_requests
WHERE user_id=?
ORDER BY created_at,id
LIMIT 1`, userID).Scan(&requestID)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return fmt.Errorf("claim: select account deletion request: %w", err)
		}
		request, err := loadRequestTx(ctx, tx, requestID)
		if err != nil {
			return err
		}
		if request.UserID == nil || *request.UserID != userID {
			return ErrConflict
		}
		if err := s.prepareRequestDeletionTx(ctx, tx, request, userID, decisionNow); err != nil {
			return fmt.Errorf("claim: prepare request %s for account deletion: %w", request.ID, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE charity_reservations SET user_id=NULL WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("claim: detach charity reservation identity: %w", err)
	}
	var attached int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM charity_reservations WHERE user_id=?)`, userID).Scan(&attached); err != nil {
		return fmt.Errorf("claim: verify charity reservation detachment: %w", err)
	}
	if attached != 0 {
		return ErrInvariant
	}
	return nil
}

func detachTerminalRequestsTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	count, err := validateTerminalDeletionRequestsTx(ctx, tx, userID)
	if err != nil || count == 0 {
		return err
	}
	if err := validateTerminalDeletionClaimsTx(ctx, tx, userID); err != nil {
		return err
	}
	// Both scans finish before this write. The caller's transaction also owns
	// the per-request log snapshot triggers and any subsequent active handoff.
	result, err := tx.ExecContext(ctx, detachTerminalRequestsSQL, userID)
	if err != nil {
		return fmt.Errorf("claim: detach terminal request identities: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("claim: count detached terminal requests: %w", err)
	}
	if changed != count {
		return ErrInvariant
	}
	return nil
}

func validateTerminalDeletionRequestsTx(ctx context.Context, tx *sql.Tx, userID int64) (int64, error) {
	rows, err := tx.QueryContext(ctx, terminalDeletionRequestsSQL, userID)
	if err != nil {
		return 0, fmt.Errorf("claim: read terminal deletion requests: %w", err)
	}
	defer rows.Close()
	var count int64
	for rows.Next() {
		var request Request
		var remainingBlob []byte
		var rejected, hasClaims bool
		if err := rows.Scan(&request.Route, &request.State, &request.AttemptLimit,
			&request.AccountingDisposition, &request.ReservedMilli, &request.Destination,
			&remainingBlob, &rejected, &hasClaims); err != nil {
			return 0, fmt.Errorf("claim: scan terminal deletion request: %w", err)
		}
		remaining, validCapacity := decodeSmallU128(remainingBlob)
		if !validPersistedRequest(request) || !validCapacity || remaining != 0 || request.ReservedMilli != 0 ||
			rejected && (request.AccountingDisposition != AccountingNone || hasClaims) ||
			request.Route == RouteDiscovery && request.AccountingDisposition != AccountingNone ||
			!rejected && request.Route != RouteDiscovery && request.AccountingDisposition != AccountingCommit && request.AccountingDisposition != AccountingRelease {
			return 0, ErrInvariant
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("claim: iterate terminal deletion requests: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("claim: close terminal deletion requests: %w", err)
	}
	return count, nil
}

func validateTerminalDeletionClaimsTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	rows, err := tx.QueryContext(ctx, terminalDeletionClaimsSQL, userID)
	if err != nil {
		return fmt.Errorf("claim: read terminal deletion claims: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var state ClaimState
		var purpose Purpose
		var attemptSeq int
		var secretRefID, requestLogID sql.NullInt64
		if err := rows.Scan(&state, &purpose, &attemptSeq, &secretRefID, &requestLogID); err != nil {
			return fmt.Errorf("claim: scan terminal deletion claim: %w", err)
		}
		// Terminal logs may already have expired. Claims must still satisfy the
		// same terminal invariants; an extant log must have a valid identity.
		if !validClaimState(state) || !validPurpose(purpose) ||
			state == StateClaimed || state == StateDispatched ||
			attemptSeq < 1 || attemptSeq > MaxAttempts || secretRefID.Valid ||
			requestLogID.Valid && requestLogID.Int64 <= 0 {
			return ErrInvariant
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("claim: iterate terminal deletion claims: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("claim: close terminal deletion claims: %w", err)
	}
	return nil
}

func (s *Service) prepareRequestDeletionTx(
	ctx context.Context,
	tx *sql.Tx,
	request Request,
	userID int64,
	decisionNow int64,
) error {
	if request.State == RequestTerminal {
		return ErrInvariant
	}
	claimIDs, err := requestClaimIDsTx(ctx, tx, request.ID)
	if err != nil {
		return err
	}
	hadDispatch := false
	dispatchedClaims := uint64(0)
	for _, claimID := range claimIDs {
		record, err := loadClaimTx(ctx, tx, claimID)
		if err != nil {
			return err
		}
		if record.dispatchedAt.Valid {
			hadDispatch = true
		}
		if record.state == StateDispatched {
			dispatchedClaims++
		}
		if record.state == StateClaimed {
			if _, err := s.releaseClaimTx(ctx, tx, record, decisionNow); err != nil {
				return err
			}
		}
	}

	if !hadDispatch {
		disposition := AccountingRelease
		if request.Route == RouteDiscovery {
			disposition = AccountingNone
		}
		completed, err := s.completeRequestTx(ctx, tx, request, CompleteRequestInput{
			RequestID:   request.ID,
			Caller:      CallerResult{Class: ResultCancelled},
			Disposition: disposition,
		}, decisionNow)
		if err != nil {
			return err
		}
		return detachTerminalRequestTx(ctx, tx, completed.ID, userID)
	}

	remaining, err := readRequestCapacityTx(ctx, tx, request.ID)
	if err != nil {
		return err
	}
	var preserve uint64
	switch {
	case request.Route.IsSelf():
		preserve = 1
	case request.Route.IsCharity():
		preserve = 1 + dispatchedClaims
	case request.Route == RouteDiscovery:
		preserve = 0
	default:
		return ErrInvariant
	}
	if remaining < preserve {
		return ErrInvariant
	}
	if remaining > preserve {
		if request.Route == RouteDiscovery || s.accounting == nil || remaining-preserve > MaxAttempts {
			return ErrInvariant
		}
		before := remaining
		after := preserve
		rows := uint16(before - after)
		persist := func(callbackCtx context.Context, callbackTx *sql.Tx) error {
			if callbackCtx == nil || callbackTx == nil {
				return ErrInvariant
			}
			return setRequestCapacityTx(callbackCtx, callbackTx, request.ID, before, after)
		}
		if err := s.accounting.ReleaseUnusedForDeletion(ctx, tx, request.ID, rows, persist); err != nil {
			return fmt.Errorf("claim: release unused deletion capacity: %w", err)
		}
	}

	result, err := tx.ExecContext(ctx, `UPDATE logical_requests
SET user_id=NULL,settlement_destination='external'
WHERE id=? AND user_id=? AND state IN ('accepted','running')
  AND settlement_destination='user'`, request.ID, userID)
	if err != nil {
		return fmt.Errorf("claim: hand off dispatched request settlement: %w", err)
	}
	return requireOneRow(result)
}

func requestClaimIDsTx(ctx context.Context, tx *sql.Tx, requestID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM dispatch_claims
WHERE logical_request_id=?
ORDER BY attempt_seq,id`, requestID)
	if err != nil {
		return nil, fmt.Errorf("claim: list account deletion claims: %w", err)
	}
	defer rows.Close()
	claimIDs := make([]string, 0, MaxAttempts)
	for rows.Next() {
		var claimID string
		if err := rows.Scan(&claimID); err != nil {
			return nil, fmt.Errorf("claim: scan account deletion claim: %w", err)
		}
		claimIDs = append(claimIDs, claimID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("claim: iterate account deletion claims: %w", err)
	}
	return claimIDs, nil
}

func detachTerminalRequestTx(ctx context.Context, tx *sql.Tx, requestID string, userID int64) error {
	result, err := tx.ExecContext(ctx, `UPDATE logical_requests SET user_id=NULL
WHERE id=? AND user_id=? AND state='terminal'`, requestID, userID)
	if err != nil {
		return fmt.Errorf("claim: detach terminal request identity: %w", err)
	}
	return requireOneRow(result)
}
