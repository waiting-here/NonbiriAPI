package agentusers

import (
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"sort"
)

type schema = map[string]any

func object(description string, properties schema, required ...string) schema {
	if required == nil {
		required = []string{}
	}
	return schema{"type": "object", "description": description, "properties": properties, "required": required, "additionalProperties": false}
}
func stringField(description string) schema {
	return schema{"type": "string", "description": description}
}
func decimal(description string) schema {
	return schema{"type": "string", "pattern": "^(0|[1-9][0-9]*)$", "description": description}
}
func nullableLimit(minimum, maximum int64) schema {
	return schema{"anyOf": []any{schema{"type": "null"}, schema{"type": "string", "pattern": "^(0|[1-9][0-9]*)$"}}, "description": "Decimal integer or null to restore the site default.", "x-minimum": minimum, "x-maximum": maximum}
}
func encode(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }

// OperationSchemas returns input schemas keyed by the closed operation kind.
// target_id is always a canonical positive account ID in the plan envelope.
func OperationSchemas() map[string]json.RawMessage {
	revision := decimal("Current account revision; each preceding profile, economy or ban change to this account advances it by one.")
	revision["pattern"] = "^[1-9][0-9]*$"
	profile := object("Change only supplied profile fields; null restores a configured default. Level null restores automatic level.", schema{
		"expected_revision": revision,
		"endpoint_limit":    nullableLimit(0, 10000), "rpm_limit": nullableLimit(1, db.MaxUserRPMLimit),
		"concurrency_limit": nullableLimit(1, db.MaxUserConcurrencyLimit), "light_rpm_limit": nullableLimit(1, db.MaxUserRPMLimit),
		"light_concurrency_limit": nullableLimit(1, db.MaxUserConcurrencyLimit), "global_rpm_limit": nullableLimit(1, db.MaxUserRPMLimit),
		"lang":                schema{"type": "string", "enum": []string{"zh", "en"}},
		"level":               schema{"type": []string{"integer", "null"}, "minimum": 1, "maximum": 6},
		"discord_gate_policy": schema{"type": "string", "enum": []string{db.DiscordGatePolicyInherit, db.DiscordGatePolicyRequire, db.DiscordGatePolicyExempt}},
	}, "expected_revision")
	profile["minProperties"] = 2
	flags, limits := schema{}, schema{}
	policy := authz.DefaultStewardPermissions(6)
	flagNames, limitNames := []string{}, []string{}
	for key := range policy.Permissions {
		flags[key] = schema{"type": "boolean"}
		flagNames = append(flagNames, key)
	}
	for key, value := range policy.Limits {
		limits[key] = decimal("Nonnegative decimal integer, maximum " + value + ".")
		limitNames = append(limitNames, key)
	}
	sort.Strings(flagNames)
	sort.Strings(limitNames)
	return map[string]json.RawMessage{
		"users.profile": encode(profile),
		"users.economy": encode(object("Adjust one account asset. Amount is positive decimal points, at most three fractional digits; current permission bounds apply.", schema{
			"expected_revision": revision, "target": schema{"type": "string", "enum": []string{"balance", "game_balance", "donation_credit"}},
			"direction": schema{"type": "string", "enum": []string{"increase", "decrease"}},
			"amount":    schema{"type": "string", "pattern": "^(0|[1-9][0-9]*)([.][0-9]{1,3})?$"},
			"reason":    stringField("Required reason, 1 to 1024 characters."),
		}, "expected_revision", "target", "direction", "amount", "reason")),
		"users.ban": encode(object("Ban a lower-level account; null duration means permanent. Replacing an active ban can require unban authority.", schema{
			"expected_revision": revision, "reason": stringField("Required reason, 1 to 1024 characters."),
			"duration_seconds": schema{"type": []string{"integer", "null"}, "minimum": 1, "maximum": db.MaxBanDurationSeconds},
		}, "expected_revision", "reason", "duration_seconds")),
		"users.unban":             encode(object("Remove a ban; blacklisted identities must first be handled through the blacklist controls.", schema{"expected_revision": revision}, "expected_revision")),
		"users.caller_key_revoke": encode(object("Revoke the existing caller key without revealing any key material.", schema{"expected_generation": decimal("Generation from caller_key metadata.")}, "expected_generation")),
		"users.permissions": encode(object("Replace the complete level 5 policy. Only an administrator or actual level 6 steward may delegate.", schema{
			"expected_revision": decimal("Current steward permission policy revision; separate from account revision."),
			"permissions":       object("Every current permission is required.", flags, flagNames...),
			"limits":            object("Every current limit is required.", limits, limitNames...),
			"donation_scope":    schema{"type": "string", "enum": []string{"none", "standard", "enhanced", "full"}},
		}, "expected_revision", "permissions", "limits", "donation_scope")),
		"users.mfa_reset": encode(object("Administrator-only MFA reset; requires fresh human authentication at approval, never a password or OTP in this input.", schema{
			"expected_revision": decimal("Current account revision."), "expected_mfa_version": schema{"type": "string", "pattern": "^[0-9a-f]{64}$", "description": "Opaque mfa_version returned by the MFA read."},
		}, "expected_revision", "expected_mfa_version")),
		"users.delete": encode(object("Administrator-only permanent account deletion through the existing retention and legal-hold process; requires fresh human authentication.", schema{"expected_revision": decimal("Current account revision.")}, "expected_revision")),
	}
}
func ReadSchema() json.RawMessage {
	return encode(object("Read safe managed account information. A profile search returns all matches as data plus decimal-string total; exact user_id returns the selected object. Search filters are optional. MFA requires an administrator; key metadata never includes key fragments.", schema{
		"user_id": schema{"type": "string", "pattern": "^[1-9][0-9]*$"},
		"kind":    schema{"type": "string", "enum": []string{"profile", "caller_key", "permissions", "mfa"}},
		"query":   schema{"type": "string", "maxLength": 512},
	}))
}
