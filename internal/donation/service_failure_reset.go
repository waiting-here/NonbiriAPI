package donation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

const NoResetServiceMessage = "No approved services are eligible for failure reset; check approval and credential availability."

var ErrNoResetService = fmt.Errorf("%w: %s", ErrConflict, NoResetServiceMessage)

// ServiceFailureReset describes the failure state only. Other admission limits
// remain authoritative and are never lifted by this operation.
type ServiceFailureReset struct {
	UseID                 string                 `json:"use_id"`
	NativeService         contract.NativeService `json:"native_service"`
	Status                string                 `json:"status"`
	Reason                string                 `json:"reason,omitempty"`
	FailureStreakBefore   string                 `json:"failure_streak_before"`
	FailureDisabledBefore bool                   `json:"failure_disabled_before"`
	RetryAfterUntil       *int64                 `json:"retry_after_until"`
	BackoffRetryAt        *int64                 `json:"backoff_retry_at"`
	ManuallyDisabled      bool                   `json:"manually_disabled"`
}

type ServiceResetCounts struct {
	Reset        int `json:"reset"`
	AlreadyClear int `json:"already_clear"`
	Skipped      int `json:"skipped"`
}

func serviceResetCounts(services []ServiceFailureReset) *ServiceResetCounts {
	counts := &ServiceResetCounts{}
	for _, service := range services {
		switch service.Status {
		case "reset":
			counts.Reset++
		case "already_clear":
			counts.AlreadyClear++
		default:
			counts.Skipped++
		}
	}
	return counts
}

type serviceResetState struct {
	receipt                          ServiceFailureReset
	id, useID, keyID, bit, versionID int64
	generation                       []byte
}

