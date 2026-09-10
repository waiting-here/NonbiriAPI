package charityrouting

import (
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

// Catalog availability describes the model's current resources independently
// of the reader's level. The three filters can therefore be combined without
// making the resource filter implicitly repeat the personal-access filter.
// Count only through the runtime candidate ceiling: an oversized candidate set
// cannot be dispatched, even when it contains individually eligible keys.
// Only repository-owned SQL expressions are interpolated here.
func catalogAvailableSQL() string {
	price := `(CASE cm.pricing_mode WHEN 'per_request' THEN cm.request_user_price ELSE COALESCE((SELECT amount_milli FROM charity_model_token_reserves WHERE model_id=cm.id),cx.token_reserve) END)`
	return `(CASE WHEN cx.charity_enabled=1 AND cm.enabled=1 AND (cm.pricing_mode='per_request' OR ` + price + `>0) THEN (SELECT COUNT(*) FROM (
 SELECT 1 FROM charity_model_bindings b
 JOIN donation_keys dk ON dk.id=b.donation_key_id
 JOIN donations d ON d.id=dk.donation_id
 JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id
 JOIN endpoint_keys k ON k.id=m.endpoint_key_id
 JOIN endpoints e ON e.id=k.endpoint_id
 JOIN model_pair_catalog pc ON pc.endpoint_key_id=b.endpoint_key_id AND pc.normalized_model_id=b.upstream_model_id
 WHERE b.charity_model_id=cm.id AND d.status='approved' AND d.user_id IS NOT NULL
 AND dk.ended_at IS NULL AND dk.enabled=1 AND dk.failure_disabled=0
 AND k.enabled=1 AND e.enabled=1 AND (pc.automatic_supports>0 OR pc.manual_supports>0)
 AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions x WHERE x.endpoint_key_id=k.id)
 AND (dk.expires_at IS NULL OR dk.expires_at>cx.decision_now)
 AND nbi_u128_remaining(dk.price_limit_mag,dk.price_used_mag,dk.price_reserved_mag,nbi_u128(0))>nbi_u128(0)
 AND nbi_u128_remaining(dk.price_limit_mag,dk.price_used_mag,dk.price_reserved_mag,nbi_u128(0))>=nbi_u128(` + price + `)
 AND nbi_u128_remaining(dk.call_limit_mag,dk.calls_used,dk.calls_reserved,nbi_u128(0))>=nbi_u128(1)
 AND nbi_u128_remaining(dk.token_limit_mag,dk.tokens_used,dk.tokens_reserved,nbi_u128(0))>nbi_u128(0)
 AND nbi_u128_remaining(dk.token_limit_mag,dk.tokens_used,dk.tokens_reserved,nbi_u128(0))>=nbi_u128(dk.token_reserve)
 AND ` + donationquota.AvailabilityPredicate("dk.id", "cx.decision_now", price, "dk.token_reserve") + `
	 LIMIT ` + strconv.Itoa(MaxRuntimeCandidates+1) + `)) BETWEEN 1 AND ` + strconv.Itoa(MaxRuntimeCandidates) + ` ELSE 0 END)`
}
