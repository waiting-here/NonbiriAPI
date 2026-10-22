package donation

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type useSubmission struct {
	grant            connectionstore.GrantAuthorization
	services         contract.NativeServiceSet
	sourceRevisionID *int64
}

func connectionError(err error) error {
	switch {
	case errors.Is(err, connectionstore.ErrNotFound), errors.Is(err, connectionstore.ErrUnavailable):
		return ErrNotFound
	case errors.Is(err, connectionstore.ErrConflict):
		return ErrConflict
	case errors.Is(err, connectionstore.ErrInvariant):
		return ErrInvariant
	default:
		return err
	}
}

func attachSubmissionUsesTx(ctx context.Context, tx *sql.Tx, ownerID int64, inputs []CreateKeyInput, keys []submissionKey) error {
	ids := make([]int64, 0, len(keys))
	byKey := make(map[int64]CreateKeyInput, len(inputs))
	for _, input := range inputs {
		byKey[input.EndpointKeyID] = input
	}
	for _, key := range keys {
		ids = append(ids, key.id)
	}
	grants, err := connectionstore.LoadSelfGrantsTx(ctx, tx, ownerID, ids)
	if err != nil {
		return connectionError(err)
	}
	for index := range keys {
		key := &keys[index]
		grant, ok := grants[key.id]
		if !ok {
			return ErrNotFound
		}
		input := byKey[key.id]
		if input.CredentialID != 0 || input.CredentialVersion != 0 || input.GrantRevision != 0 {
			if input.CredentialID <= 0 || input.CredentialVersion <= 0 || input.GrantRevision <= 0 {
				return ErrInvalidRequest
			}
			if input.CredentialID != grant.CredentialID || input.CredentialVersion != grant.CredentialVersion || input.GrantRevision != grant.GrantRevision {
				return ErrConflict
			}
		}
		services := contract.NativeServiceSet{}
		if input.NativeServices == nil {
			services, err = contract.LegacyNativeServices(grant.ConfiguredType)
			services = services.Intersect(grant.EffectiveServices)
		} else {
			services, err = contract.ParseNativeServices(input.NativeServices)
		}
		if err != nil || services.Empty() || services.Intersect(grant.EffectiveServices).Mask() != services.Mask() {
			return ErrInvalidRequest
		}
		key.use = useSubmission{grant: grant, services: services}
		if (input.DiscoverySourceID == nil) != (input.DiscoverySourceRevision == nil) {
			return ErrInvalidRequest
		}
		if input.DiscoverySourceID != nil {
			if *input.DiscoverySourceID <= 0 || *input.DiscoverySourceRevision <= 0 {
				return ErrInvalidRequest
			}
			source, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: ownerID, SourceID: *input.DiscoverySourceID, Purpose: connectionstore.SourceSelf})
			if err != nil {
				return connectionError(err)
			}
			if source.SourceRevision != *input.DiscoverySourceRevision || source.CredentialVersionID != grant.CredentialVersionID {
				return ErrConflict
			}
			key.use.sourceRevisionID = &source.SourceRevisionID
		}
	}
	return nil
}

func createInitialUsesTx(ctx context.Context, tx *sql.Tx, keyID int64, submission useSubmission, approved bool, now int64) error {
	g := submission.grant
	var head, tail string
	var fingerprint, bodyHMAC []byte
	var requirement sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT v.display_head,v.display_tail,g.report_fingerprint,s.key_body_review_hmac,r.revision
FROM endpoint_key_grant_revisions g JOIN credential_versions v ON v.id=g.credential_version_id
JOIN endpoint_key_secrets s ON s.id=v.secret_ref_id LEFT JOIN donation_key_review_requirements r ON r.hmac=s.key_body_review_hmac WHERE g.id=?`, g.GrantRevisionID).Scan(&head, &tail, &fingerprint, &bodyHMAC, &requirement)
	if err != nil {
		return err
	}
	defer clear(fingerprint)
	defer clear(bodyHMAC)
	if len(fingerprint) != 32 || len(bodyHMAC) != 32 {
		return ErrReviewMaterialUnavailable
	}
	for _, service := range submission.services.Services() {
		set, err := contract.ParseNativeServices([]contract.NativeService{service})
		if err != nil {
			return ErrInvariant
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO donation_uses(donation_key_id,native_service_bit,enabled,discovery_enabled,revision,created_at,updated_at) VALUES(?,?,1,?,1,?,?)`, keyID, set.Mask(), submission.sourceRevisionID != nil, now, now)
		if err != nil {
			return err
		}
		useID, err := result.LastInsertId()
		if err != nil || useID <= 0 {
			return ErrInvariant
		}
		result, err = tx.ExecContext(ctx, `INSERT INTO donation_use_revisions
(use_id,use_revision,grant_revision_id,discovery_source_revision_id,source_grant_revision_id,source_discovery_revision_id,credential_id_snapshot,credential_version,grant_revision,display_head,display_tail,report_fingerprint,key_body_review_hmac,origin,review_state,review_reason,review_requirement_revision,created_at,reviewed_at)
VALUES(?,1,?,?,?,?,?,?,?,?,?,?,?,'initial','pending','',?,?,NULL)`, useID, g.GrantRevisionID, submission.sourceRevisionID, g.GrantRevisionID, submission.sourceRevisionID, g.CredentialID, g.CredentialVersion, g.GrantRevision, head, tail, fingerprint, bodyHMAC, requirement, now)
		if err != nil {
			return err
		}
		revisionID, err := result.LastInsertId()
		if err != nil || revisionID <= 0 {
			return ErrInvariant
		}
		var pending, approvedID any
		if approved {
			if _, err = tx.ExecContext(ctx, `UPDATE donation_use_revisions SET review_state='approved',reviewed_at=? WHERE id=? AND review_state='pending'`, now, revisionID); err != nil {
				return err
			}
			approvedID = revisionID
		} else {
			pending = revisionID
		}
		result, err = tx.ExecContext(ctx, `UPDATE donation_uses SET current_approved_revision_id=?,current_pending_revision_id=?,revision=revision+1 WHERE id=? AND revision=1`, approvedID, pending, useID)
		if err != nil {
			return err
		}
		if err := requireOne(result); err != nil {
			return err
		}
	}
	return nil
}
