package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func keyServiceSelection(endpoint Endpoint, selected *[]contract.NativeService) (contract.NativeServiceSet, error) {
	allowed, err := contract.ParseNativeServices(endpoint.NativeServices)
	if err != nil || allowed.Empty() {
		return contract.NativeServiceSet{}, ErrUnavailable
	}
	services := allowed
	if selected != nil {
		services, err = contract.ParseNativeServices(*selected)
	}
	if err != nil || !services.IsSubsetOf(allowed) || contract.ValidateNativeServices(contract.Type(endpoint.ConnectorType), services, endpoint.RerankFormat) != nil {
		return contract.NativeServiceSet{}, ErrInvalidRequest
	}
	return services, nil
}

type credentialVersionReference struct{ credentialID, versionID, secretRefID int64 }

func currentCredentialReferenceTx(ctx context.Context, tx *sql.Tx, userID, credentialID int64) (credentialVersionReference, error) {
	var ref credentialVersionReference
	err := tx.QueryRowContext(ctx, `SELECT c.id,v.id,v.secret_ref_id FROM credentials c
 JOIN credential_versions v ON v.id=c.current_version_id AND v.credential_id=c.id
 JOIN endpoint_key_secrets s ON s.id=v.secret_ref_id
 WHERE c.id=? AND c.user_id=? AND c.deleted_at IS NULL AND v.retired_at IS NULL AND s.orphaned_at IS NULL`, credentialID, userID).Scan(&ref.credentialID, &ref.versionID, &ref.secretRefID)
	if errors.Is(err, sql.ErrNoRows) {
		return ref, ErrNotFound
	}
	return ref, err
}

func (r *Repository) resolveKeyCredentialTx(ctx context.Context, tx *sql.Tx, userID int64, endpoint Endpoint, input CreateEndpointKeyInput, now int64) (int64, int64, [32]byte, error) {
	var fingerprint [32]byte
	if input.CredentialID > 0 {
		ref, err := currentCredentialReferenceTx(ctx, tx, userID, input.CredentialID)
		if err != nil {
			return 0, 0, fingerprint, err
		}
		writer, ok := r.secrets.(SharedSecretWriter)
		if !ok || isNilInterface(writer) {
			return 0, 0, fingerprint, ErrUnavailable
		}
		fingerprint, err = writer.FingerprintEndpointSecret(ctx, tx, ref.secretRefID, endpoint.ConnectorType, endpoint.BaseURL)
		return ref.credentialID, ref.versionID, fingerprint, err
	}
	plaintext := append([]byte(nil), input.Secret...)
	defer clear(plaintext)
	stored, err := r.secrets.WriteEndpointSecret(ctx, tx, SecretWriteInput{CanonicalBaseURL: endpoint.BaseURL, ConnectorType: endpoint.ConnectorType, Plaintext: plaintext, CreatedAt: now})
	if err != nil {
		return 0, 0, fingerprint, fmt.Errorf("resources: store endpoint secret: %w", err)
	}
	var connectionID int64
	if endpoint.ConnectionID != nil {
		connectionID, _ = parseDecimalID(*endpoint.ConnectionID)
	}
	credentialID, versionID, err := createCredentialFromSecretTx(ctx, tx, userID, connectionID, input.Note, stored, now)
	return credentialID, versionID, stored.Fingerprint, err
}

