package resources

import (
	"context"
	"database/sql"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const credentialVersionSelect = `SELECT v.credential_id,v.version,v.enabled AND v.secret_ref_id IS NOT NULL,v.revision,v.display_head,v.display_tail,v.created_at
FROM credential_versions v JOIN credentials cr ON cr.id=v.credential_id WHERE cr.user_id=? AND cr.deleted_at IS NULL AND cr.id=?`

func scanCredentialVersion(scanner pageScanner) (CredentialVersion, error) {
	var item CredentialVersion
	var credentialID, version, revision int64
	var enabled int
	if err := scanner.Scan(&credentialID, &version, &enabled, &revision, &item.DisplayHead, &item.DisplayTail, &item.CreatedAt); err != nil {
		return CredentialVersion{}, err
	}
	if credentialID <= 0 || version <= 0 || revision <= 0 || enabled < 0 || enabled > 1 || len(item.DisplayHead) > 16 || len(item.DisplayTail) > 16 {
		return CredentialVersion{}, ErrUnavailable
	}
	item.CredentialID, item.CredentialVersion, item.Revision = strconv.FormatInt(credentialID, 10), strconv.FormatInt(version, 10), strconv.FormatInt(revision, 10)
	item.Enabled = enabled == 1
	return item, nil
}

func (r *Repository) listCredentialVersions(ctx context.Context, userID, credentialID int64, window connectionListWindow) (Page[CredentialVersion], error) {
	if credentialID <= 0 {
		return Page[CredentialVersion]{}, ErrNotFound
	}
	return userPageRead(ctx, r, userID, pagination.Default(), func(ctx context.Context, tx *sql.Tx) (Page[CredentialVersion], error) {
		if _, err := getCredentialTx(ctx, tx, userID, credentialID); err != nil {
			return Page[CredentialVersion]{}, err
		}
		return readConnectionList(ctx, tx, r, window, "credential-versions", credentialVersionSelect, "v.version", []any{userID, credentialID}, scanCredentialVersion, func(item CredentialVersion) string { return item.CredentialVersion })
	})
}

func (r *Repository) PatchCredentialVersion(ctx context.Context, userID, credentialID, version int64, mutation ControlMutation, expectedRevision int64, enabled bool) (MutationResult[CredentialVersion], error) {
	if r == nil || userID <= 0 || credentialID <= 0 || version <= 0 || expectedRevision <= 0 ||
		!validResourceMutation(mutation, http.MethodPatch, routeCredentialVersion, credentialID, version) {
		return MutationResult[CredentialVersion]{}, ErrInvalidRequest
	}
	return ownerResourceMutation(ctx, r, userID, mutation, http.StatusOK, "", func(ctx context.Context, tx *sql.Tx, now int64) (CredentialVersion, ResourceOperationResult, error) {
		if _, err := getCredentialTx(ctx, tx, userID, credentialID); err != nil {
			return CredentialVersion{}, ResourceOperationResult{}, err
		}
		locked, err := credentialLockedTx(ctx, tx, userID, credentialID)
		if err != nil {
			return CredentialVersion{}, ResourceOperationResult{}, err
		}
		if locked {
			return CredentialVersion{}, ResourceOperationResult{}, ErrResourceLocked
		}
		if expectedRevision == math.MaxInt64 {
			return CredentialVersion{}, ResourceOperationResult{}, ErrConflict
		}
		result, err := tx.ExecContext(ctx, `UPDATE credential_versions SET enabled=?,revision=revision+1
WHERE credential_id=? AND version=? AND revision=? AND secret_ref_id IS NOT NULL`, boolInt(enabled), credentialID, version, expectedRevision)
		if err != nil {
			return CredentialVersion{}, ResourceOperationResult{}, err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return CredentialVersion{}, ResourceOperationResult{}, ErrConflict
		}
		if err := r.reconcileCredentialRoutingTx(ctx, tx, userID, credentialID); err != nil {
			return CredentialVersion{}, ResourceOperationResult{}, err
		}
		item, err := scanCredentialVersion(tx.QueryRowContext(ctx, credentialVersionSelect+` AND v.version=?`, userID, credentialID, version))
		return item, ResourceOperationResult{}, err
	})
}

func (r *Repository) reconcileCredentialRoutingTx(ctx context.Context, tx *sql.Tx, userID, credentialID int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT g.endpoint_key_id FROM endpoint_key_grant_revisions g
WHERE g.credential_id=? AND g.owner_user_id=? AND EXISTS(SELECT 1 FROM endpoint_key_credential_evidence ev
 WHERE ev.endpoint_key_id=g.endpoint_key_id AND ev.grant_revision_id=g.id) ORDER BY g.endpoint_key_id`, credentialID, userID)
	if err != nil {
		return err
	}
	var keyIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		keyIDs = append(keyIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	models, err := bindingRevisionTargetsForKeysTx(ctx, tx, userID, keyIDs)
	if err != nil {
		return err
	}
	return r.reconcileRoutingTargetsTx(ctx, tx, userID, models)
}
