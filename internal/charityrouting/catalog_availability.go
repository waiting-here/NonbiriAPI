package charityrouting

import (
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

// Catalog availability describes the model's current resources independently
// of the reader's level. The three filters can therefore be combined without
// making the resource filter implicitly repeat the personal-access filter.
// Count only through the runtime candidate ceiling: an oversized candidate set
// cannot be dispatched, even when it contains individually eligible keys.
// Only repository-owned SQL expressions are interpolated here.
func catalogAvailableSQL() string {
	return catalogAvailableBindingSQL("")
}

// bindingID is an internal SQL expression, never request input. An empty
// expression retains the public catalog's bounded model availability check.
func catalogAvailableBindingSQL(bindingID string, memberID ...string) string {
	return `(` + availableCandidateCountSQL(bindingID, false, memberID...) + ` BETWEEN 1 AND ` + strconv.Itoa(MaxRuntimeCandidates) + `)`
}

// Caller-scoped lists and the global catalog share every resource and quota
// predicate. Only caller lists exclude the reader's own donations (except
// level six, whose non-administrator identity is checked by CurrentLevel).
func availableCandidateCountSQL(bindingID string, callerScoped bool, memberID ...string) string {
	scope := ""
	if bindingID != "" {
		scope = " AND b.id=" + bindingID
	}
	if len(memberID) > 0 && memberID[0] != "" {
		scope += " AND dk.id=" + memberID[0]
	}
	if callerScoped {
		scope += " AND (d.user_id<>cx.caller_id OR cx.caller_level=6)"
	}
	price := `(CASE cm.pricing_mode WHEN 'per_request' THEN cm.request_user_price ELSE COALESCE((SELECT amount_milli FROM charity_model_token_reserves WHERE model_id=cm.id),CAST(cx.token_reserve AS INTEGER)) END)`
	// The joined key has no extra quota predicate when neither independent
	// token dimension nor current rules restrict it. CASE keeps the original
	// calculation lazy for that common case.
	quota := `(CASE WHEN dk.input_token_limit_mag IS NULL AND dk.output_token_limit_mag IS NULL
 AND NOT EXISTS(SELECT 1 FROM donation_quota_rules qr WHERE qr.donation_key_id=dk.id AND qr.current_epoch IS NOT NULL)
 THEN 1 ELSE ` + donationquota.AvailabilityPredicate("dk.id", "cx.decision_now", price, "dk.token_reserve") + ` END)`
	// Start with this model's bindings. Reordering these joins around the
	// authorization views otherwise revisits every live donation key per model.
	return `(CASE WHEN cx.charity_enabled=1 AND cm.enabled=1 AND (cm.pricing_mode='per_request' OR ` + price + `>0) THEN (SELECT COUNT(*) FROM (
 SELECT DISTINCT b.id FROM charity_model_bindings b
 CROSS JOIN charity_binding_targets target ON target.binding_id=b.id
 CROSS JOIN donation_keys dk ON dk.id=target.donation_key_id
 JOIN donations d ON d.id=dk.donation_id
 JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id
 JOIN endpoint_keys k ON k.id=m.endpoint_key_id
 JOIN endpoints e ON e.id=k.endpoint_id

 WHERE b.charity_model_id=cm.id AND d.status IN ('pending','approved') AND d.terminal_at IS NULL AND d.user_id IS NOT NULL
 AND ` + donorAvailableSQL("cx.decision_now") + `
 AND dk.ended_at IS NULL AND dk.enabled=1 AND dk.failure_disabled=0
 AND ` + smartbackoff.EffectiveUntilSQL("dk.id") + `<=cx.decision_now
 AND k.enabled=1 AND e.enabled=1 AND ` + charityscope.AvailableUseSQL("dk.id", "target.native_service_mask", "cm.model_types", "cx.decision_now") + `
 AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions x WHERE x.endpoint_key_id=k.id)
 AND (dk.expires_at IS NULL OR dk.expires_at>cx.decision_now)
 AND nbi_u128_remaining(dk.price_limit_mag,dk.price_used_mag,dk.price_reserved_mag,nbi_u128(0))>nbi_u128(0)
 AND nbi_u128_remaining(dk.price_limit_mag,dk.price_used_mag,dk.price_reserved_mag,nbi_u128(0))>=nbi_u128(` + price + `)
 AND nbi_u128_remaining(dk.call_limit_mag,dk.calls_used,dk.calls_reserved,nbi_u128(0))>=nbi_u128(1)
 AND nbi_u128_remaining(dk.token_limit_mag,dk.tokens_used,dk.tokens_reserved,nbi_u128(0))>nbi_u128(0)
 AND nbi_u128_remaining(dk.token_limit_mag,dk.tokens_used,dk.tokens_reserved,nbi_u128(0))>=nbi_u128(COALESCE(dk.input_token_reserve+dk.output_token_reserve,dk.token_reserve))
 AND ` + quota + `
 ` + scope + `
	 LIMIT ` + strconv.Itoa(MaxRuntimeCandidates+1) + `)) ELSE 0 END)`
}