func insertKeyGrantTx(ctx context.Context, tx *sql.Tx, grantID, keyID, endpointID, userID, credentialID, versionID, revision int64, services contract.NativeServiceSet, format contract.RerankFormat, fingerprint [32]byte, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO endpoint_key_grant_revisions(id,endpoint_key_id,endpoint_id,owner_user_id,credential_id,credential_version_id,grant_revision,native_service_mask,rerank_format,report_fingerprint,origin,created_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,'explicit',?)`, grantID, keyID, endpointID, userID, credentialID, versionID, revision, services.Mask(), string(format), fingerprint[:], now)
	return err
}

func (r *Repository) updateKeyAuthorizationTx(ctx context.Context, tx *sql.Tx, userID, endpointID, keyID int64, endpoint Endpoint, current EndpointKey, input PatchEndpointKeyInput, now int64) error {
	if input.CredentialID == nil && input.NativeServices == nil && input.DiscoverySourceID == nil {
		return nil
	}
	grantChanged := input.CredentialID != nil || input.NativeServices != nil
	var grantID int64
	if grantChanged {
		var err error
		endpoint, err = authorizeEndpointProtocolTx(ctx, tx, userID, endpoint, input.NativeServices, now)
		if err != nil {
			return err
		}
		selected := input.NativeServices
		if selected == nil {
			selected = &current.NativeServices
		}
		services, err := keyServiceSelection(endpoint, selected)
		if err != nil {
			return err
		}
		credentialID, _ := parseDecimalID(current.CredentialID)
		if input.CredentialID != nil {
			credentialID = *input.CredentialID
		}
		var versionID int64
		var fingerprint [32]byte
		if input.CredentialID != nil {
			credentialID, versionID, fingerprint, err = r.resolveKeyCredentialTx(ctx, tx, userID, endpoint, CreateEndpointKeyInput{CredentialID: credentialID}, now)
		} else {
			var raw []byte
			err = tx.QueryRowContext(ctx, `SELECT credential_version_id,secret_fingerprint FROM endpoint_key_current_credentials WHERE id=?`, keyID).Scan(&versionID, &raw)
			if err == nil && len(raw) != len(fingerprint) {
				return ErrUnavailable
			}
			copy(fingerprint[:], raw)
		}
		if err != nil {
			return err
		}
		revision, err := parseDecimalID(current.GrantRevision)
		if err != nil || revision == int64(^uint64(0)>>1) {
			return ErrConflict
		}
		grantID, err = nextConnectionIdentityTx(ctx, tx, "endpoint_key_grant_revisions")
		if err != nil {
			return err
		}
		if err := insertKeyGrantTx(ctx, tx, grantID, keyID, endpointID, userID, credentialID, versionID, revision+1, services, endpoint.RerankFormat, fingerprint, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE endpoint_keys SET current_grant_revision_id=? WHERE id=? AND endpoint_id=?`, grantID, keyID, endpointID); err != nil {
			return err
		}
	}
	if input.DiscoverySourceID != nil {
		if err := setKeySourceTx(ctx, tx, userID, keyID, *input.DiscoverySourceID); err != nil {
			return err
		}
	} else if current.DiscoverySourceID != nil {
		id, err := parseDecimalID(*current.DiscoverySourceID)
		if err != nil {
			return ErrUnavailable
		}
		if err := setKeySourceTx(ctx, tx, userID, keyID, &id); err != nil {
			return err
		}
	}
	if grantChanged {
		if err := r.stageKeyPendingUsesTx(ctx, tx, userID, keyID, grantID, input.CredentialID != nil, now); err != nil {
			return err
		}
		if input.NativeServices != nil {
			full, _ := contract.SupportedNativeServices(contract.Type(endpoint.ConnectorType))
			selected, _ := contract.ParseNativeServices(*input.NativeServices)
			if selected.Mask() == full.Mask() {
				if err := stageMissingProtocolUsesTx(ctx, tx, userID, keyID, grantID, now); err != nil {
					return err
				}
			}
		}
		if err := r.keyCreation.ProtectNewEndpointKey(ctx, tx, userID, keyID, now); err != nil {
			return err
		}
		if err := r.notifyPendingSubmissionsTx(ctx, tx, userID, []int64{grantID}); err != nil {
			return err
		}
	}
	if input.DiscoverySourceID != nil {
		if err := r.rebuildKeyCatalogProjectionTx(ctx, tx, userID, keyID, now); err != nil {
			return err
		}
	}
	return r.projection.ReconcileModelDiscovery(ctx, tx, userID, keyID)
}
