package resources

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
)

func resourceIDsTx(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return ids, nil
}

func (r *Repository) DeleteCredential(ctx context.Context, userID, credentialID int64, mutation ControlMutation, expectedRevision int64) (MutationResult[struct{}], error) {
	if r == nil || userID <= 0 || credentialID <= 0 || expectedRevision <= 0 || !validResourceMutation(mutation, http.MethodDelete, routeCredential, credentialID) {
		return MutationResult[struct{}]{}, ErrInvalidRequest
	}
	return ownerResourceMutation(ctx, r, userID, mutation, http.StatusNoContent, "", func(ctx context.Context, tx *sql.Tx, now int64) (struct{}, ResourceOperationResult, error) {
		return struct{}{}, ResourceOperationResult{}, r.deleteCredentialInTransaction(ctx, tx, userID, credentialID, expectedRevision, now)
	})
}

func (r *Repository) deleteCredentialInTransaction(ctx context.Context, tx *sql.Tx, userID, credentialID, expectedRevision, now int64) error {
	item, err := getCredentialTx(ctx, tx, userID, credentialID)
	if err != nil {
		return err
	}
	if item.Revision != strconv.FormatInt(expectedRevision, 10) || expectedRevision == math.MaxInt64 {
		return ErrConflict
	}
	locked, err := credentialLockedTx(ctx, tx, userID, credentialID)
	if err != nil {
		return err
	}
	if locked {
		return ErrResourceLocked
	}
	keys, err := resourceIDsTx(ctx, tx, `SELECT DISTINCT g.endpoint_key_id FROM endpoint_key_grant_revisions g WHERE g.credential_id=? AND g.owner_user_id=?`, credentialID, userID)
	if err != nil {
		return err
	}
	models, err := bindingRevisionTargetsForKeysTx(ctx, tx, userID, keys)
	if err != nil {
		return err
	}
	versions, err := resourceIDsTx(ctx, tx, `SELECT id FROM credential_versions WHERE credential_id=? AND secret_ref_id IS NOT NULL ORDER BY id`, credentialID)
	if err != nil {
		return err
	}
	// A key can use a newer personal credential while keeping an older charity
	// approval. Cancel only revisions belonging to the deleted credential.
	if _, err := tx.ExecContext(ctx, `UPDATE donation_use_revisions SET review_state='cancelled',review_reason='',reviewer_user_id=NULL,reviewer_role=NULL,reviewed_at=max(?,created_at)
 WHERE credential_id_snapshot=? AND review_state='pending' AND id IN(SELECT current_pending_revision_id FROM donation_uses)`, now, credentialID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE donation_uses SET
 current_approved_revision_id=CASE WHEN EXISTS(SELECT 1 FROM donation_use_revisions ur WHERE ur.id=current_approved_revision_id AND ur.credential_id_snapshot=?) THEN NULL ELSE current_approved_revision_id END,
 current_pending_revision_id=CASE WHEN EXISTS(SELECT 1 FROM donation_use_revisions ur WHERE ur.id=current_pending_revision_id AND ur.credential_id_snapshot=?) THEN NULL ELSE current_pending_revision_id END,
 revision=revision+1,updated_at=max(updated_at,?)
 WHERE EXISTS(SELECT 1 FROM donation_use_revisions ur WHERE ur.credential_id_snapshot=? AND ur.id IN(current_approved_revision_id,current_pending_revision_id))`, credentialID, credentialID, now, credentialID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE credentials SET current_version_id=NULL,connection_id=NULL,note='',enabled=0,deleted_at=?,updated_at=max(updated_at,?),revision=revision+1 WHERE id=? AND user_id=? AND revision=? AND deleted_at IS NULL`, now, now, credentialID, userID, expectedRevision)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrConflict
	}
	if err := connectionstore.InterruptCredentialDiscoveryRunsTx(ctx, tx, credentialID, now); err != nil {
		return mapConnectionStoreError(err)
	}
	for _, id := range versions {
		if _, err := connectionstore.RetireUnreferencedVersionTx(ctx, tx, id, now); err != nil {
			return mapConnectionStoreError(err)
		}
	}
	return r.reconcileRoutingTargetsTx(ctx, tx, userID, models)
}
