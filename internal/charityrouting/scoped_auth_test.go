package charityrouting

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func testRoleActor(role roleKind, steward int64) int64 {
	if role == roleAdmin {
		return 0
	}
	return steward
}

func (a *routingTestAuth) SessionActor(context.Context) (authz.Actor, bool) {
	if a.denySession.Load() {
		return authz.Actor{}, false
	}
	kind := authz.ActorUserSession
	if a.sessionAdmin.Load() {
		kind = authz.ActorAdminSession
	}
	return authz.Actor{Kind: kind, UserID: a.sessionUser.Load(), SessionTokenHash: "synthetic-session", SessionGeneration: strconv.FormatInt(a.sessionEpoch.Load(), 10)}, true
}

func (a *routingTestAuth) AuthorizeTraineeMutation(ctx context.Context, tx *sql.Tx, id int64) error {
	if a.denySteward.Load() {
		return ErrForbidden
	}
	var allowed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0 AND is_banned=0 AND COALESCE(level,auto_level)=5)`, id).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	a.sessionUser.Store(id)
	a.sessionAdmin.Store(false)
	return nil
}
