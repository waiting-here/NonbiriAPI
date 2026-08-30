package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// RecoverNonterminal is the startup recovery rail. It must run before the
// listener accepts new work: claimed attempts are released, dispatched
// attempts are conservatively committed as unknown synthetic attempts, and
// requests whose claims are all terminal receive an independent caller result.
func (s *Service) RecoverNonterminal(ctx context.Context, limit int) (RecoveryReport, error) {
	if s == nil || s.db == nil || ctx == nil || limit < 1 || limit > MaxRecoveryBatch {
		return RecoveryReport{}, ErrInvalidInput
	}
	var report RecoveryReport
	for report.ReleasedClaims+report.CommittedClaims < limit {
		state, err := s.recoverOneClaim(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return report, err
		}
		if state == StateReleased {
			report.ReleasedClaims++
		} else {
			report.CommittedClaims++
		}
	}
	for report.ReleasedClaims+report.CommittedClaims+report.CompletedRequests < limit {
		completed, err := s.recoverOneRequest(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return report, err
		}
		if completed {
			report.CompletedRequests++
		}
	}
	maintenance, err := s.MaintainOrphanSecrets(ctx, limit)
	if err != nil {
		return report, err
	}
	report.MarkedOrphans = maintenance.Marked
	report.DeletedOrphans = maintenance.Deleted
	more, err := s.hasRecoveryWork(ctx)
	if err != nil {
		return report, err
	}
	report.More = more
	return report, nil
}

func (s *Service) recoverOneClaim(ctx context.Context) (ClaimState, error) {
	at, err := s.nowUnix()
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("claim: begin claim recovery: %w", err)
	}
	defer tx.Rollback()
	var claimID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM dispatch_claims
WHERE state IN ('claimed','dispatched') ORDER BY claim_now,id LIMIT 1`).Scan(&claimID); err != nil {
		return "", err
	}
	record, err := loadClaimTx(ctx, tx, claimID)
	if err != nil {
		return "", err
	}
	switch record.state {
	case StateClaimed:
		if _, err := s.releaseClaimTx(ctx, tx, record, at); err != nil {
			return "", err
		}
	case StateDispatched:
		if _, err := s.completeAttemptTx(ctx, tx, record, candidateSnapshot{
			endpointID:    record.currentEndpoint,
			endpointKeyID: record.endpointKeyID,
			connectorType: record.connectorType,
			baseURL:       record.baseURL,
			upstreamModel: record.upstreamModel,
		}, AttemptOutcome{
			Kind:            ResultSynthetic,
			UpstreamStatus:  502,
			Diagnostic:      "dispatch outcome unavailable after restart",
			ProtocolSuccess: false,
			ResponseStarted: true,
		}, at); err != nil {
			return "", err
		}
	default:
		return "", ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("claim: commit claim recovery: %w", err)
	}
	if record.state == StateClaimed {
		return StateReleased, nil
	}
	return StateCommitted, nil
}

func (s *Service) recoverOneRequest(ctx context.Context) (bool, error) {
	at, err := s.nowUnix()
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("claim: begin request recovery: %w", err)
	}
	defer tx.Rollback()
	var requestID string
	if err := tx.QueryRowContext(ctx, `SELECT r.id FROM logical_requests r
WHERE r.state<>'terminal'
AND NOT EXISTS(SELECT 1 FROM dispatch_claims c
               WHERE c.logical_request_id=r.id AND c.state IN ('claimed','dispatched'))
ORDER BY r.created_at,r.id LIMIT 1`).Scan(&requestID); err != nil {
		return false, err
	}
	request, err := loadRequestTx(ctx, tx, requestID)
	if err != nil {
		return false, err
	}
	var dispatched, debugLive int
	if err := tx.QueryRowContext(ctx, `SELECT
