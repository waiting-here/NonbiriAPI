package donation

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
)

const reviewerTrainee reviewerRole = "trainee5"
const reviewerGranted reviewerRole = "level5"

// Review authority is independent of the model-scoped maintenance capability.
func (s *Service) beginReviewTx(ctx context.Context, role reviewerRole, userID int64, readOnly bool) (*sql.Tx, reviewerRole, int64, error) {
	if role == reviewerAdmin {
		tx, id, err := s.beginRoleTx(ctx, role, userID)
		return tx, role, id, err
	}
	if s == nil || s.db == nil || ctx == nil || nilDependency(s.roleAuth) {
		return nil, role, 0, ErrUnavailable
	}
	if role != reviewerSteward || userID <= 0 {
		return nil, role, 0, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
	if err != nil {
		return nil, role, 0, err
	}
	var level, admin int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(level,auto_level),is_admin FROM users WHERE id=?`, userID).Scan(&level, &admin)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrUnauthorized
	}
	if err == nil {
		switch {
		case admin != 0:
			err = ErrForbidden
		case level == 6:
			err = mapAuthorization(s.roleAuth.AuthorizeStewardMutation(ctx, tx, userID))
		case level == 5:
			role = reviewerTrainee
			authority, ok := s.roleAuth.(interface {
				AuthorizeTraineeMutation(context.Context, *sql.Tx, int64) error
			})
			if !ok {
				err = ErrForbidden
			} else {
				err = mapAuthorization(authority.AuthorizeTraineeMutation(ctx, tx, userID))
				if err == nil {
					_, err = charityscope.SessionIdentity(ctx, s.roleAuth, userID)
					err = mapAuthorization(err)
				}
			}
		default:
			err = ErrForbidden
		}
	}
	if err == nil {
		permission := authz.StewardPermissionFromContext(ctx)
		if permission == "" {
			permission = "donations.review"
		}
		err = mapAuthorization(authz.RequireStewardPermission(ctx, tx, userID, permission))
		if err == nil && level == 5 {
			p, e := authz.ReadStewardPermissions(ctx, tx, userID)
			err = e
			if err == nil && p.Permissions["donations.read_all"] {
				role = reviewerGranted
			}
		}
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, role, 0, err
	}
	return tx, role, userID, nil
}

func pendingReviewFilter(filter ManagementFilter) (ManagementFilter, error) {
	if filter.Status != "" && filter.Status != "pending" || filter.ReviewStatus != "" && filter.ReviewStatus != "pending" && filter.ReviewStatus != "approved_pending" || filter.Handling != "" && filter.Handling != "pending" {
		return filter, authz.ErrPermissionDenied
	}
	filter.Status = ""
	return filter, nil
}

const pendingUseReviewSQL = `d.status IN ('pending','approved') AND d.terminal_at IS NULL AND EXISTS(
SELECT 1 FROM donation_keys review_key JOIN donation_uses review_use ON review_use.donation_key_id=review_key.id
WHERE review_key.donation_id=d.id AND review_key.ended_at IS NULL AND (review_key.expires_at IS NULL OR review_key.expires_at>?)
AND review_use.current_pending_revision_id IS NOT NULL)`

func requirePendingUseReviewTx(ctx context.Context, tx *sql.Tx, donationID, now int64) error {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT `+pendingUseReviewSQL+` FROM donations d WHERE d.id=?`, now, donationID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !active {
		return ErrForbidden
	}
	return err
}

func pendingReviewKeysTx(ctx context.Context, tx *sql.Tx, donationIDs []int64) (map[string]bool, error) {
	keys := make(map[string]bool)
	if len(donationIDs) == 0 {
		return keys, nil
	}
	if len(donationIDs) > 100 {
		return nil, ErrInvalidRequest
	}
	args := make([]any, len(donationIDs))
	for i, id := range donationIDs {
		args[i] = id
	}
	rows, err := tx.QueryContext(ctx, `SELECT dk.id FROM donation_keys dk WHERE dk.donation_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`) AND EXISTS(SELECT 1 FROM donation_uses u WHERE u.donation_key_id=dk.id AND u.current_pending_revision_id IS NOT NULL)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		keys[strconv.FormatInt(id, 10)] = true
	}
	return keys, rows.Err()
}

func pendingUseReviewProjection(value AdminDonation, keys map[string]bool) AdminDonation {
	value = traineeReviewProjection(value)
	kept := value.Keys[:0]
	for _, key := range value.Keys {
		if keys[key.ID] {
			kept = append(kept, key)
		}
	}
	value.Keys = kept
	return value
}

func requirePendingReviewTx(ctx context.Context, tx *sql.Tx, donationID, now int64) error {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT d.status='pending' AND `+logicallyActiveDonationSQL+` FROM donations d WHERE d.id=?`, now, donationID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !active {
		return ErrForbidden
	}
	return err
}

func traineeReviewProjection(value AdminDonation) AdminDonation {
	value.Owner, value.Reviewer = nil, nil
	value.DiscordPublicThanks = nil
	value.CanForceReject = false
	value.ForceRejectUnavailableReason = nil
	for i := range value.Keys {
		value.Keys[i].EndpointKeyID = nil
		value.Keys[i].CanManage = false
	}
	return value
}
