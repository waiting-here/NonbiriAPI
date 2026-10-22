package resources

import (
	"context"
	"database/sql"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
)

type RotateCredentialInput struct {
	Secret             []byte
	OwnershipConfirmed bool
	ExpectedRevision   int64
}
type CredentialRotation struct {
	Credential Credential              `json:"credential"`
	Impact     CredentialImpactSummary `json:"impact"`
}

func (r *Repository) RotateCredential(ctx context.Context, userID, credentialID int64, mutation ControlMutation, input RotateCredentialInput) (MutationResult[CredentialRotation], error) {
	if r == nil || userID <= 0 || credentialID <= 0 || input.ExpectedRevision <= 0 || !input.OwnershipConfirmed || !validateEndpointSecretPlaintext(input.Secret) || !validResourceMutation(mutation, http.MethodPost, routeCredentialVersions, credentialID) {
		return MutationResult[CredentialRotation]{}, ErrInvalidRequest
	}
	writer, ok := r.secrets.(SharedSecretWriter)
	if !ok || isNilInterface(writer) {
		return MutationResult[CredentialRotation]{}, ErrUnavailable
	}
	plaintext := append([]byte(nil), input.Secret...)
	defer clear(plaintext)
	return ownerResourceMutation(ctx, r, userID, mutation, http.StatusCreated, "credential_version", func(ctx context.Context, tx *sql.Tx, now int64) (CredentialRotation, ResourceOperationResult, error) {
		item, err := getCredentialTx(ctx, tx, userID, credentialID)
		if err != nil {
			return CredentialRotation{}, ResourceOperationResult{}, err
		}
		if item.Revision != strconv.FormatInt(input.ExpectedRevision, 10) || input.ExpectedRevision == math.MaxInt64 {
			return CredentialRotation{}, ResourceOperationResult{}, ErrConflict
		}
		locked, err := credentialLockedTx(ctx, tx, userID, credentialID)
		if err != nil {
			return CredentialRotation{}, ResourceOperationResult{}, err
		}
		if locked {
			return CredentialRotation{}, ResourceOperationResult{}, ErrResourceLocked
		}
		var oldVersionID, version int64
		if err := tx.QueryRowContext(ctx, `SELECT c.current_version_id,max(v.version) FROM credentials c JOIN credential_versions v ON v.credential_id=c.id WHERE c.id=? GROUP BY c.id`, credentialID).Scan(&oldVersionID, &version); err != nil {
			return CredentialRotation{}, ResourceOperationResult{}, err
		}
		if version == math.MaxInt64 {
			return CredentialRotation{}, ResourceOperationResult{}, ErrConflict
		}
		impact, err := credentialImpactSummaryTx(ctx, tx, userID, credentialID)
		if err != nil {
			return CredentialRotation{}, ResourceOperationResult{}, err
		}
		stored, err := writer.WriteCredentialSecret(ctx, tx, plaintext, now)
		if err != nil {
			return CredentialRotation{}, ResourceOperationResult{}, err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO credential_versions(credential_id,version,secret_ref_id,display_head,display_tail,enabled,revision,created_at) VALUES(?,?,?,?,?,1,1,?)`, credentialID, version+1, stored.RefID, stored.DisplayHead, stored.DisplayTail, now)
		if err != nil {
			return CredentialRotation{}, ResourceOperationResult{}, err
		}
		versionID, err := result.LastInsertId()
		if err != nil {
			return CredentialRotation{}, ResourceOperationResult{}, err
		}
		result, err = tx.ExecContext(ctx, `UPDATE credentials SET current_version_id=?,revision=revision+1,updated_at=? WHERE id=? AND user_id=? AND revision=? AND deleted_at IS NULL`, versionID, now, credentialID, userID, input.ExpectedRevision)
		if err != nil {
			return CredentialRotation{}, ResourceOperationResult{}, err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return CredentialRotation{}, ResourceOperationResult{}, ErrConflict
		}
		retired, err := r.rotateCredentialAuthorizationsTx(ctx, tx, userID, credentialID, versionID, stored.RefID, now, writer)
		if err != nil {
			return CredentialRotation{}, ResourceOperationResult{}, err
		}
		retired[oldVersionID] = true
		for id := range retired {
			if _, err := connectionstore.RetireUnreferencedVersionTx(ctx, tx, id, now); err != nil {
				return CredentialRotation{}, ResourceOperationResult{}, mapConnectionStoreError(err)
			}
		}
		item, err = getCredentialTx(ctx, tx, userID, credentialID)
		return CredentialRotation{Credential: item, Impact: impact}, ResourceOperationResult{CredentialID: item.ID, CredentialVersion: item.CredentialVersion}, err
	})
}
