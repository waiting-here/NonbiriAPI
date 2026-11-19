package logapi

import (
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

// AttemptHealthImpact describes the outcome at dispatch time. In particular,
// later manual releases must not change a recorded retry deadline or count.
type AttemptHealthImpact struct {
	StreakEffect string          `json:"streak_effect"`
	StreakBefore string          `json:"streak_before,omitempty"`
	StreakAfter  string          `json:"streak_after,omitempty"`
	Backoff      *AttemptBackoff `json:"backoff,omitempty"`
}

type AttemptBackoff struct {
	Reason       string `json:"reason"`
	Scope        string `json:"scope"`
	RetryAt      int64  `json:"retry_at"`
	Period       string `json:"period,omitempty"`
	ObservedOnly bool   `json:"observed_only,omitempty"`
}

// Only pending rows consult the ordered fold cursor. A generation reset or
// deletion can retire a waiting claim before it ever changes the counter.
const attemptStreakStillPending = `EXISTS (
 SELECT 1 FROM donation_usage_reservations r
 LEFT JOIN dispatch_service_health m ON m.claim_id=r.claim_id
 LEFT JOIN donation_service_health h ON h.id=m.health_id
 LEFT JOIN donation_uses u ON u.id=h.use_id
 LEFT JOIN donation_keys sk ON sk.id=u.donation_key_id
 LEFT JOIN donation_keys k ON k.id=r.donation_key_id
 WHERE r.claim_id=a.claim_id AND r.state IN ('committed','released') AND (
  (m.claim_id IS NOT NULL AND sk.id IS NOT NULL AND sk.ended_at IS NULL
    AND h.streak_generation=m.streak_generation AND m.claim_seq>=h.next_fold_seq)
  OR (m.claim_id IS NULL AND k.streak_generation=r.streak_generation AND r.claim_seq>=k.next_fold_seq)
 ))`

const managementHealthColumns = `CASE WHEN l.route_kind IN ` + charityRouteKindsSQL + ` THEN
 CASE WHEN a.streak_effect='pending' THEN CASE WHEN ` + attemptStreakStillPending + ` THEN 'pending' ELSE 'unchanged' END
 ELSE COALESCE(a.streak_effect,'unknown') END END,
 a.streak_before,a.streak_after,sf.native_service_bit,b.reason,b.scope,b.retry_at,b.period,b.observed_only`

const managementAttemptsFrom = ` FROM request_attempts a` + managementAttemptTargetJoin + `
 LEFT JOIN request_attempt_service_facts sf ON sf.claim_id=a.claim_id
 LEFT JOIN request_attempt_backoffs b ON b.claim_id=a.claim_id AND b.request_log_id=a.request_log_id`
const managementAttemptsWhere = ` WHERE a.request_log_id=? AND a.attempt_seq>?`
const managementAttemptsSelect = `SELECT ` + attemptColumns + `,` + managementAttemptTargetColumns + `,` + managementHealthColumns + managementAttemptsFrom + managementAttemptsWhere
const managementAttemptsCount = `SELECT COUNT(*) FROM request_attempts a` + managementAttemptsWhere

type managementAttemptFacts struct {
	donationID, donationKeyID     sql.NullInt64
	effect, before, after         sql.NullString
	service, retryAt, observation sql.NullInt64
	reason, scope, period         sql.NullString
}

func scanManagementAttempt(row rowScanner) (attemptRecord, managementAttemptFacts, error) {
	var f managementAttemptFacts
	r, err := scanAttempt(row, &f.donationID, &f.donationKeyID, &f.effect, &f.before, &f.after,
		&f.service, &f.reason, &f.scope, &f.retryAt, &f.period, &f.observation)
	return r, f, err
}

func (f managementAttemptFacts) health(disabled *diagnostic.FailureDisablement) (*AttemptHealthImpact, string) {
	if !f.effect.Valid {
		return nil, ""
	}
	h := &AttemptHealthImpact{StreakEffect: f.effect.String, StreakBefore: f.before.String, StreakAfter: f.after.String}
	var service string
	if f.service.Valid {
		if set, err := contract.NativeServicesFromMask(f.service.Int64); err == nil {
			if services := set.Services(); len(services) == 1 {
				service = string(services[0])
			}
		}
	}
	if disabled != nil {
		// The threshold-crossing receipt independently proves the exact counts.
		h.StreakEffect, h.StreakBefore, h.StreakAfter = "incremented", disabled.StreakBefore, disabled.StreakAfter
		if service == "" {
			service = disabled.NativeService
		}
	}
	if f.reason.Valid {
		h.Backoff = &AttemptBackoff{Reason: f.reason.String, Scope: f.scope.String, RetryAt: f.retryAt.Int64,
			Period: f.period.String, ObservedOnly: f.observation.Int64 == 1}
	}
	return h, service
}
