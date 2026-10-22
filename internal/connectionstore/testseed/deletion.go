package testseed

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
)

// PrepareConnectionDeletionTx supplies the resource and credential steps for a
// fixture's account deletion. Callers must first hand off their claims,
// donations, reports and other domain state, just as the lifecycle coordinator
// does. This helper neither deletes the user nor settles any account.
func PrepareConnectionDeletionTx(ctx context.Context, tx *sql.Tx, ownerUserID, now int64) error {
	if tx == nil || ownerUserID <= 0 || now < 0 || now > 253402300799 {
		return connectionstore.ErrInvariant
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM endpoints WHERE user_id=?", ownerUserID); err != nil {
		return err
	}
	return connectionstore.PrepareAccountDeletionTx(ctx, tx, ownerUserID, now)
}
