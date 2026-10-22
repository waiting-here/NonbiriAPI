package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type OwnBindingDiscovery struct {
	UseID, ApprovedRevisionID, SourceRevisionID, CredentialVersionID int64
	NativeService                                                    contract.NativeService
}

// ErrDiscoveryModelMissing means an authorized, successful discovery run did
// not contain the requested model. It does not describe missing authorization.
var ErrDiscoveryModelMissing = errors.New("charity routing: discovered model missing")

// EnsureOwnBindingInTransaction appends one owned donation key, or preserves
// an identical existing binding. A positive discovery revision requires that
// exact successful automatic catalog; zero requires a manual entry.
func (s *Service) EnsureOwnBindingInTransaction(ctx context.Context, tx *sql.Tx, userID, modelID, donationKeyID int64, upstreamModelID string, discoveryRevision int64, discovery ...OwnBindingDiscovery) error {
	if s == nil || tx == nil || userID <= 0 || modelID <= 0 || donationKeyID <= 0 || discoveryRevision < 0 {
		return ErrInvalidRequest
	}
	if _, err := validateSelections([]BindingSelection{{DonationKeyID: strconv.FormatInt(donationKeyID, 10), UpstreamModelID: upstreamModelID}}); err != nil {
		return err
	}
	if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, userID); err != nil {
		return mapAuthorization(err)
	}
	now, err := s.nowUnix()
	if err != nil {
		return err
	}
	revision, count, err := readBindingHeadTx(ctx, tx, modelID)
	if err != nil {
		return err
	}
	var keyID int64
	err = tx.QueryRowContext(ctx, `SELECT dk.endpoint_key_id FROM donation_keys dk
JOIN donations d ON d.id=dk.donation_id
JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id
JOIN endpoint_keys k ON k.id=m.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id
WHERE dk.id=? AND d.user_id=? AND e.user_id=? AND d.status IN ('pending','approved') AND d.terminal_at IS NULL
AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?)`, donationKeyID, userID, userID, now).Scan(&keyID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if discoveryRevision > 0 {
		if len(discovery) != 1 {
			return ErrInvalidRequest
		}
		selected := discovery[0]
		use, err := connectionstore.LoadApprovedUseTx(ctx, tx, donationKeyID, selected.NativeService)
		if err != nil {
			return targetConnectionError(err)
		}
		if !use.DiscoveryEnabled || use.UseID != selected.UseID || use.ApprovedRevisionID != selected.ApprovedRevisionID || use.DiscoverySourceRevisionID != selected.SourceRevisionID || use.Grant.CredentialVersionID != selected.CredentialVersionID {
			return ErrConflict
		}
		var present bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_catalog_entries c WHERE c.discovery_run_id=r.id AND c.source_type='automatic' AND c.normalized_model_id=?)
FROM discovery_source_states st JOIN discovery_runs r ON r.id=st.last_successful_run_id AND r.source_revision_id=st.source_revision_id AND r.state='succeeded'
WHERE st.source_revision_id=? AND r.credential_version_id=? AND r.result_generation=?`, upstreamModelID, selected.SourceRevisionID, selected.CredentialVersionID, discoveryRevision).Scan(&present)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !present {
			return ErrDiscoveryModelMissing
		}
	} else {
		if len(discovery) != 0 {
			return ErrInvalidRequest
		}
		if err := ensureOwnManualModelTx(ctx, tx, donationKeyID, upstreamModelID, now); err != nil {
			return err
		}
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM charity_model_bindings b JOIN charity_binding_targets t ON t.binding_id=b.id WHERE b.charity_model_id=? AND t.donation_key_id=? AND t.upstream_model_id=?)`, modelID, donationKeyID, upstreamModelID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	var targetsCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM charity_binding_targets t JOIN charity_model_bindings b ON b.id=t.binding_id WHERE b.charity_model_id=?`, modelID).Scan(&targetsCount); err != nil {
		return err
	}
	if targetsCount >= maxBindingBatch {
		return ErrResourceLimit
	}
	targets := []validatedTarget{{keyID: donationKeyID, upstream: upstreamModelID, legacy: true}}
	if err := validateTargetsTx(ctx, tx, charityscope.Scope{}, modelID, targets, now); err != nil {
		return err
	}
	if _, err := insertBindingGroupTx(ctx, tx, modelID, count, targets, now); err != nil {
		return classifyWrite("append owned charity binding", err)
	}
	return advanceBindingRevisionTx(ctx, tx, modelID, revision, now)
}

func ensureOwnManualModelTx(ctx context.Context, tx *sql.Tx, keyID int64, model string, now int64) error {
	var count, revision int64
	var present bool
	if err := tx.QueryRowContext(ctx, `SELECT manual_catalog_revision,(SELECT COUNT(*) FROM donation_key_manual_models WHERE donation_key_id=?),EXISTS(SELECT 1 FROM donation_key_manual_models WHERE donation_key_id=? AND normalized_model_id=?) FROM donation_keys WHERE id=?`, keyID, keyID, model, keyID).Scan(&revision, &count, &present); err != nil {
		return err
	}
	if present {
		return nil
	}
	if count >= 1024 || revision == math.MaxInt64 {
		return ErrResourceLimit
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO donation_key_manual_models(donation_key_id,normalized_model_id,display_name,revision,created_at,updated_at) VALUES(?,?,?,1,?,?)`, keyID, model, model, now, now); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE donation_keys SET manual_catalog_revision=manual_catalog_revision+1 WHERE id=?`, keyID)
	return err
}
