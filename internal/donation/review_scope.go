package donation

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
)

const reviewerTrainee reviewerRole = "trainee5"

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
	if err != nil {
		_ = tx.Rollback()
		return nil, role, 0, err
	}
	return tx, role, userID, nil
}

func pendingReviewFilter(filter ManagementFilter) (ManagementFilter, error) {
	if filter.Status != "" && filter.Status != "pending" || filter.Handling != "" && filter.Handling != "pending" {
		return filter, ErrForbidden
	}
	filter.Status = "pending"
	return filter, nil
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
	}
	return value
}
