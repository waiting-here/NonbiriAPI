package adminusers

import (
	"context"
	"database/sql"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

// AgentTarget contains no credential or session material.
type AgentTarget struct {
	UserID    int64
	DiscordID string
}

func agentRole(actor agentplan.Actor) (managementRole, error) {
	if actor.Automatic || actor.ID <= 0 {
		return roleAdmin, ErrForbidden
	}
	switch actor.Role {
	case "admin":
		return roleAdmin, nil
	case "level5", "level6":
		return roleSteward, nil
	}
	return roleAdmin, ErrForbidden
}
func agentPermission(p AgentOperation) string {
	switch p.Kind {
	case "users.profile", "users.economy":
		return "user_fields"
	case "users.ban":
		return "users.ban"
	case "users.unban":
		return "users.unban"
	case "users.permissions":
		return "delegate"
	case "users.caller_key_revoke":
		return "users.caller_keys.revoke"
	}
	return "users.read"
}
func (s *Service) authorizeAgentTx(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, p AgentOperation, authorize agentplan.Authorize, preparing ...bool) (userRow, error) {
	role, err := agentRole(actor)
	if err != nil || tx == nil || authorize == nil {
		return userRow{}, ErrForbidden
	}
	if err = authorize(ctx, tx, actor, p.Kind); err != nil {
		return userRow{}, err
	}
	ctx = authz.WithStewardPermission(ctx, agentPermission(p))
	trainee, err := s.authorizeManagementTx(ctx, tx, actor.ID, role, true)
	if err != nil {
		return userRow{}, err
	}
	row, err := readUserRow(ctx, tx, p.UserID)
	if err != nil {
		return row, err
	}
	if role == roleSteward && !canManageUser(row, actor.ID, trainee) {
		return row, ErrForbidden
	}
	switch p.Kind {
	case "users.profile":
		if p.profile.DiscordGatePolicySet && role != roleAdmin {
			return row, ErrForbidden
		}
		if role == roleSteward {
			err = authorizeProfilePermissions(ctx, tx, actor.ID, p.profile)
		}
	case "users.economy":
		if role == roleSteward {
			if p.economy.Target == "donation_credit" {
				return row, ErrForbidden
			}
			err = authorizeEconomyPermissions(ctx, tx, actor.ID, p.economy)
		}
	case "users.ban":
		if role == roleSteward {
			err = authorizeBanPermissions(ctx, tx, actor.ID, p.ban.DurationSeconds)
		}
	case "users.caller_key_revoke":
		err = authorizeCallerKeyTargetTx(ctx, tx, actor.ID, p.UserID, role, trainee)
	case "users.permissions":
		if role == roleSteward && trainee {
			return row, ErrForbidden
		}
		if actor.ID == p.UserID || (len(preparing) == 0 && (!row.manualLevel.Valid || row.manualLevel.Int64 != 5)) {
			return row, ErrForbidden
		}
	case "users.delete", "users.mfa_reset":
		if role != roleAdmin || actor.ID == p.UserID {
			return row, ErrForbidden
		}
	}
	if err == nil && (p.Kind == "users.profile" || p.Kind == "users.economy" || p.Kind == "users.ban" || p.Kind == "users.unban") {
		err = authorizeExistingBanChange(ctx, tx, actor.ID, row, trainee, p.Kind == "users.profile" && p.profile.limitsOnly(), s.now().Unix())
	}
	return row, err
}

// AgentPreflight verifies live authority without consuming elevation or taking
// a writer lock. Versions are checked later in operation order inside Apply.
func (s *Service) AgentPreflight(ctx context.Context, actor agentplan.Actor, ops []AgentOperation, authorize agentplan.Authorize) ([]AgentTarget, error) {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := make([]AgentTarget, 0, len(ops))
	checkedRevision := make(map[int64]bool)
	for _, p := range ops {
		row, err := s.authorizeAgentTx(ctx, tx, actor, p, authorize, true)
		if err != nil {
			return nil, err
		}
		if p.Kind == "users.profile" || p.Kind == "users.economy" || p.Kind == "users.ban" || p.Kind == "users.unban" || p.Kind == "users.delete" || p.Kind == "users.mfa_reset" {
			if !checkedRevision[p.UserID] && !equalU128Bytes(row.revision, p.Revision) {
				return nil, ErrConflict
			}
			checkedRevision[p.UserID] = true
		}
		out = append(out, AgentTarget{UserID: row.id, DiscordID: row.discordID.String})
	}
	return out, nil
}
func (s *Service) AgentLockIdentity(ctx context.Context, target AgentTarget) (func(), error) {
	if target.UserID <= 0 || target.DiscordID == "" {
		return nil, ErrConflict
	}
	return s.lockIdentity(ctx, target.DiscordID)
}
