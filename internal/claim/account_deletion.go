package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

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

func (s *Service) prepareRequestDeletionTx(
	ctx context.Context,
	tx *sql.Tx,
	request Request,
	userID int64,
	decisionNow int64,
) error {
	claimIDs, err := requestClaimIDsTx(ctx, tx, request.ID)
	if err != nil {
		return err
	}
	if request.State == RequestTerminal {
		remaining, err := readRequestCapacityTx(ctx, tx, request.ID)
		if err != nil {
			return err
		}
		if remaining != 0 || request.ReservedMilli != 0 ||
			request.Route == RouteDiscovery && request.AccountingDisposition != AccountingNone ||
			request.Route != RouteDiscovery && request.AccountingDisposition != AccountingCommit && request.AccountingDisposition != AccountingRelease {
			return ErrInvariant
		}
		for _, claimID := range claimIDs {
			record, err := loadClaimTx(ctx, tx, claimID)
			if err != nil {
				return err
			}
			if record.state == StateClaimed || record.state == StateDispatched {
				return ErrInvariant
			}
		}
		return detachTerminalRequestTx(ctx, tx, request.ID, userID)
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
	switch request.Route {
	case RouteOpenAIChat:
		preserve = 1
	case RouteCharityChat:
		preserve = 1 + dispatchedClaims
	case RouteDiscovery:
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
