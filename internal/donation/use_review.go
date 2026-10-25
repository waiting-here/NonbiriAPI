package donation

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func legacyUseAllowed(kind string, bit int64) bool {
	allowed, err := contract.LegacyNativeServices(contract.Type(kind))
	if err != nil {
		return false
	}
	selected, err := contract.NativeServicesFromMask(bit)
	return err == nil && len(selected.Services()) == 1 && selected.IsSubsetOf(allowed)
}

func (s *Service) reauthorizeUseReviewTx(ctx context.Context, tx *sql.Tx, access useAccess) error {
	switch access.role {
	case reviewerAdmin:
		return mapAuthorization(s.roleAuth.AuthorizeAdminMutation(ctx, tx, access.actorID))
	case reviewerSteward, reviewerGranted:
		return mapAuthorization(s.roleAuth.AuthorizeStewardMutation(ctx, tx, access.actorID))
	case reviewerTrainee:
		authority, ok := s.roleAuth.(interface {
			AuthorizeTraineeMutation(context.Context, *sql.Tx, int64) error
		})
		if !ok {
			return ErrForbidden
		}
		if err := authority.AuthorizeTraineeMutation(ctx, tx, access.actorID); err != nil {
			return mapAuthorization(err)
		}
		_, err := charityscope.SessionIdentity(ctx, s.roleAuth, access.actorID)
		return mapAuthorization(err)
	default:
		return ErrForbidden
	}
}

func (s *Service) reviewUseTx(ctx context.Context, tx *sql.Tx, access useAccess, donationID int64, item UseReviewItem, now int64) error {
	if err := s.reauthorizeUseReviewTx(ctx, tx, access); err != nil {
		return err
	}
	if access.role != reviewerAdmin {
		if err := authz.RequireStewardPermission(ctx, tx, access.actorID, "donations.review"); err != nil {
			return err
		}
		rangeScope, err := readDonationAccessScope(ctx, tx, access.role, access.actorID)
		if err != nil {
			return err
		}
		_, err = rangeScope.requireTx(ctx, tx, donationID, now)
		if err != nil {
			return err
		}
	}
	if err := requireUseAccessTx(ctx, tx, access, donationID, item.UseID, now, true); err != nil {
		return err
	}
	var revision int64
	var pending, previousVersion sql.NullInt64
	var eligible bool
	err := tx.QueryRowContext(ctx, `SELECT u.revision,u.current_pending_revision_id,oldg.credential_version_id,
COALESCE(a.relationship_valid=1 AND a.available=1 AND (a.effective_native_service_mask&u.native_service_bit)=u.native_service_bit,0)
FROM donation_uses u LEFT JOIN donation_use_revisions p ON p.id=u.current_pending_revision_id AND p.use_id=u.id
LEFT JOIN endpoint_grant_authorizations a ON a.grant_revision_id=p.grant_revision_id
LEFT JOIN donation_use_revisions old ON old.id=u.current_approved_revision_id LEFT JOIN endpoint_key_grant_revisions oldg ON oldg.id=old.grant_revision_id WHERE u.id=?`, item.UseID).Scan(&revision, &pending, &previousVersion, &eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if revision != item.ExpectedRevision || revision == math.MaxInt64 || !pending.Valid {
		return ErrConflict
	}
	if item.Decision == "approve" {
		if !eligible {
			return ErrResourceLocked
		}
		if err := s.reviewMaterial.ApproveUseRevision(ctx, tx, pending.Int64, item.ExpectedReviewRevision, time.Unix(now, 0)); err != nil {
			return mapReviewError(err)
		}
	}
	state := "rejected"
	if item.Decision == "approve" {
		state = "approved"
	}
	result, err := tx.ExecContext(ctx, `UPDATE donation_use_revisions SET review_state=?,review_reason=?,reviewer_user_id=?,reviewer_role=?,review_requirement_revision=?,reviewed_at=? WHERE id=? AND use_id=? AND review_state='pending'`, state, item.ReviewNote, access.actorID, string(access.role), item.ExpectedReviewRevision, now, pending.Int64, item.UseID)
	if err != nil {
		return err
	}
	if err := requireOne(result); err != nil {
		return err
	}
	if item.Decision == "approve" {
		result, err = tx.ExecContext(ctx, `UPDATE donation_uses SET current_approved_revision_id=?,current_pending_revision_id=NULL,revision=revision+1,updated_at=? WHERE id=? AND revision=? AND current_pending_revision_id=?`, pending.Int64, now, item.UseID, revision, pending.Int64)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE donation_uses SET current_pending_revision_id=NULL,revision=revision+1,updated_at=? WHERE id=? AND revision=? AND current_pending_revision_id=?`, now, item.UseID, revision, pending.Int64)
	}
	if err != nil {
		return err
	}
	if err := requireOne(result); err != nil {
		return err
	}
	if item.Decision == "approve" && previousVersion.Valid {
		if _, err := connectionstore.RetireUnreferencedVersionTx(ctx, tx, previousVersion.Int64, now); err != nil {
			return err
		}
	}
	if item.Decision == "reject" {
		return retireUseRevisionVersionTx(ctx, tx, pending.Int64, now)
	}
	return nil
}

func retireUseRevisionVersionTx(ctx context.Context, tx *sql.Tx, revisionID, now int64) error {
	var version sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT g.credential_version_id FROM donation_use_revisions r LEFT JOIN endpoint_key_grant_revisions g ON g.id=r.grant_revision_id WHERE r.id=?`, revisionID).Scan(&version)
	if err != nil {
		return err
	}
	if !version.Valid {
		return nil
	}
	_, err = connectionstore.RetireUnreferencedVersionTx(ctx, tx, version.Int64, now)
	return err
}

func useReviewError(err error) (*UseReviewError, bool) {
	switch {
	case errors.Is(err, ErrNotFound):
		return &UseReviewError{string(httperr.CodeNotFound), "not found"}, true
	case errors.Is(err, ErrForbidden):
		return &UseReviewError{string(httperr.CodeForbidden), "access denied"}, true
	case errors.Is(err, ErrConflict):
		return &UseReviewError{string(httperr.CodeConflict), "request conflicts with current state"}, true
	case errors.Is(err, ErrReviewMaterialUnavailable):
		return &UseReviewError{string(httperr.CodeConflict), "donation review material is unavailable; restore the original material before reviewing"}, true
	case errors.Is(err, ErrResourceLocked):
		return &UseReviewError{string(httperr.CodeResourceLocked), "resource is locked"}, true
	default:
		return nil, false
	}
}

func (s *Service) reviewUses(ctx context.Context, role reviewerRole, actorID, donationID int64, items []UseReviewItem, mutation resources.ControlMutation) (resources.MutationResult[UseReviewReceipt], error) {
	var empty resources.MutationResult[UseReviewReceipt]
	route := routeAdminUses + "/review"
	if role == reviewerSteward {
		route = routeStewardUses + "/review"
	}
	if ctx == nil || donationID <= 0 || len(items) == 0 || len(items) > 100 || !validMutation(mutation, http.MethodPost, route, donationID) {
		return empty, ErrInvalidRequest
	}
	seen := make(map[int64]bool, len(items))
	for _, item := range items {
		if item.UseID <= 0 || item.ExpectedRevision <= 0 || seen[item.UseID] || item.ExpectedReviewRevision != nil && *item.ExpectedReviewRevision <= 0 || (item.Decision != "approve" && item.Decision != "reject") || !validReviewReason(item.ReviewNote) {
			return empty, ErrInvalidRequest
		}
		seen[item.UseID] = true
	}
	tx, actualRole, actorID, err := s.beginReviewTx(ctx, role, actorID, false)
	if err != nil {
		return empty, err
	}
	committed := false
	defer finishTx(tx, &committed)
	access := useAccess{role: actualRole, actorID: actorID}
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	if err := requireManagedDonationTx(ctx, tx, actualRole, donationID, now); err != nil {
		return empty, err
	}
	decision, err := beginMutation(ctx, tx, string(actualRole), actorID, idempotency.ScopeControlMutation, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		if _, err := readDonationAccessScope(ctx, tx, actualRole, actorID); err != nil {
			return empty, err
		}
		return replay[UseReviewReceipt](decision)
	}
	if err := requireDonationRangeTx(ctx, tx, actualRole, actorID, donationID, now); err != nil {
		return empty, err
	}
	receipt := UseReviewReceipt{Results: make([]UseReviewResult, 0, len(items))}
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `SAVEPOINT use_review_item`); err != nil {
			return empty, err
		}
		itemErr := s.reviewUseTx(ctx, tx, access, donationID, item, now)
		result := UseReviewResult{UseID: strconv.FormatInt(item.UseID, 10), OK: itemErr == nil}
		if itemErr != nil {
			if _, err := tx.ExecContext(ctx, `ROLLBACK TO use_review_item`); err != nil {
				return empty, err
			}
			var safe bool
			result.Error, safe = useReviewError(itemErr)
			if !safe {
				return empty, itemErr
			}
		}
		if _, err := tx.ExecContext(ctx, `RELEASE use_review_item`); err != nil {
			return empty, err
		}
		// The bounded receipt records the decision; callers refresh the paginated
		// material view rather than embedding up to three snapshots per item.
		receipt.Results = append(receipt.Results, result)
	}
	result, err := finishJSON(ctx, tx, decision, http.StatusOK, receipt)
	if err != nil {
		return empty, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return empty, err
	}
	return result, nil
}

