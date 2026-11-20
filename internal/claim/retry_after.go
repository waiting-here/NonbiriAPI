package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

const retryHeader = "retry_header"

// ObserveResponseHeaders persists a retry hint before reading the response body.
// Only an accepted charity stream also establishes a billing checkpoint.
func (s *Service) ObserveResponseHeaders(ctx context.Context, handle Handle, observation connectorcontract.ResponseObservation) (resultErr error) {
	if s == nil || s.db == nil || ctx == nil || !validHandle(handle) || handle.purpose != PurposeCharity ||
		observation.Status < 100 || observation.Status > 599 || observation.ObservedAt < 0 || observation.ObservedAt > 253402300799 ||
		observation.RateLimitedUntil < 0 || observation.RateLimitedUntil > 253402300799 {
		return ErrInvalidInput
	}
	tx, finish, err := observability.BeginMeasuredTx(ctx, s.db, nil, "claim_response_headers")
	if err != nil {
		return fmt.Errorf("claim: begin response headers: %w", err)
	}
	defer func() { finish(resultErr) }()
	record, err := loadClaimTx(ctx, tx, handle.claimID)
	if err != nil {
		return err
	}
	if err := verifyHandle(record, handle); err != nil {
		return err
	}
	if record.state != StateDispatched {
		return ErrNotDispatched
	}
	if !observation.HasRetryAfter {
		trial, err := smartbackoff.IsTrial(ctx, tx, record.claimID)
		if err != nil {
			return err
		}
		if !trial || observation.Status != 200 {
			return ErrInvalidInput
		}
		if err := smartbackoff.Accepted(ctx, tx, record.claimID, max(observation.ObservedAt, record.dispatchedAt.Int64)); err != nil {
			return err
		}
		if observation.Stream {
			if err := recordResponseStartTx(ctx, tx, record.claimID, max(observation.ObservedAt, record.dispatchedAt.Int64), 200); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	now, err := s.nowUnix()
	if err != nil {
		return err
	}
	changed, err := recordRetryAfterTx(ctx, tx, record, observation.Status, observation.ObservedAt, observation.RateLimitedUntil, now, retryHeader)
	if err != nil {
		return err
	}
	if changed == 1 && observation.Status == 200 && record.backoffActive {
		if err := smartbackoff.Accepted(ctx, tx, record.claimID, max(observation.ObservedAt, record.dispatchedAt.Int64)); err != nil {
			return err
		}
	}
	if changed == 1 && observation.Stream && observation.Status == 200 {
		at := max(observation.ObservedAt, record.dispatchedAt.Int64)
		if err := recordResponseStartTx(ctx, tx, record.claimID, at, 200); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Header hints and verified short-lived provider errors share the existing
// claim-bound cooldown receipt. Header snapshots still contain only real headers.
func recordRetryAfterTx(ctx context.Context, tx *sql.Tx, record claimRecord, status int, at, until, now int64, reason string) (int64, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO dispatch_retry_after_observations(claim_id,http_status,observed_at,rate_limited_until)
VALUES(?,?,?,?) ON CONFLICT(claim_id) DO NOTHING`, record.claimID, status, at, until)
	if err != nil {
		return 0, err
	}
	changed, err := result.RowsAffected()
	if err == nil && changed == 1 && record.donationKeyID.Valid && until > at {
		if googleQuotaResponse(record, status) {
			if reason == retryHeader {
				// A legacy trial already gates its key until completion.
				_, err = tx.ExecContext(ctx, `UPDATE dispatch_retry_after_observations SET pending_health_id=(
SELECT h.id FROM dispatch_smart_backoff_facts f JOIN donation_service_health h ON h.id=f.health_id_snapshot AND h.backoff_generation=f.generation
WHERE f.claim_id=?1) WHERE claim_id=?1`, record.claimID)
			}
		} else {
			err = smartbackoff.ObserveRetryAfter(ctx, tx, record.claimID, reason, until, at, now)
		}
	}
	return changed, err
}

func retryAfterOutcomeTx(ctx context.Context, tx *sql.Tx, record claimRecord, outcome AttemptOutcome, at int64) (AttemptOutcome, error) {
	if record.purpose != PurposeCharity {
		if outcome.StreakDisposition == connectorcontract.StreakNeutral && retryOrigin(outcome.FailureOrigin) {
			return outcome, ErrInvalidInput
		}
		return outcome, nil
	}
	if record.backoffAccepted.Valid {
		outcome.StreakDisposition = connectorcontract.StreakSuccess
		return outcome, nil
	}
	var status int
	var observed, until int64
	var reason string
	var seconds int64
	if record.state == StateDispatched && outcome.Kind == ResultResponse && !outcome.ProtocolSuccess && !outcome.ResponseStarted &&
		outcome.FailureOrigin == connectorcontract.OriginUpstreamResponse && googleQuotaResponse(record, outcome.UpstreamStatus) {
		reason, seconds = outcome.TemporaryBackoff.ModelQuota(outcome.UpstreamStatus, record.upstreamModel)
	}
	err := tx.QueryRowContext(ctx, `SELECT http_status,observed_at,rate_limited_until FROM dispatch_retry_after_observations WHERE claim_id=?`, record.claimID).Scan(&status, &observed, &until)
	if errors.Is(err, sql.ErrNoRows) {
		if seconds > 0 {
			observed = max(at, record.dispatchedAt.Int64)
			until = min(int64(253402300799), observed+seconds)
			if _, err := recordRetryAfterTx(ctx, tx, record, outcome.UpstreamStatus, observed, until, observed, reason); err != nil {
				return outcome, err
			}
			if err := storeModelCooldownTx(ctx, tx, record.claimID, reason, until, observed); err != nil {
				return outcome, err
			}
			outcome.StreakDisposition = connectorcontract.StreakNeutral
			return outcome, nil
		}
		if record.state == StateDispatched && outcome.Kind == ResultResponse && !outcome.ProtocolSuccess && !outcome.ResponseStarted && outcome.FailureOrigin == connectorcontract.OriginUpstreamResponse {
			if delay := outcome.TemporaryBackoff.Delay(outcome.UpstreamStatus); delay > 0 {
				observed := max(at, record.dispatchedAt.Int64)
				if _, err := recordRetryAfterTx(ctx, tx, record, outcome.UpstreamStatus, observed, min(int64(253402300799), observed+delay), observed, "google_model_overloaded"); err != nil {
					return outcome, err
				}
				outcome.StreakDisposition = connectorcontract.StreakNeutral
				return outcome, nil
			}
		}
		if record.backoffActive && outcome.UpstreamStatus != 200 && outcome.FailureOrigin == connectorcontract.OriginUpstreamResponse {
			neutral, err := smartbackoff.RecordClassification(ctx, tx, record.claimID, outcome.Backoff)
			if err != nil {
				return outcome, err
			}
			if neutral {
				outcome.StreakDisposition = connectorcontract.StreakNeutral
				return outcome, nil
			}
		}
		if outcome.Kind == ResultResponse && !outcome.ProtocolSuccess && !outcome.ResponseStarted &&
			outcome.FailureOrigin == connectorcontract.OriginUpstreamResponse &&
			outcome.CallerFault.Matches(outcome.UpstreamStatus, outcome.UpstreamCode) {
			if err := recordCallerFaultTx(ctx, tx, record, outcome); err != nil {
				return outcome, err
			}
			outcome.StreakDisposition = connectorcontract.StreakNeutral
			return outcome, nil
		}
		if outcome.StreakDisposition == connectorcontract.StreakNeutral && retryOrigin(outcome.FailureOrigin) {
			return outcome, ErrInvalidInput
		}
		return outcome, nil
	}
	if err != nil {
		return outcome, err
	}
	if record.purpose != PurposeCharity {
		return outcome, ErrInvariant
	}
	if record.state == StateDispatched && googleQuotaResponse(record, status) {
		if err := finishGoogleHeaderTx(ctx, tx, record, reason, observed, until, at); err != nil {
			return outcome, err
		}
	}
	if status == 200 {
		outcome.StreakDisposition = connectorcontract.StreakSuccess
	} else if !outcome.ProtocolSuccess {
		outcome.StreakDisposition = connectorcontract.StreakNeutral
	}
	return outcome, nil
}

func retryOrigin(origin connectorcontract.FailureOrigin) bool {
	return origin == connectorcontract.OriginUpstreamResponse || origin == connectorcontract.OriginUpstreamProtocol ||
		origin == connectorcontract.OriginNetwork || origin == connectorcontract.OriginTimeout
}
