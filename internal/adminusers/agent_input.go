package adminusers

import (
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
	"math"
	"strconv"
	"strings"
)

// AgentOperation is decoded with the same field validators as the user APIs.
type AgentOperation struct {
	Kind           string
	UserID         int64
	Revision       db.U128
	MFAVersion     string
	profile        ProfileMutation
	economy        EconomyMutation
	ban            BanMutation
	generation     int64
	policy         authz.StewardPermissions
	policyRevision int64
}

func (p AgentOperation) NeedsIdentity() bool {
	return p.Kind == "users.ban" || p.Kind == "users.unban" || p.Kind == "users.profile" && p.profile.LevelSet
}
func (p AgentOperation) RequiresElevation() bool {
	return p.Kind == "users.delete" || p.Kind == "users.mfa_reset"
}
func ParseAgentOperation(op agentplan.Operation) (AgentOperation, error) {
	out := AgentOperation{Kind: op.Kind}
	id, err := strconv.ParseInt(op.TargetID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != op.TargetID || len(op.Input) > idempotency.MaxControlBodyBytes || strictjson.ValidateObject(op.Input) != nil {
		return out, ErrInvalidRequest
	}
	out.UserID = id
	var obj map[string]json.RawMessage
	if json.Unmarshal(op.Input, &obj) != nil {
		return out, ErrInvalidRequest
	}
	valid := false
	switch op.Kind {
	case "users.profile", "users.economy":
		if _, exists := obj["mode"]; exists {
			return out, ErrInvalidRequest
		}
		obj["mode"], _ = json.Marshal(strings.TrimPrefix(op.Kind, "users."))
		if op.Kind == "users.profile" {
			out.profile, valid = decodeProfileMutation(obj)
			out.Revision = out.profile.ExpectedRevision
		} else {
			out.economy, valid = decodeEconomyMutation(obj)
			out.Revision = out.economy.ExpectedRevision
		}
	case "users.ban":
		if exactObject(obj, "expected_revision", "reason", "duration_seconds") {
			out.Revision, valid = requiredRevision(obj, "expected_revision")
			reason, ok := requiredString(obj, "reason")
			duration, ok2 := nullableInteger(obj["duration_seconds"])
			valid = valid && ok && validReason(reason) && ok2 && (duration == nil || *duration > 0 && *duration <= db.MaxBanDurationSeconds)
			out.ban = BanMutation{ExpectedRevision: out.Revision, Reason: reason, DurationSeconds: duration}
		}
	case "users.unban":
		if exactObject(obj, "expected_revision") {
			out.Revision, valid = requiredRevision(obj, "expected_revision")
		}
	case "users.delete":
		if exactObject(obj, "expected_revision") {
			raw, ok := requiredString(obj, "expected_revision")
			out.Revision, err = db.ParseU128Decimal(raw)
			valid = ok && err == nil
		}
	case "users.mfa_reset":
		if exactObject(obj, "expected_revision", "expected_mfa_version") {
			raw, ok := requiredString(obj, "expected_revision")
			out.Revision, err = db.ParseU128Decimal(raw)
			valid = ok && err == nil

			out.MFAVersion, ok = requiredString(obj, "expected_mfa_version")
			valid = valid && ok && len(out.MFAVersion) == 64
		}
	case "users.caller_key_revoke":
		if exactObject(obj, "expected_generation") {
			raw, ok := requiredString(obj, "expected_generation")
			out.generation, err = strconv.ParseInt(raw, 10, 64)
			valid = ok && err == nil && out.generation >= 0 && strconv.FormatInt(out.generation, 10) == raw
		}
	case "users.permissions":
		if exactObject(obj, "expected_revision", "permissions", "limits", "donation_scope") {
			raw, ok := requiredString(obj, "expected_revision")
			out.policyRevision, err = strconv.ParseInt(raw, 10, 64)
			scope, ok2 := requiredString(obj, "donation_scope")
			var flags map[string]*bool
			valid = ok && ok2 && err == nil && out.policyRevision >= 0 && out.policyRevision < math.MaxInt64 && strconv.FormatInt(out.policyRevision, 10) == raw && json.Unmarshal(obj["permissions"], &flags) == nil && json.Unmarshal(obj["limits"], &out.policy.Limits) == nil
			out.policy.Revision = raw
			out.policy.DonationScope = authz.DonationScope(scope)
			out.policy.Permissions = make(map[string]bool, len(flags))
			for key, value := range flags {
				if value == nil {
					valid = false
					break
				}
				out.policy.Permissions[key] = *value
			}
			valid = valid && authz.ValidateStewardPermissions(out.policy) == nil
		}
	}
	if !valid {
		return out, ErrInvalidRequest
	}
	return out, nil
}