// Select the approved credential version, even when a newer self-use version
// exists. Manual switches do not prevent counter maintenance; revoked approval,
// deleted credentials and unavailable credential material do.
func resetServicesTx(ctx context.Context, tx *sql.Tx, donationID, keyID, useID, now int64) ([]ServiceFailureReset, error) {
	rows, err := tx.QueryContext(ctx, `SELECT u.id,dk.endpoint_key_id,u.native_service_bit,COALESCE(g.credential_version_id,0),
 COALESCE(h.id,0),h.streak_generation,h.failure_streak,COALESCE(h.failure_disabled,0),
 NULLIF(h.retry_after_until,0),NULLIF((`+smartbackoff.ServiceUntilSQL("h")+`),0),
 u.enabled=0 OR dk.enabled=0 OR COALESCE(k.enabled,0)=0 OR COALESCE(e.enabled,0)=0 OR COALESCE(c.enabled,0)=0,
 CASE WHEN d.status NOT IN ('pending','approved') OR d.terminal_at IS NOT NULL OR dk.ended_at IS NOT NULL OR dk.expires_at<=? THEN 'ineligible'
 WHEN r.id IS NULL OR r.review_state<>'approved' THEN 'not_approved'
 WHEN EXISTS(SELECT 1 FROM donation_key_review_requirements rr WHERE rr.hmac=r.key_body_review_hmac AND rr.required=1) THEN 'review_required'
 WHEN COALESCE(a.relationship_valid,0)=0 OR a.owner_user_id<>d.user_id OR a.endpoint_key_id<>dk.endpoint_key_id
 OR (a.effective_native_service_mask&u.native_service_bit)<>u.native_service_bit OR c.deleted_at IS NOT NULL OR COALESCE(v.enabled,0)=0
 OR s.id IS NULL OR s.orphaned_at IS NOT NULL
 OR NOT EXISTS(SELECT 1 FROM donation_key_memberships m WHERE m.donation_id=d.id AND m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id)
 THEN 'credential_unavailable' ELSE '' END
 FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donations d ON d.id=dk.donation_id
 LEFT JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id AND r.use_id=u.id
 LEFT JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id
 LEFT JOIN endpoint_grant_authorizations a ON a.grant_revision_id=g.id
 LEFT JOIN endpoint_keys k ON k.id=dk.endpoint_key_id LEFT JOIN endpoints e ON e.id=k.endpoint_id
 LEFT JOIN credentials c ON c.id=g.credential_id LEFT JOIN credential_versions v ON v.id=g.credential_version_id
 LEFT JOIN endpoint_key_secrets s ON s.id=v.secret_ref_id
 LEFT JOIN donation_service_health h ON h.use_id=u.id AND h.endpoint_key_id=dk.endpoint_key_id AND h.native_service_bit=u.native_service_bit AND h.credential_version_id=g.credential_version_id
 WHERE dk.donation_id=? AND dk.id=? AND (?=0 OR u.id=?) ORDER BY u.id`, now, donationID, keyID, useID, useID)
	if err != nil {
		return nil, err
	}
	states := []serviceResetState{}
	for rows.Next() {
		var state serviceResetState
		var streak []byte
		var retry, backoff sql.NullInt64
		var endpoint sql.NullInt64
		if err := rows.Scan(&state.useID, &endpoint, &state.bit, &state.versionID, &state.id, &state.generation, &streak,
			&state.receipt.FailureDisabledBefore, &retry, &backoff, &state.receipt.ManuallyDisabled, &state.receipt.Reason); err != nil {
			rows.Close()
			return nil, err
		}
		state.keyID = endpoint.Int64
		services, err := contract.NativeServicesFromMask(state.bit)
		if err != nil || len(services.Services()) != 1 {
			rows.Close()
			return nil, ErrInvariant
		}
		state.receipt.UseID = strconv.FormatInt(state.useID, 10)
		state.receipt.NativeService = services.Services()[0]
		state.receipt.FailureStreakBefore = "0"
		if state.id != 0 {
			count, err := db.DecodeU128(streak)
			if err != nil {
				rows.Close()
				return nil, ErrInvariant
			}
			state.receipt.FailureStreakBefore = count.Decimal()
		}
		state.receipt.RetryAfterUntil = unixPointer(retry)
		state.receipt.BackoffRetryAt = unixPointer(backoff)
		states = append(states, state)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]ServiceFailureReset, 0, len(states))
	for _, state := range states {
		state.receipt.Status = "ineligible"
		if state.receipt.Reason == "" {
			if state.id == 0 {
				one := db.EncodeU128(db.U128{15: 1})
				result, err := tx.ExecContext(ctx, `INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,next_claim_seq,next_fold_seq,backoff_generation,backoff_episode,backoff_stage,updated_at) VALUES(?,?,?,?,?,?,?, ?,1,0,0,?)`,
					state.useID, state.keyID, state.bit, state.versionID, one, db.EncodeU128(db.U128{}), one, one, now)
				if err != nil {
					return nil, err
				}
				state.id, err = result.LastInsertId()
				if err != nil {
					return nil, err
				}
				state.generation = one
			}
			values, err := streakResetValues(state.generation)
			if err != nil {
				return nil, err
			}
			result, err := tx.ExecContext(ctx, `UPDATE donation_service_health SET `+streakResetAssignments+`,updated_at=? WHERE id=?`, append(values, now, state.id)...)
			if err != nil {
				return nil, err
			}
			if err := requireOne(result); err != nil {
				return nil, err
			}
			state.receipt.Status = "already_clear"
			if state.receipt.FailureDisabledBefore || state.receipt.FailureStreakBefore != "0" {
				state.receipt.Status = "reset"
			}
		}
		out = append(out, state.receipt)
	}
	return out, nil
}

func serviceResetOutcome(services []ServiceFailureReset) string {
	outcome := "no_service"
	for _, service := range services {
		if service.Status == "reset" {
			return "recovered"
		}
		if service.Status == "already_clear" {
			outcome = "already_clear"
		}
	}
	return outcome
}

func resetUseTx(ctx context.Context, tx *sql.Tx, id, expected, now int64) (ServiceFailureReset, error) {
	var empty ServiceFailureReset
	var approved sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT current_approved_revision_id FROM donation_uses WHERE id=? AND revision=?`, id, expected).Scan(&approved); errors.Is(err, sql.ErrNoRows) {
		return empty, ErrConflict
	} else if err != nil {
		return empty, err
	}
	if !approved.Valid {
		return empty, ErrNoResetService
	}
	var donationID, keyID int64
	if err := tx.QueryRowContext(ctx, `SELECT dk.donation_id,dk.id FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id WHERE u.id=?`, id).Scan(&donationID, &keyID); err != nil {
		return empty, err
	}
	services, err := resetServicesTx(ctx, tx, donationID, keyID, id, now)
	if err != nil {
		return empty, err
	}
	if serviceResetOutcome(services) == "no_service" {
		return empty, ErrNoResetService
	}
	result, err := tx.ExecContext(ctx, `UPDATE donation_uses SET revision=revision+1,updated_at=? WHERE id=? AND revision=? AND revision<9223372036854775807`, now, id, expected)
	if err != nil {
		return empty, err
	}
	if err := requireOne(result); err != nil {
		return empty, err
	}
	return services[0], nil
}
