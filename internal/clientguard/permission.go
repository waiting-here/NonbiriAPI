package clientguard

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/riskaudit"
)

// A persisted rule binding does not preserve authority after its editor loses
// access. The target and editor are checked again in the effects transaction.
func ruleActionAllowed(ctx context.Context, tx *sql.Tx, ruleID string, targetID, now int64, action riskaudit.AutoBan) (bool, error) {
	var actorID int64
	var admin, level, targetLevel int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(u.id,0),COALESCE(u.is_admin,CASE r.updated_by_role WHEN 'admin' THEN 1 ELSE 0 END),COALESCE(u.level,u.auto_level,0),COALESCE(t.level,t.auto_level) FROM client_rule_auto_bans b JOIN risk_client_rules r ON r.id=b.rule_id LEFT JOIN users u ON u.id=b.actor_user_id JOIN users t ON t.id=? AND t.is_admin=0 WHERE b.rule_id=? AND (u.is_banned=0 OR u.banned_until<=? OR u.id IS NULL AND r.updated_by_role='admin')`, targetID, ruleID, now).Scan(&actorID, &admin, &level, &targetLevel)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if admin != 0 {
		return true, nil
	}
	if actorID == targetID || level < 5 || targetLevel >= level {
		return false, nil
	}
	if err = authz.RequireStewardPermission(ctx, tx, actorID, "risk.manage"); err == nil {
		err = authz.RequireStewardBanBounds(ctx, tx, actorID, action.DurationSeconds)
	}
	if errors.Is(err, authz.ErrPermissionDenied) || errors.Is(err, authz.ErrForbidden) {
		return false, nil
	}
	return err == nil, err
}