EXISTS(SELECT 1 FROM dispatch_claims WHERE logical_request_id=? AND dispatched_at IS NOT NULL),
EXISTS(SELECT 1 FROM dispatch_claims WHERE logical_request_id=? AND purpose='debug_live')`,
		requestID, requestID).Scan(&dispatched, &debugLive); err != nil {
		return false, fmt.Errorf("claim: classify request recovery: %w", err)
	}
	input := CompleteRequestInput{RequestID: requestID}
	if request.Route == RouteDiscovery {
		input.Caller = CallerResult{Class: ResultSuccess, Status: 202}
		input.Disposition = AccountingNone
	} else {
		input.Caller = CallerResult{
			Class:     ResultFailed,
			Status:    503,
			ErrorCode: httperr.CodeServiceUnavailable,
		}
		input.Disposition = AccountingRelease
		if dispatched != 0 {
			input.Caller.Status = 502
			input.Caller.ErrorCode = httperr.CodeUpstream
			if debugLive != 0 {
				input.Caller.Status = 422
				input.Caller.ErrorCode = httperr.CodeDebugLiveCancelled
			}
			input.Disposition = AccountingCommit
			input.ActualChargeMilli = request.ReservedMilli
		}
	}
	if _, err := s.completeRequestTx(ctx, tx, request, input, at); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("claim: commit request recovery: %w", err)
	}
	return true, nil
}

// MaintainOrphanSecrets marks unreachable credential rows and removes rows at
// the exact one-hour boundary. The batch covers at most limit rows in one
// transaction; due deletion is prioritized over new marking.
func (s *Service) MaintainOrphanSecrets(ctx context.Context, limit int) (MaintenanceReport, error) {
	if s == nil || s.db == nil || ctx == nil || limit < 1 || limit > MaxRecoveryBatch {
		return MaintenanceReport{}, ErrInvalidInput
	}
	at, err := s.nowUnix()
	if err != nil {
		return MaintenanceReport{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MaintenanceReport{}, fmt.Errorf("claim: begin credential maintenance: %w", err)
	}
	defer tx.Rollback()
	threshold := int64(-1)
	if at >= int64(OrphanSecretTTL.Seconds()) {
		threshold = at - int64(OrphanSecretTTL.Seconds())
	}
	deletedResult, err := tx.ExecContext(ctx, `DELETE FROM endpoint_key_secrets
WHERE id IN (
 SELECT id FROM endpoint_key_secrets
 WHERE orphaned_at IS NOT NULL AND orphaned_at<=?
   AND NOT EXISTS(SELECT 1 FROM endpoint_keys k WHERE k.secret_ref_id=endpoint_key_secrets.id)
   AND NOT EXISTS(SELECT 1 FROM dispatch_claims c WHERE c.secret_ref_id=endpoint_key_secrets.id
                  AND c.state IN ('claimed','dispatched'))
 ORDER BY orphaned_at,id LIMIT ?
)`, threshold, limit)
	if err != nil {
		return MaintenanceReport{}, fmt.Errorf("claim: delete expired orphan credentials: %w", err)
	}
	deleted, err := deletedResult.RowsAffected()
	if err != nil {
		return MaintenanceReport{}, fmt.Errorf("claim: count deleted credentials: %w", err)
	}
	remaining := limit - int(deleted)
	marked := int64(0)
	if remaining > 0 {
		markedResult, err := tx.ExecContext(ctx, `UPDATE endpoint_key_secrets SET orphaned_at=?
WHERE id IN (
 SELECT id FROM endpoint_key_secrets
 WHERE orphaned_at IS NULL
   AND NOT EXISTS(SELECT 1 FROM endpoint_keys k WHERE k.secret_ref_id=endpoint_key_secrets.id)
   AND NOT EXISTS(SELECT 1 FROM dispatch_claims c WHERE c.secret_ref_id=endpoint_key_secrets.id
                  AND c.state IN ('claimed','dispatched'))
 ORDER BY id LIMIT ?
) AND orphaned_at IS NULL`, at, remaining)
		if err != nil {
			return MaintenanceReport{}, fmt.Errorf("claim: mark orphan credentials: %w", err)
		}
		marked, err = markedResult.RowsAffected()
		if err != nil {
			return MaintenanceReport{}, fmt.Errorf("claim: count orphan credentials: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return MaintenanceReport{}, fmt.Errorf("claim: commit credential maintenance: %w", err)
	}
	return MaintenanceReport{Marked: int(marked), Deleted: int(deleted)}, nil
}

func (s *Service) hasRecoveryWork(ctx context.Context) (bool, error) {
	at, err := s.nowUnix()
	if err != nil {
		return false, err
	}
	threshold := int64(-1)
	if at >= int64(OrphanSecretTTL.Seconds()) {
		threshold = at - int64(OrphanSecretTTL.Seconds())
	}
	var pending int
	err = s.db.QueryRowContext(ctx, `SELECT
EXISTS(SELECT 1 FROM dispatch_claims WHERE state IN ('claimed','dispatched'))
OR EXISTS(SELECT 1 FROM logical_requests WHERE state<>'terminal')
OR EXISTS(SELECT 1 FROM endpoint_key_secrets s
          WHERE (s.orphaned_at IS NULL OR s.orphaned_at<=?)
            AND NOT EXISTS(SELECT 1 FROM endpoint_keys k WHERE k.secret_ref_id=s.id)
            AND NOT EXISTS(SELECT 1 FROM dispatch_claims c WHERE c.secret_ref_id=s.id
                           AND c.state IN ('claimed','dispatched')))`, threshold).Scan(&pending)
	if err != nil {
		return false, fmt.Errorf("claim: inspect recovery backlog: %w", err)
	}
	return pending != 0, nil
}
