package clientguard

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/riskaudit"
)

type ruleAuthority struct {
	actorID                   int64
	admin, level, targetLevel int
}

// Load only matching bindings in the current transaction. The effects
// transaction independently reads target and editor authority after admission.
func readRuleAuthorities(ctx context.Context, tx *sql.Tx, matches []riskaudit.Match, targetID, now int64) (map[string]ruleAuthority, error) {
	args := make([]any, 0, len(matches)+2)
	args = append(args, targetID, now)
	for _, match := range matches {
		args = append(args, match.RuleID)
	}
	ids := strings.TrimSuffix(strings.Repeat("?,", len(matches)), ",")
	rows, err := tx.QueryContext(ctx, `SELECT b.rule_id,COALESCE(u.id,0),COALESCE(u.is_admin,CASE r.updated_by_role WHEN 'admin' THEN 1 ELSE 0 END),
COALESCE(u.level,u.auto_level,0),COALESCE(t.level,t.auto_level)
FROM client_rule_auto_bans b JOIN risk_client_rules r ON r.id=b.rule_id
LEFT JOIN users u ON u.id=b.actor_user_id JOIN users t ON t.id=? AND t.is_admin=0
WHERE (u.is_banned=0 OR u.banned_until<=? OR u.id IS NULL AND r.updated_by_role='admin')
AND b.rule_id IN (`+ids+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	authorities := make(map[string]ruleAuthority, len(matches))
	for rows.Next() {
		var id string
		var authority ruleAuthority
		if err := rows.Scan(&id, &authority.actorID, &authority.admin, &authority.level, &authority.targetLevel); err != nil {
			return nil, err
		}
		authorities[id] = authority
	}
	return authorities, rows.Err()
}

func (a ruleAuthority) allows(ctx context.Context, tx *sql.Tx, targetID int64, action riskaudit.AutoBan) (bool, error) {
	if a.admin != 0 {
		return true, nil
	}
	if a.actorID == targetID || a.level < 5 || a.targetLevel >= a.level {
		return false, nil
	}
	err := authz.RequireStewardPermission(ctx, tx, a.actorID, "risk.manage")
	if err == nil {
		err = authz.RequireStewardBanBounds(ctx, tx, a.actorID, action.DurationSeconds)
	}
	if errors.Is(err, authz.ErrPermissionDenied) || errors.Is(err, authz.ErrForbidden) {
		return false, nil
	}
	return err == nil, err
}
