package connectionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

// StagePendingUseRevisionTx never approves a use. The caller must roll back
// its transaction on error, including a failed final CAS.
func StagePendingUseRevisionTx(ctx context.Context, tx *sql.Tx, in StagePendingUseRevisionInput) (UseRevisionIdentity, error) {
	if tx == nil || in.UseID <= 0 || in.ExpectedRevision <= 0 || in.ExpectedRevision == math.MaxInt64 || in.GrantRevisionID <= 0 || in.At < 0 || in.At > 253402300799 ||
		(in.Origin != UseOriginRotation && in.Origin != UseOriginReattachment && in.Origin != UseOriginServiceExpansion) ||
		(in.DiscoverySourceRevisionID != nil && *in.DiscoverySourceRevisionID <= 0) {
		return UseRevisionIdentity{}, ErrInvariant
	}
	var revision, keyID, ownerID, serviceBit, currentGrantID int64
	var priorPending sql.NullInt64
	var priorVersion int64
	var lifecycleLive int
	err := tx.QueryRowContext(ctx, "SELECT u.revision,u.current_pending_revision_id,u.native_service_bit,COALESCE(dk.endpoint_key_id,0),COALESCE(d.user_id,0),COALESCE(k.current_grant_revision_id,0),CASE WHEN dk.ended_at IS NULL AND d.status IN ('pending','approved') AND d.terminal_at IS NULL AND EXISTS(SELECT 1 FROM donation_key_memberships m WHERE m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id) THEN 1 ELSE 0 END FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donations d ON d.id=dk.donation_id LEFT JOIN endpoint_keys k ON k.id=dk.endpoint_key_id WHERE u.id=?", in.UseID).
		Scan(&revision, &priorPending, &serviceBit, &keyID, &ownerID, &currentGrantID, &lifecycleLive)
	if errors.Is(err, sql.ErrNoRows) {
		return UseRevisionIdentity{}, ErrNotFound
	}
	if err != nil {
		return UseRevisionIdentity{}, fmt.Errorf("connectionstore: read pending use: %w", err)
	}
	if lifecycleLive != 1 || ownerID <= 0 || keyID <= 0 {
		return UseRevisionIdentity{}, ErrUnavailable
	}
	if revision != in.ExpectedRevision || currentGrantID != in.GrantRevisionID {
		return UseRevisionIdentity{}, ErrConflict
	}
	var row grantRow
	err = tx.QueryRowContext(ctx, "SELECT "+grantReadColumns+" FROM endpoint_grant_authorizations a WHERE a.grant_revision_id=?", in.GrantRevisionID).Scan(row.destinations()...)
	if errors.Is(err, sql.ErrNoRows) {
		return UseRevisionIdentity{}, ErrInvariant
	}
	if err != nil {
		return UseRevisionIdentity{}, err
	}
	grant, err := row.facts()
	if err != nil {
		return UseRevisionIdentity{}, err
	}
	if grant.EndpointKeyID != keyID || grant.OwnerUserID != ownerID || grant.SecretRefID <= 0 || serviceBit <= 0 || serviceBit&(serviceBit-1) != 0 || grant.GrantedServices.Mask()&serviceBit != serviceBit {
		return UseRevisionIdentity{}, ErrUnavailable
	}
	if in.DiscoverySourceRevisionID != nil {
		var sourceOwner, sourceCredential, sourceVersion int64
		var relationshipValid int
		err := tx.QueryRowContext(ctx, "SELECT owner_user_id,credential_id,credential_version_id,relationship_valid FROM discovery_source_authorizations WHERE source_revision_id=?", *in.DiscoverySourceRevisionID).Scan(&sourceOwner, &sourceCredential, &sourceVersion, &relationshipValid)
		if errors.Is(err, sql.ErrNoRows) {
			return UseRevisionIdentity{}, ErrNotFound
		}
		if err != nil {
			return UseRevisionIdentity{}, err
		}
		if relationshipValid != 1 {
			return UseRevisionIdentity{}, ErrInvariant
		}
		if sourceOwner != ownerID || sourceCredential != grant.CredentialID || sourceVersion != grant.CredentialVersionID {
			return UseRevisionIdentity{}, ErrUnavailable
		}
	}
	var displayHead, displayTail string
	var fingerprint, bodyHMAC []byte
	err = tx.QueryRowContext(ctx, "SELECT v.display_head,v.display_tail,g.report_fingerprint,s.key_body_review_hmac FROM endpoint_key_grant_revisions g JOIN credential_versions v ON v.id=g.credential_version_id JOIN endpoint_key_secrets s ON s.id=v.secret_ref_id JOIN credentials c ON c.id=g.credential_id WHERE g.id=? AND c.deleted_at IS NULL AND v.retired_at IS NULL", in.GrantRevisionID).Scan(&displayHead, &displayTail, &fingerprint, &bodyHMAC)
	if errors.Is(err, sql.ErrNoRows) {
		return UseRevisionIdentity{}, ErrInvariant
	}
	if err != nil {
		return UseRevisionIdentity{}, err
	}
	if len(fingerprint) != 32 || len(bodyHMAC) != 32 {
		return UseRevisionIdentity{}, ErrInvariant
	}
	var contentRevision int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(use_revision),0) FROM donation_use_revisions WHERE use_id=?", in.UseID).Scan(&contentRevision); err != nil {
		return UseRevisionIdentity{}, err
	}
	if contentRevision < 0 || contentRevision == math.MaxInt64 {
		return UseRevisionIdentity{}, ErrInvariant
	}
	contentRevision++
	if priorPending.Valid {
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(g.credential_version_id,0) FROM donation_use_revisions ur LEFT JOIN endpoint_key_grant_revisions g ON g.id=ur.grant_revision_id WHERE ur.id=? AND ur.use_id=?", priorPending.Int64, in.UseID).Scan(&priorVersion); err != nil {
			return UseRevisionIdentity{}, err
		}
		result, err := tx.ExecContext(ctx, "UPDATE donation_use_revisions SET review_state='superseded',review_reason='',reviewer_user_id=NULL,reviewer_role=NULL,reviewed_at=? WHERE id=? AND use_id=? AND review_state='pending'", in.At, priorPending.Int64, in.UseID)
		if err != nil {
			return UseRevisionIdentity{}, err
		}
		if err := requireOne(result); err != nil {
			return UseRevisionIdentity{}, err
		}
	}
	var source any
	if in.DiscoverySourceRevisionID != nil {
		source = *in.DiscoverySourceRevisionID
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO donation_use_revisions(use_id,use_revision,grant_revision_id,discovery_source_revision_id,source_grant_revision_id,source_discovery_revision_id,credential_id_snapshot,credential_version,grant_revision,display_head,display_tail,report_fingerprint,key_body_review_hmac,origin,review_state,review_reason,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'pending','',?)", in.UseID, contentRevision, in.GrantRevisionID, source, in.GrantRevisionID, source, grant.CredentialID, grant.CredentialVersion, grant.GrantRevision, displayHead, displayTail, fingerprint, bodyHMAC, string(in.Origin), in.At)
	if err != nil {
		return UseRevisionIdentity{}, fmt.Errorf("connectionstore: stage use revision: %w", err)
	}
	revisionID, err := result.LastInsertId()
	if err != nil || revisionID <= 0 {
		return UseRevisionIdentity{}, ErrInvariant
	}
	result, err = tx.ExecContext(ctx, "UPDATE donation_uses SET current_pending_revision_id=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?", revisionID, in.At, in.UseID, in.ExpectedRevision)
	if err != nil {
		return UseRevisionIdentity{}, err
	}
	if err := requireOne(result); err != nil {
		return UseRevisionIdentity{}, err
	}
	if priorVersion > 0 {
		if _, err := RetireUnreferencedVersionTx(ctx, tx, priorVersion, in.At); err != nil {
			return UseRevisionIdentity{}, err
		}
	}
	return UseRevisionIdentity{UseID: in.UseID, UseRevision: in.ExpectedRevision + 1, RevisionID: revisionID, Revision: contentRevision, Origin: in.Origin}, nil
}

func requireOne(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}
