package donation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type AppendUseItem struct {
	NativeService    contract.NativeService
	ExpectedRevision *int64
}

type AppendUsesInput struct {
	ExpectedRevision        int64
	DonationKeyID           int64
	CredentialID            int64
	CredentialVersion       int64
	GrantRevision           int64
	Items                   []AppendUseItem
	DiscoverySourceID       *int64
	DiscoverySourceRevision *int64
}

type AppendedUse struct {
	ID            string                 `json:"id"`
	Revision      string                 `json:"revision"`
	NativeService contract.NativeService `json:"native_service"`
}

type AppendUsesReceipt struct {
	DonationRevision string        `json:"donation_revision"`
	Uses             []AppendedUse `json:"uses"`
}

func (in AppendUsesInput) services() (contract.NativeServiceSet, error) {
	if in.ExpectedRevision <= 0 || in.ExpectedRevision == math.MaxInt64 ||
		in.DonationKeyID <= 0 || in.CredentialID <= 0 || in.CredentialVersion <= 0 || in.GrantRevision <= 0 ||
		len(in.Items) == 0 || len(in.Items) > 19 ||
		(in.DiscoverySourceID == nil) != (in.DiscoverySourceRevision == nil) ||
		in.DiscoverySourceID != nil && (*in.DiscoverySourceID <= 0 || *in.DiscoverySourceRevision <= 0) {
		return contract.NativeServiceSet{}, ErrInvalidRequest
	}
	values := make([]contract.NativeService, len(in.Items))
	for i, item := range in.Items {
		if item.ExpectedRevision != nil && (*item.ExpectedRevision <= 0 || *item.ExpectedRevision == math.MaxInt64) {
			return contract.NativeServiceSet{}, ErrInvalidRequest
		}
		values[i] = item.NativeService
	}
	services, err := contract.ParseNativeServices(values)
	if err != nil {
		return contract.NativeServiceSet{}, ErrInvalidRequest
	}
	return services, nil
}

// AppendUses explicitly submits additional services without replacing the member's budget.
func (s *Service) AppendUses(ctx context.Context, userID, donationID int64, input AppendUsesInput, mutation resources.ControlMutation) (resources.MutationResult[AppendUsesReceipt], error) {
	var empty resources.MutationResult[AppendUsesReceipt]
	services, err := input.services()
	if err != nil || ctx == nil || userID <= 0 || donationID <= 0 ||
		!validScopedMutation(ctx, mutation, http.MethodPost, routeOwnerUses, donationID) {
		return empty, ErrInvalidRequest
	}
	tx, err := s.beginOwnerTx(ctx, userID)
	if err != nil {
		return empty, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	// Ownership remains required on replay; lifecycle and CAS apply only once.
	var revision int64
	var keyID sql.NullInt64
	var live bool
	err = tx.QueryRowContext(ctx, `SELECT d.revision,dk.endpoint_key_id,
d.status IN ('pending','approved') AND d.terminal_at IS NULL
AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?)
AND EXISTS(SELECT 1 FROM donation_key_memberships m
 WHERE m.donation_key_id=dk.id AND m.donation_id=d.id AND m.endpoint_key_id=dk.endpoint_key_id)
FROM donations d JOIN donation_keys dk ON dk.donation_id=d.id
WHERE d.id=? AND d.user_id=? AND dk.id=?`, now, donationID, userID, input.DonationKeyID).Scan(&revision, &keyID, &live)
	if errors.Is(err, sql.ErrNoRows) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, fmt.Errorf("donation: read append member: %w", err)
	}
	decision, err := beginMutation(ctx, tx, "user", userID, idempotency.ScopeDonation, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[AppendUsesReceipt](decision)
	}
	if !live || !keyID.Valid || revision != input.ExpectedRevision {
		return empty, ErrConflict
	}
	if enabled, err := configBool(ctx, tx, "donation_accept_enabled"); err != nil {
		return empty, err
	} else if !enabled {
		return empty, ErrFeatureDisabled
	}
	var suspended bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM endpoint_key_suspensions WHERE endpoint_key_id=?)", keyID.Int64).Scan(&suspended); err != nil {
		return empty, err
	}
	if suspended {
		return empty, ErrNotFound
	}
	grant, err := connectionstore.LoadSelfGrantTx(ctx, tx, userID, keyID.Int64)
	if err != nil {
		return empty, connectionError(err)
	}
	if grant.CredentialID != input.CredentialID || grant.CredentialVersion != input.CredentialVersion || grant.GrantRevision != input.GrantRevision {
		return empty, ErrConflict
	}
	full, err := contract.SupportedNativeServices(grant.ConfiguredType)
	if err != nil {
		return empty, ErrInvariant
	}
	wholeProtocol := services.Mask() == full.Mask()
	if wholeProtocol {
		if _, err := resources.AuthorizeProtocolServicesTx(ctx, tx, userID, keyID.Int64, now, false); err != nil {
			return empty, protocolAuthorizationError(err)
		}
		grant, err = connectionstore.LoadSelfGrantTx(ctx, tx, userID, keyID.Int64)
		if err != nil {
			return empty, connectionError(err)
		}
	} else if !services.IsSubsetOf(grant.EffectiveServices) {
		return empty, ErrConflict
	}
	sourceRevisionID, err := connectionstore.ConfiguredDiscoverySourceRevisionTx(ctx, tx, grant)
	if err != nil {
		return empty, connectionError(err)
	}
	if input.DiscoverySourceID != nil {
		source, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{
			OwnerUserID: userID, SourceID: *input.DiscoverySourceID, Purpose: connectionstore.SourceSelf,
		})
		if err != nil {
			return empty, connectionError(err)
		}
		if source.SourceRevision != *input.DiscoverySourceRevision || source.CredentialVersionID != grant.CredentialVersionID {
			return empty, ErrConflict
		}
		sourceRevisionID = &source.SourceRevisionID
	}
	value := AppendUsesReceipt{DonationRevision: strconv.FormatInt(revision+1, 10), Uses: make([]AppendedUse, 0, len(input.Items))}
	for _, item := range input.Items {
		use, err := appendUseTx(ctx, tx, input.DonationKeyID, item, grant.GrantRevisionID, sourceRevisionID, now, wholeProtocol)
		if err != nil {
			return empty, err
		}
		value.Uses = append(value.Uses, use)
	}
	result, err := tx.ExecContext(ctx, "UPDATE donations SET revision=revision+1,updated_at=? WHERE id=? AND user_id=? AND revision=?", now, donationID, userID, revision)
	if err != nil {
		return empty, err
	}
	if err := requireOne(result); err != nil {
		return empty, err
	}
	ids := make([]int64, len(value.Uses))
	for i, use := range value.Uses {
		ids[i], _ = strconv.ParseInt(use.ID, 10, 64)
	}
	approved, err := s.autoApproveSubmittedUsesTx(ctx, tx, donationID, ids, now)
	if err != nil {
		return empty, err
	}
	if approved {
		for i := range value.Uses {
			revision, _ := strconv.ParseInt(value.Uses[i].Revision, 10, 64)
			value.Uses[i].Revision = strconv.FormatInt(revision+1, 10)
		}
	}
	if !approved && s.pendingSubmission != nil {
		if err := s.pendingSubmission(ctx, tx, donationID); err != nil {
			return empty, err
		}
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusCreated, value)
	if err != nil {
		return empty, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return empty, err
	}
	return out, nil
}

