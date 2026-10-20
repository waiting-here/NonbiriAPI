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
	result, err := tx.ExecContext(ctx, `INSERT INTO dispatch_retry_after_observations(claim_id,http_status,observed_at,rate_limited_until)
VALUES(?,?,?,?) ON CONFLICT(claim_id) DO NOTHING`, record.claimID, observation.Status, observation.ObservedAt, observation.RateLimitedUntil)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 1 && record.donationKeyID.Valid && observation.RateLimitedUntil > observation.ObservedAt {
		_, err = tx.ExecContext(ctx, `INSERT INTO donation_key_cooldowns(donation_key_id,rate_limited_until,updated_at)
SELECT id,?,? FROM donation_keys WHERE id=? AND ended_at IS NULL
ON CONFLICT(donation_key_id) DO UPDATE SET
rate_limited_until=max(donation_key_cooldowns.rate_limited_until,excluded.rate_limited_until),
updated_at=max(donation_key_cooldowns.updated_at,excluded.updated_at)`,
			observation.RateLimitedUntil, observation.ObservedAt, record.donationKeyID.Int64)
		if err != nil {
			return err
		}
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

func retryAfterOutcomeTx(ctx context.Context, tx *sql.Tx, record claimRecord, outcome AttemptOutcome) (AttemptOutcome, error) {
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
	err := tx.QueryRowContext(ctx, `SELECT http_status FROM dispatch_retry_after_observations WHERE claim_id=?`, record.claimID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
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
