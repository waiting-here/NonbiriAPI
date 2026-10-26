// Package donationstate shares the management projection of a donated key.
package donationstate

import "github.com/waiting-here/NonbiriAPI/internal/smartbackoff"

func Valid(value string) bool {
	switch value {
	case "", "all", "available", "disabled", "suspended", "exhausted", "rate_limited", "pending", "expired", "ended":
		return true
	}
	return false
}

func Filtered(value string) bool { return value != "" && value != "all" }

const PhysicalJoins = ` LEFT JOIN endpoint_keys k ON k.id=dk.endpoint_key_id LEFT JOIN endpoints e ON e.id=k.endpoint_id `

// SQL uses repository-owned dk/d/k/e aliases. Its four time placeholders
// receive the same request clock, preserving prepared statement reuse.
func SQL() string {
	return SQLAt("?")
}

// SQLAt accepts a repository-owned clock expression, never request input.
func SQLAt(now string) string {
	status := `(CASE WHEN d.status IN ('pending','approved') AND NOT EXISTS(SELECT 1 FROM donation_keys live WHERE live.donation_id=d.id AND live.ended_at IS NULL AND (live.expires_at IS NULL OR live.expires_at>` + now + `)) THEN 'expired' ELSE d.status END)`
	healthy := `SELECT 1 FROM charity_use_availability av LEFT JOIN donation_service_health use_health ON use_health.use_id=av.use_id AND use_health.endpoint_key_id=dk.endpoint_key_id AND use_health.credential_version_id=av.credential_version_id AND use_health.native_service_bit=av.native_service_bit WHERE av.donation_key_id=dk.id AND COALESCE(use_health.failure_disabled,0)=0`
	return `CASE
 WHEN dk.ended_reason IS NOT NULL OR NOT EXISTS(SELECT 1 FROM donation_key_memberships m WHERE m.donation_key_id=dk.id)
 THEN CASE WHEN dk.ended_reason='expired' OR ` + status + `='expired' THEN 'expired' ELSE 'ended' END
 WHEN dk.expires_at<=` + now + ` OR ` + status + `='expired' THEN 'expired'
 WHEN d.terminal_at IS NOT NULL OR d.status NOT IN ('pending','approved') THEN 'ended'
 WHEN d.status='pending' AND NOT EXISTS(SELECT 1 FROM donation_uses pending_use WHERE pending_use.donation_key_id=dk.id AND pending_use.current_approved_revision_id IS NOT NULL) THEN 'pending'
 WHEN COALESCE(e.enabled,0)=0 OR COALESCE(k.enabled,0)=0 OR dk.enabled=0 OR dk.failure_disabled=1 THEN 'disabled'
 WHEN EXISTS(SELECT 1 FROM endpoint_key_suspensions x WHERE x.endpoint_key_id=k.id) THEN 'suspended'
 WHEN NOT EXISTS(` + healthy + `) THEN 'disabled'
 WHEN nbi_u128_remaining(dk.price_limit_mag,dk.price_used_mag,dk.price_reserved_mag,nbi_u128(0))=nbi_u128(0)
 OR nbi_u128_remaining(dk.call_limit_mag,dk.calls_used,dk.calls_reserved,nbi_u128(0))=nbi_u128(0)
 OR nbi_u128_remaining(dk.token_limit_mag,dk.tokens_used,dk.tokens_reserved,nbi_u128(0))=nbi_u128(0)
 OR nbi_u128_remaining(dk.input_token_limit_mag,dk.input_tokens_used,dk.input_tokens_reserved,nbi_u128(0))=nbi_u128(0)
 OR nbi_u128_remaining(dk.output_token_limit_mag,dk.output_tokens_used,dk.output_tokens_reserved,nbi_u128(0))=nbi_u128(0) THEN 'exhausted'
 WHEN (SELECT ` + smartbackoff.EffectiveUntilSQL("dk.id") + `>health_clock.decision_now OR NOT EXISTS(` + healthy + ` AND ` + smartbackoff.ServiceUntilSQL("use_health") + `<=health_clock.decision_now) FROM (SELECT ` + now + ` AS decision_now) health_clock) THEN 'rate_limited'
 ELSE 'available' END`
}