// The legacy whole-donation command can approve only its original service
// permissions and initial/legacy payloads. Rotation and expansion use reviews
// must name the stable use and its revision explicitly.
func (s *Service) approveInitialUsesTx(ctx context.Context, tx *sql.Tx, donationID, actorID int64, role string, settings []KeySetting, note string, now int64) error {
	for _, setting := range settings {
		if !setting.Enabled {
			continue
		}
		rows, err := tx.QueryContext(ctx, `SELECT u.id,u.revision,p.origin,u.native_service_bit,dk.connector_type
FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donation_use_revisions p ON p.id=u.current_pending_revision_id WHERE dk.donation_id=? AND dk.id=? ORDER BY u.native_service_bit`, donationID, setting.DonationKeyID)
		if err != nil {
			return err
		}
		items := []UseReviewItem{}
		for rows.Next() {
			var item UseReviewItem
			var origin, kind string
			var bit int64
			if err := rows.Scan(&item.UseID, &item.ExpectedRevision, &origin, &bit, &kind); err != nil {
				rows.Close()
				return err
			}
			if origin != "initial" && origin != "legacy" {
				rows.Close()
				return ErrConflict
			}
			if !legacyUseAllowed(kind, bit) {
				rows.Close()
				return ErrConflict
			}
			item.ExpectedReviewRevision, item.Decision, item.ReviewNote = setting.ExpectedReviewRevision, "approve", note
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, item := range items {
			if err := s.reviewUseTx(ctx, tx, useAccess{role: reviewerRole(role), actorID: actorID}, donationID, item, now); err != nil {
				return err
			}
		}
	}
	return nil
}
