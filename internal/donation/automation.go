package donation

import (
	"context"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

// ApproveOwnNewInTransaction approves the caller's newly created submission
// without committing any part of the encompassing resource transaction.
func (s *Service) ApproveOwnNewInTransaction(ctx context.Context, tx *sql.Tx, userID, donationID int64, input ReviewInput) error {
	if s == nil || tx == nil || userID <= 0 || donationID <= 0 || input.Decision != "approve" || input.ExpectedRevision != 1 || !validReviewInput(input) {
		return ErrInvalidRequest
	}
	if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, userID); err != nil {
		return mapAuthorization(err)
	}
	if err := authz.RequireStewardPermission(ctx, tx, userID, "donations.review"); err != nil {
		return err
	}
	var owned bool
	if err := tx.QueryRowContext(ctx, `SELECT user_id=? AND status='pending' AND revision=1 FROM donations WHERE id=?`, userID, donationID).Scan(&owned); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if !owned {
		return ErrNotFound
	}
	now, err := s.nowUnix()
	if err != nil {
		return err
	}
	if err := requireManagedDonationTx(ctx, tx, reviewerSteward, donationID, now); err != nil {
		return err
	}
	var level int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(level,auto_level) FROM users WHERE id=?`, userID).Scan(&level); err != nil {
		return err
	}
	role := reviewerSteward
	if level == 5 {
		role = reviewerGranted
	}
	if err := requireDonationRangeTx(ctx, tx, role, userID, donationID, now); err != nil {
		return err
	}
	return s.reviewDonationTx(ctx, tx, donationID, userID, string(role), input, now)
}
