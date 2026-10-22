package connectionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ReleaseClaimAuthorizationTx runs after the caller has persisted the terminal
// claim and safe attempt facts. It never starts or commits a transaction.
func ReleaseClaimAuthorizationTx(ctx context.Context, tx *sql.Tx, claimID string, now int64) error {
	if tx == nil || len(claimID) != 26 || !strings.HasPrefix(claimID, "clm_") || now < 0 || now > 253402300799 {
		return ErrInvariant
	}
	var state string
	var secretRef, terminalAt sql.NullInt64
	err := tx.QueryRowContext(ctx, "SELECT state,secret_ref_id,terminal_at FROM dispatch_claims WHERE id=?", claimID).Scan(&state, &secretRef, &terminalAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("connectionstore: read terminal claim: %w", err)
	}
	if (state != "committed" && state != "released") || secretRef.Valid || !terminalAt.Valid || terminalAt.Int64 < 0 || terminalAt.Int64 > 253402300799 {
		return ErrInvariant
	}
	var versionID int64
	err = tx.QueryRowContext(ctx, "SELECT credential_version_id FROM dispatch_claim_targets WHERE claim_id=?", claimID).Scan(&versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("connectionstore: read claim authorization: %w", err)
	}
	if versionID <= 0 {
		return ErrInvariant
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM dispatch_claim_targets WHERE claim_id=? AND credential_version_id=?", claimID, versionID)
	if err != nil {
		return fmt.Errorf("connectionstore: release claim authorization: %w", err)
	}
	if err := requireOne(result); err != nil {
		return err
	}
	_, err = RetireUnreferencedVersionTx(ctx, tx, versionID, max(now, terminalAt.Int64))
	return err
}

// RetireUnreferencedVersionTx removes only an unused version's secret link.
// Ciphertext deletion remains subject to the existing orphan retention period.
func RetireUnreferencedVersionTx(ctx context.Context, tx *sql.Tx, versionID, now int64) (bool, error) {
	if tx == nil || versionID <= 0 || now < 0 || now > 253402300799 {
		return false, ErrInvariant
	}
	var secretRef, retiredAt sql.NullInt64
	var createdAt int64
	err := tx.QueryRowContext(ctx, "SELECT secret_ref_id,created_at,retired_at FROM credential_versions WHERE id=?", versionID).Scan(&secretRef, &createdAt, &retiredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("connectionstore: read retired version: %w", err)
	}
	if createdAt < 0 || createdAt > 253402300799 || secretRef.Valid == retiredAt.Valid || secretRef.Valid && secretRef.Int64 <= 0 {
		return false, ErrInvariant
	}
	if !secretRef.Valid {
		return false, nil
	}
	live, err := HasLiveVersionReferenceTx(ctx, tx, versionID)
	if err != nil || live {
		return false, err
	}
	// Legacy claims may still hold the secret without a version snapshot.
	live, err = HasLiveSecretReferenceTx(ctx, tx, secretRef.Int64)
	if err != nil || live {
		return false, err
	}
	at := max(now, createdAt)
	result, err := tx.ExecContext(ctx, "UPDATE credential_versions SET secret_ref_id=NULL,display_head='',display_tail='',retired_at=? WHERE id=? AND secret_ref_id=? AND NOT EXISTS(SELECT 1 FROM credential_version_live_references WHERE credential_version_id=?) AND NOT EXISTS(SELECT 1 FROM secret_live_references WHERE secret_ref_id=?)", at, versionID, secretRef.Int64, versionID, secretRef.Int64)
	if err != nil {
		return false, fmt.Errorf("connectionstore: retire credential version: %w", err)
	}
	if err := requireOne(result); err != nil {
		return false, err
	}
	result, err = tx.ExecContext(ctx, "UPDATE endpoint_key_secrets SET orphaned_at=COALESCE(orphaned_at,?) WHERE id=? AND NOT EXISTS(SELECT 1 FROM secret_live_references WHERE secret_ref_id=?)", at, secretRef.Int64, secretRef.Int64)
	if err != nil {
		return false, fmt.Errorf("connectionstore: mark retired secret: %w", err)
	}
	if err := requireOne(result); err != nil {
		return false, err
	}
	return true, nil
}
