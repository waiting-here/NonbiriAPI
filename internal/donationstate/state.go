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
	status := `(CASE WHEN d.status IN ('pending','approved') AND NOT EXISTS(SELECT 1 FROM donation_keys live WHERE live.donation_id=d.id AND live.ended_at IS NULL AND (live.expires_at IS NULL OR live.expires_at>?)) THEN 'expired' ELSE d.status END)`
	return `CASE
 WHEN dk.ended_reason IS NOT NULL OR NOT EXISTS(SELECT 1 FROM donation_key_memberships m WHERE m.donation_key_id=dk.id)
 THEN CASE WHEN dk.ended_reason='expired' OR ` + status + `='expired' THEN 'expired' ELSE 'ended' END
 WHEN dk.expires_at<=? OR ` + status + `='expired' THEN 'expired'
 WHEN d.status='pending' THEN 'pending'
 WHEN COALESCE(e.enabled,0)=0 OR COALESCE(k.enabled,0)=0 OR dk.enabled=0 OR dk.failure_disabled=1 THEN 'disabled'
 WHEN EXISTS(SELECT 1 FROM endpoint_key_suspensions x WHERE x.endpoint_key_id=k.id) THEN 'suspended'
 WHEN nbi_u128_remaining(dk.price_limit_mag,dk.price_used_mag,dk.price_reserved_mag,nbi_u128(0))=nbi_u128(0)
 OR nbi_u128_remaining(dk.call_limit_mag,dk.calls_used,dk.calls_reserved,nbi_u128(0))=nbi_u128(0)
 OR nbi_u128_remaining(dk.token_limit_mag,dk.tokens_used,dk.tokens_reserved,nbi_u128(0))=nbi_u128(0)
 OR nbi_u128_remaining(dk.input_token_limit_mag,dk.input_tokens_used,dk.input_tokens_reserved,nbi_u128(0))=nbi_u128(0)
 OR nbi_u128_remaining(dk.output_token_limit_mag,dk.output_tokens_used,dk.output_tokens_reserved,nbi_u128(0))=nbi_u128(0) THEN 'exhausted'
 WHEN ` + smartbackoff.EffectiveUntilSQL("dk.id") + `>? THEN 'rate_limited'
 ELSE 'available' END`
}
