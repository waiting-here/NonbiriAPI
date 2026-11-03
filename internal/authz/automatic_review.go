package authz

import (
	"context"
	"database/sql"
)

type automaticReviewKey struct{}

// WithAutomaticReviewAuthority is only for the trusted management dispatcher,
// AFTER it has selected a registered read or model-discovery handler. It is not
// a session, is never decoded from HTTP/model input, and cannot grant elevation.
// Each domain transaction still checks the persisted service owner and mode.
func WithAutomaticReviewAuthority(ctx context.Context, ownerID int64) context.Context {
	return context.WithValue(ctx, automaticReviewKey{}, ownerID)
}

func AutomaticReviewOwner(ctx context.Context) int64 {
	id, _ := ctx.Value(automaticReviewKey{}).(int64)
	return id
}

func (a *Authorizer) authorizeAutomaticReview(ctx context.Context, tx *sql.Tx, actor Actor, requirement Requirement) (Principal, error) {
	if actor.Kind != ActorAdminSession || actor.UserID != AutomaticReviewOwner(ctx) || actor.SessionTokenHash != "" || requirement.Role != RoleAdministrator || requirement.Owner != nil || requirement.FreshElevation {
		return Principal{}, ErrForbidden
	}
	var level int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(u.level,u.auto_level) FROM ai_agent_settings c JOIN users u ON u.id=c.owner_id
WHERE c.id=1 AND c.mode<>'off' AND c.owner_id=? AND u.is_admin=1 AND (u.is_banned=0 OR u.banned_until<=?)`, actor.UserID, a.now().Unix()).Scan(&level)
	if err == sql.ErrNoRows {
		return Principal{}, ErrForbidden
	}
	if err != nil {
		return Principal{}, err
	}
	return Principal{UserID: actor.UserID, Role: RoleAdministrator, EffectiveLevel: level}, nil
}
