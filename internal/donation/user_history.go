package donation

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// UserDonationHistory contains only safe donation facts. It deliberately has
// no credential material or controls, including for full-history readers.
type UserDonationHistory struct {
	AdminDonationSummary
	OwnerHiddenAt *int64 `json:"owner_hidden_at"`
}

func (s *Service) beginUserHistoryTx(ctx context.Context, role reviewerRole, actorID int64) (*sql.Tx, bool, error) {
	if role == reviewerAdmin {
		tx, err := s.beginBrowseTx(ctx, role, 0)
		return tx, true, err
	}
	if role != reviewerSteward {
		return nil, false, ErrInvalidRequest
	}
	tx, err := s.beginOwnerTx(ctx, actorID)
	if err != nil {
		return nil, false, err
	}
	permissions, err := authz.ReadStewardPermissions(ctx, tx, actorID)
	if err != nil {
		tx.Rollback()
		return nil, false, mapAuthorization(err)
	}
	if !permissions.Permissions["users.donations.read"] {
		tx.Rollback()
		return nil, false, authz.ErrPermissionDenied
	}
	return tx, permissions.Permissions["donations.read_all"], nil
}

func (s *Service) UserDonationHistoryPage(ctx context.Context, role reviewerRole, actorID, targetID int64, filter ManagementFilter, page pagination.Request) (Page[UserDonationHistory], error) {
	var empty Page[UserDonationHistory]
	if ctx == nil || targetID <= 0 || !page.Valid() || !validManagementFilter(filter) || filter.UserID != "" || filter.DiscordID != "" || filter.OwnerView != "" || filter.Handling != "" {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, full, err := s.beginUserHistoryTx(ctx, role, actorID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id=?`, targetID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return empty, ErrNotFound
	} else if err != nil {
		return empty, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	query := `SELECT d.id FROM donations d JOIN donation_handling h ON h.donation_id=d.id
WHERE d.user_id=? AND (d.status IN ('pending','approved') OR d.terminal_at>?)`
	query, args := appendManagementFilter(query, []any{targetID, now - terminalRetention}, filter, now)
	ids, metadata, err := browseIDs(ctx, tx, query, args, page)
	if err != nil {
		return empty, err
	}
	items, err := userDonationHistoryTx(ctx, tx, targetID, ids, now, full)
	if err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return Page[UserDonationHistory]{Data: items, Pagination: &metadata}, nil
}

func userDonationHistoryTx(ctx context.Context, tx *sql.Tx, targetID int64, ids []int64, now int64, full bool) ([]UserDonationHistory, error) {
	summaries, err := readUseSummariesTx(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	hidden, err := ownerHiddenPageTx(ctx, tx, targetID, ids)
	if err != nil {
		return nil, err
	}
	items := make([]UserDonationHistory, 0, len(ids))
	for _, id := range ids {
		header, err := logicalHeaderTx(ctx, tx, id, now)
		if err != nil {
			return nil, err
		}
		header.UseSummary = summaries[id]
		if !full {
			header = traineeReviewProjection(header)
		}
		summary, err := donationSummaryTx(ctx, tx, header, now)
		if err != nil {
			return nil, err
		}
		items = append(items, UserDonationHistory{AdminDonationSummary: AdminDonationSummary{
			DonationSummary: summary, Owner: header.Owner, Reviewer: header.Reviewer, Handling: header.Handling,
		}, OwnerHiddenAt: hidden[id]})
	}
	return items, nil
}

func (s *Service) UserDonationHistoryDetail(ctx context.Context, role reviewerRole, actorID, targetID, donationID int64) (UserDonationHistory, error) {
	if ctx == nil || targetID <= 0 || donationID <= 0 {
		return UserDonationHistory{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, full, err := s.beginUserHistoryTx(ctx, role, actorID)
	if err != nil {
		return UserDonationHistory{}, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return UserDonationHistory{}, err
	}
	var owned bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donations WHERE id=? AND user_id=? AND (status IN ('pending','approved') OR terminal_at>?))`, donationID, targetID, now-terminalRetention).Scan(&owned)
	if err != nil {
		return UserDonationHistory{}, err
	}
	if !owned {
		return UserDonationHistory{}, ErrNotFound
	}
	items, err := userDonationHistoryTx(ctx, tx, targetID, []int64{donationID}, now, full)
	if err != nil {
		return UserDonationHistory{}, err
	}
	if items[0].ID != strconv.FormatInt(donationID, 10) {
		return UserDonationHistory{}, ErrInvariant
	}
	return items[0], tx.Commit()
}
