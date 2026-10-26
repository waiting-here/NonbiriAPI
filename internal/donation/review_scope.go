package donation

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
)

const reviewerTrainee reviewerRole = "trainee5"
const reviewerGranted reviewerRole = "level5"

// Review actions use the same live donation scope as every other steward action.
func (s *Service) beginReviewTx(ctx context.Context, role reviewerRole, userID int64, readOnly bool) (*sql.Tx, reviewerRole, int64, error) {
	if role == reviewerAdmin {
		tx, id, err := s.beginRoleTx(ctx, role, userID)
		return tx, role, id, err
	}
	if s == nil {
		return nil, role, 0, ErrUnavailable
	}
	return s.beginStewardReviewTx(ctx, s.db, role, userID, readOnly)
}

func (s *Service) beginStewardReviewTx(ctx context.Context, database *sql.DB, role reviewerRole, userID int64, readOnly bool) (*sql.Tx, reviewerRole, int64, error) {
	if s == nil || database == nil || ctx == nil || nilDependency(s.roleAuth) {
		return nil, role, 0, ErrUnavailable
	}
	if role != reviewerSteward || userID <= 0 {
		return nil, role, 0, ErrInvalidRequest
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
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

	}
	if err != nil {
		_ = tx.Rollback()
		return nil, role, 0, err
	}
	return tx, role, userID, nil
}

func traineeReviewProjection(value AdminDonation) AdminDonation {
	value.Owner, value.Reviewer = nil, nil
	value.DiscordPublicThanks = nil
	value.CanForceReject = false
	value.ForceRejectUnavailableReason = nil
	for i := range value.Keys {
		value.Keys[i].EndpointKeyID = nil
		value.Keys[i].OwnerEndpointID = nil
		value.Keys[i].CanManage = false
	}
	return value
}