func appendUseTx(ctx context.Context, tx *sql.Tx, donationKeyID int64, item AppendUseItem, grantID int64, sourceRevisionID *int64, now int64, wholeProtocol bool) (AppendedUse, error) {
	service, err := contract.ParseNativeServices([]contract.NativeService{item.NativeService})
	if err != nil {
		return AppendedUse{}, ErrInvalidRequest
	}
	var useID, revision int64
	var pending, approved sql.NullInt64
	var discoveryEnabled bool
	err = tx.QueryRowContext(ctx, "SELECT id,revision,current_pending_revision_id,current_approved_revision_id,discovery_enabled FROM donation_uses WHERE donation_key_id=? AND native_service_bit=?", donationKeyID, service.Mask()).Scan(&useID, &revision, &pending, &approved, &discoveryEnabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if item.ExpectedRevision != nil {
			return AppendedUse{}, ErrConflict
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO donation_uses(
donation_key_id,native_service_bit,enabled,discovery_enabled,revision,created_at,updated_at)
VALUES(?,?,CASE WHEN ? THEN COALESCE((SELECT MAX(enabled) FROM donation_uses WHERE donation_key_id=?),1) ELSE 1 END,
CASE WHEN ? THEN CASE WHEN ? THEN COALESCE((SELECT MAX(discovery_enabled) FROM donation_uses WHERE donation_key_id=?),1) ELSE 1 END ELSE 0 END,1,?,?)`, donationKeyID, service.Mask(), wholeProtocol, donationKeyID, sourceRevisionID != nil, wholeProtocol, donationKeyID, now, now)
		if err != nil {
			return AppendedUse{}, err
		}
		useID, err = result.LastInsertId()
		if err != nil || useID <= 0 {
			return AppendedUse{}, ErrInvariant
		}
		revision = 1
	case err != nil:
		return AppendedUse{}, err
	default:
		if item.ExpectedRevision == nil || revision != *item.ExpectedRevision || !wholeProtocol && (pending.Valid || approved.Valid) {
			return AppendedUse{}, ErrConflict
		}
		// Keep pause state. Sharing and the subsequent pending pointer each
		// require their own CAS increment under the existing database guard.
		if sharing := sourceRevisionID != nil; !wholeProtocol && discoveryEnabled != sharing {
			if revision >= math.MaxInt64-1 {
				return AppendedUse{}, ErrConflict
			}
			result, err := tx.ExecContext(ctx, "UPDATE donation_uses SET discovery_enabled=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?", sharing, now, useID, revision)
			if err != nil {
				return AppendedUse{}, err
			}
			if err := requireOne(result); err != nil {
				return AppendedUse{}, err
			}
			revision++
		}
	}
	staged, err := connectionstore.StagePendingUseRevisionTx(ctx, tx, connectionstore.StagePendingUseRevisionInput{
		UseID: useID, ExpectedRevision: revision, GrantRevisionID: grantID,
		DiscoverySourceRevisionID: sourceRevisionID, Origin: connectionstore.UseOriginServiceExpansion, At: now,
	})
	if err != nil {
		return AppendedUse{}, connectionError(err)
	}
	return AppendedUse{ID: strconv.FormatInt(useID, 10), Revision: strconv.FormatInt(staged.UseRevision, 10), NativeService: item.NativeService}, nil
}
