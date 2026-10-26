package donationstate

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

// ServiceStatus describes the current approved service's authorization and
// health. Shared key lifecycle, admission limits and quotas remain separate.
type ServiceStatus struct {
	smartbackoff.Projection
	UseID           string                 `json:"use_id"`
	NativeService   contract.NativeService `json:"native_service"`
	State           string                 `json:"state"`
	FailureStreak   string                 `json:"failure_streak"`
	FailureDisabled bool                   `json:"failure_disabled"`
	RetryAfterUntil *int64                 `json:"retry_after_until"`
	ConfiguredType  contract.Type          `json:"-"`
	RerankFormat    contract.RerankFormat  `json:"-"`
}

// LoadServiceStatuses reads approved versions even when their service or grant
// is paused. Missing health rows represent a service with no failure history.
// The caller authorizes keyIDs in the same transaction before exposing them.
func LoadServiceStatuses(ctx context.Context, tx *sql.Tx, keyIDs []int64, now int64) (map[int64][]ServiceStatus, error) {
	if ctx == nil || tx == nil {
		return nil, fmt.Errorf("donationstate: missing service status reader")
	}
	result := make(map[int64][]ServiceStatus, len(keyIDs))
	ids := make([]int64, 0, len(keyIDs))
	for _, id := range keyIDs {
		if id <= 0 {
			return nil, fmt.Errorf("donationstate: invalid donation key ID")
		}
		if _, exists := result[id]; !exists {
			result[id] = []ServiceStatus{}
			ids = append(ids, id)
		}
	}
	const batchSize = 512
	for start := 0; start < len(ids); start += batchSize {
		batch := ids[start:min(start+batchSize, len(ids))]
		if err := loadServiceStatusBatch(ctx, tx, batch, now, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func loadServiceStatusBatch(ctx context.Context, tx *sql.Tx, ids []int64, now int64, result map[int64][]ServiceStatus) error {
	args := make([]any, len(ids), 2*len(ids))
	for i, id := range ids {
		args[i] = id
	}
	args = append(args, args...)
	rows, err := tx.QueryContext(ctx, serviceStatusQuery(strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")), args...)
	if err != nil {
		return fmt.Errorf("donationstate: read service statuses: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status ServiceStatus
		var keyID, useID, serviceBit int64
		var enabled, authorized, reviewRequired, probing bool
		var streak []byte
		var retry, backoff sql.NullInt64
		var period, source string
		if err := rows.Scan(&keyID, &useID, &serviceBit, &enabled, &authorized, &reviewRequired,
			&streak, &status.FailureDisabled, &retry, &period, &source, &backoff, &probing, &status.ConfiguredType, &status.RerankFormat); err != nil {
			return fmt.Errorf("donationstate: scan service status: %w", err)
		}
		services, err := contract.NativeServicesFromMask(serviceBit)
		if err != nil || len(services.Services()) != 1 {
			return fmt.Errorf("donationstate: invalid service status identity")
		}
		status.UseID = strconv.FormatInt(useID, 10)
		status.NativeService = services.Services()[0]
		status.FailureStreak = "0"
		if streak != nil {
			count, err := db.DecodeU128(streak)
			if err != nil {
				return fmt.Errorf("donationstate: decode service failure streak: %w", err)
			}
			status.FailureStreak = count.Decimal()
		}
		if retry.Valid && retry.Int64 > now {
			status.RetryAfterUntil = &retry.Int64
		}
		var backoffAt *int64
		if backoff.Valid {
			backoffAt = &backoff.Int64
		}
		status.Projection = smartbackoff.DecodeProjection(period, source, backoffAt, probing, now)
		switch {
		case !enabled:
			status.State = "disabled"
		case reviewRequired:
			status.State = "review_required"
		case !authorized:
			status.State = "unavailable"
		case status.FailureDisabled:
			status.State = "failure_disabled"
		case status.RetryAfterUntil != nil || status.Backoff != nil && status.Backoff.State != "ready":
			status.State = "rate_limited"
		default:
			status.State = "available"
		}
		result[keyID] = append(result[keyID], status)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("donationstate: iterate service statuses: %w", err)
	}
	return nil
}

func serviceStatusQuery(placeholders string) string {
	return `SELECT dk.id,u.id,u.native_service_bit,u.enabled,
COALESCE(a.available=1 AND a.relationship_valid=1 AND a.owner_user_id=d.user_id AND a.endpoint_key_id=dk.endpoint_key_id
 AND (a.effective_native_service_mask&u.native_service_bit)=u.native_service_bit,0),
EXISTS(SELECT 1 FROM donation_key_review_requirements rr WHERE rr.hmac=ur.key_body_review_hmac AND rr.required=1),
h.failure_streak,COALESCE(h.failure_disabled,0),h.retry_after_until,
COALESCE(h.backoff_period,''),COALESCE(h.backoff_source,''),h.backoff_retry_at,h.trial_claim_id IS NOT NULL,
COALESCE(a.configured_type,''),COALESCE(a.rerank_format,'')
FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id
JOIN donation_uses u ON u.donation_key_id=dk.id
JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id AND ur.use_id=u.id AND ur.review_state='approved'
LEFT JOIN (SELECT * FROM endpoint_grant_authorizations WHERE grant_revision_id IN (
 SELECT selected_revision.grant_revision_id FROM donation_uses selected_use
 JOIN donation_use_revisions selected_revision ON selected_revision.id=selected_use.current_approved_revision_id AND selected_revision.use_id=selected_use.id
 WHERE selected_use.donation_key_id IN (` + placeholders + `)
)) a ON a.grant_revision_id=ur.grant_revision_id
LEFT JOIN donation_service_health h ON h.use_id=u.id AND h.endpoint_key_id=dk.endpoint_key_id
 AND h.native_service_bit=u.native_service_bit AND h.credential_version_id=a.credential_version_id
WHERE dk.id IN (` + placeholders + `) ORDER BY dk.id,u.native_service_bit`
}
