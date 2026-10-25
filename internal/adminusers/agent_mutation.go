package adminusers

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"strconv"
)

// AgentApply uses the caller's transaction. The returned finalizer must be
// called after rollback as well as commit, including when this returns an error.
func (s *Service) AgentApply(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, p AgentOperation, lockedIdentity string, authorize agentplan.Authorize) (json.RawMessage, func(bool), error) {
	var finalizers []func(bool)
	finish := func(committed bool) {
		for i := len(finalizers) - 1; i >= 0; i-- {
			finalizers[i](committed)
		}
	}
	row, err := s.authorizeAgentTx(ctx, tx, actor, p, authorize)
	if err != nil {
		return nil, finish, err
	}
	if p.NeedsIdentity() && (!row.discordID.Valid || row.discordID.String != lockedIdentity) {
		return nil, finish, ErrConflict
	}
	now := s.now().Unix()
	if !validNow(now) {
		return nil, finish, ErrUnavailable
	}
	var value any
	invalidate := func(bool) {}
	switch p.Kind {
	case "users.profile":
		value, err = s.applyProfileTx(ctx, tx, row, p.UserID, p.profile, now)
		invalidate = func(committed bool) {
			if !committed {
				return
			}
			if p.profile.LevelSet {
				s.invalidator.InvalidateUserAuthority(p.UserID)
			} else if p.profile.RPMLimitSet || p.profile.ConcurrencySet || p.profile.LightRPMLimitSet || p.profile.LightConcurrencySet || p.profile.GlobalRPMLimitSet {
				if n, ok := s.invalidator.(interface{ UserLimitsChanged(int64) }); ok {
					n.UserLimitsChanged(p.UserID)
				}
			}
		}
	case "users.economy":
		var changed bool
		value, changed, err = s.applyEconomyTx(ctx, tx, row, actor.ID, p.UserID, p.economy, now)
		invalidate = func(committed bool) {
			if committed && changed {
				s.invalidator.InvalidateUserAuthority(p.UserID)
			}
		}
	case "users.ban", "users.unban":
		banned := p.Kind == "users.ban"
		err = s.applyBanTx(ctx, tx, row, actor.ID, p.UserID, p.Revision, banned, p.ban.Reason, p.ban.DurationSeconds, now, &finalizers)
		value = map[string]any{"user_id": strconv.FormatInt(p.UserID, 10), "banned": banned}
		invalidate = func(committed bool) {
			if committed && banned {
				s.invalidator.InvalidateUserAuthority(p.UserID)
			}
		}
	case "users.caller_key_revoke":
		var active bool
		value, active, err = revokeManagedCallerKeyTx(ctx, tx, p.UserID, p.generation, now)
		invalidate = func(committed bool) {
			if committed && active {
				s.invalidator.InvalidateUserAuthority(p.UserID)
			}
		}
	case "users.permissions":
		var previous authz.StewardPermissions
		previous, err = authz.ReadStewardPermissions(ctx, tx, p.UserID)
		if err == nil {
			value, err = replaceStewardPermissionsTx(ctx, tx, actor.ID, p.UserID, p.policyRevision, p.policy, previous, now)
		}
		invalidate = func(committed bool) {
			if committed {
				s.invalidator.InvalidateUserAuthority(p.UserID)
			}
		}
	default:
		err = ErrInvalidRequest
	}
	if err != nil {
		return nil, finish, err
	}
	finalizers = append(finalizers, invalidate)
	raw, err := json.Marshal(value)
	return raw, finish, err
}

func (s *Service) AgentPreview(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, p AgentOperation, authorize agentplan.Authorize) (agentplan.Preview, error) {
	row, err := s.authorizeAgentTx(ctx, tx, actor, p, authorize, true)
	if err != nil {
		return agentplan.Preview{}, err
	}
	cfg, err := readProjectionConfig(ctx, tx)
	if err != nil {
		return agentplan.Preview{}, err
	}
	user, err := projectUser(ctx, tx, row, cfg, s.now().Unix())
	if err != nil {
		return agentplan.Preview{}, err
	}
	var snapshot any = user
	if p.Kind == "users.permissions" && row.manualLevel.Valid && row.manualLevel.Int64 == 5 {
		snapshot, err = authz.ReadStewardPermissions(ctx, tx, p.UserID)
		if err != nil {
			return agentplan.Preview{}, err
		}
	} else if p.Kind == "users.caller_key_revoke" {
		snapshot, _, err = readManagedCallerKey(ctx, tx, p.UserID)
		if err != nil {
			return agentplan.Preview{}, err
		}
	}
	before, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return agentplan.Preview{}, err
	}
	return agentplan.Preview{Title: map[string]string{"users.profile": "Update account profile", "users.economy": "Adjust account credit", "users.ban": "Ban account", "users.unban": "Unban account", "users.permissions": "Replace level 5 permissions", "users.caller_key_revoke": "Revoke caller key", "users.mfa_reset": "Reset two-step authentication", "users.delete": "Delete account"}[p.Kind], Description: "Account " + strconv.FormatInt(p.UserID, 10) + ": " + row.username, Before: string(before), Dangerous: p.Kind == "users.delete" || p.Kind == "users.ban" || p.Kind == "users.mfa_reset" || p.Kind == "users.caller_key_revoke", RequiresElevation: p.RequiresElevation()}, nil
}
