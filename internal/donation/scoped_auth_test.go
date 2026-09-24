package donation

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

// The repository harness supplies a synthetic session after its database role
// check. Production adapters additionally validate the actual session record.
func (a *donationTestAuth) SessionActor(context.Context) (authz.Actor, bool) {
	if a.denySession.Load() {
		return authz.Actor{}, false
	}
	kind := authz.ActorUserSession
	if a.sessionAdmin.Load() {
		kind = authz.ActorAdminSession
	}
	return authz.Actor{Kind: kind, UserID: a.sessionUser.Load(), SessionTokenHash: "synthetic-session", SessionGeneration: strconv.FormatInt(a.sessionEpoch.Load(), 10)}, true
}

func (a *donationTestAuth) AuthorizeTraineeMutation(ctx context.Context, tx *sql.Tx, id int64) error {
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
