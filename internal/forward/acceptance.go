package forward

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

// CallerKeyAcceptanceGate binds economic acceptance to the credential checked
// at ingress. Reading the body and preparing a route may outlive key rotation.
type CallerKeyAcceptanceGate struct {
	Next claim.AcceptanceGate
}

func (g CallerKeyAcceptanceGate) AuthorizeChatAcceptance(ctx context.Context, tx *sql.Tx, userID, at int64) error {
	identity, ok := ctx.Value(callerIdentityContextKey{}).(resources.CallerIdentity)
	if !ok || !validCallerIdentity(identity) || identity.UserID != userID {
		return claim.ErrNotFound
	}
	var current bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM caller_keys WHERE user_id=? AND generation=? AND key_hash IS NOT NULL)`, userID, identity.Generation).Scan(&current); err != nil {
		return err
	}
	if !current {
		return claim.ErrNotFound
	}
	return g.Next.AuthorizeChatAcceptance(ctx, tx, userID, at)
}
