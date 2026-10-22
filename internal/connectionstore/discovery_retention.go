package connectionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// InterruptCredentialDiscoveryRunsTx is used after deleting a credential. It
// ends only work authorized by that credential's exact versions.
func InterruptCredentialDiscoveryRunsTx(ctx context.Context, tx *sql.Tx, credentialID, now int64) error {
	if tx == nil || credentialID <= 0 || now < 0 || now > 253402300799 {
		return ErrInvariant
	}
	var deleted sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT deleted_at FROM credentials WHERE id=?", credentialID).Scan(&deleted); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if !deleted.Valid {
		return ErrInvariant
	}
	return interruptDiscoveryRunsTx(ctx, tx, "credential_version_id IN(SELECT id FROM credential_versions WHERE credential_id=?)", credentialID, now)
}

// The selector is always a package-owned SQL expression, never user input.
// The acceptance receipt describes a completed continuation; its result is the
// failed/interrupted run. Existing operation-status reads derive this terminal
// state without rewriting the immutable acceptance result.
func interruptDiscoveryRunsTx(ctx context.Context, tx *sql.Tx, selector string, id, now int64) error {
	if _, err := tx.ExecContext(ctx, "UPDATE accepted_operations SET state='completed',checkpoint='',last_error_class=NULL,terminal_at=max(?,created_at) WHERE kind='model_discovery' AND state IN ('accepted','running') AND id IN(SELECT operation_id FROM discovery_runs WHERE state='checking' AND ("+selector+"))", now, id); err != nil {
		return fmt.Errorf("connectionstore: complete interrupted discovery operation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE discovery_runs SET state='failed',safe_class='interrupted',safe_diagnostic='',completed_at=max(?,started_at) WHERE state='checking' AND ("+selector+")", now, id); err != nil {
		return fmt.Errorf("connectionstore: interrupt discovery run: %w", err)
	}
	return nil
}
