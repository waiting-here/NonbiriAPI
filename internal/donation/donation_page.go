package donation

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/donationstate"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func donationSummaryTx(ctx context.Context, tx *sql.Tx, header AdminDonation, now int64, pendingOnly bool) (DonationSummary, error) {
	out := DonationSummary{UseSummary: header.UseSummary, DiscordPublicThanks: header.DiscordPublicThanks, ID: header.ID, Status: header.Status, Revision: header.Revision, Description: header.Description,
		ReviewResult: header.ReviewResult, CreatedAt: header.CreatedAt, UpdatedAt: header.UpdatedAt,
		StateCounts: map[string]string{}, Sources: []SafeSource{}}
	for _, state := range []string{"available", "rate_limited", "pending", "disabled", "suspended", "exhausted", "expired", "ended"} {
		out.StateCounts[state] = "0"
	}
	keyFilter := ""
	if pendingOnly {
		keyFilter = ` AND EXISTS(SELECT 1 FROM donation_uses pending_use WHERE pending_use.donation_key_id=dk.id AND pending_use.current_pending_revision_id IS NOT NULL)`
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+donationstate.SQL()+` AS state,COUNT(*)
FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id `+donationstate.PhysicalJoins+`
WHERE dk.donation_id=?`+keyFilter+` GROUP BY state`, now, now, now, now, header.ID)
	if err != nil {
		return out, err
	}
	var count int64
	for rows.Next() {
		var state string
		var number int64
		if err := rows.Scan(&state, &number); err != nil {
			rows.Close()
			return out, err
		}
		if _, ok := out.StateCounts[state]; !ok {
			rows.Close()
			return out, ErrInvariant
		}
		out.StateCounts[state] = strconv.FormatInt(number, 10)
		count += number
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.KeyCount = strconv.FormatInt(count, 10)
	grouped := `SELECT ` + sourceIdentitySQL + ` AS source_key,MAX(dk.id) AS preview FROM donation_keys dk WHERE dk.donation_id=?` + keyFilter + ` GROUP BY source_key`
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+grouped+`)`, header.ID).Scan(&count); err != nil {
		return out, err
	}
	out.SourceCount = strconv.FormatInt(count, 10)
	rows, err = tx.QueryContext(ctx, `SELECT preview FROM (`+grouped+`) ORDER BY source_key LIMIT 3`, header.ID)
	if err != nil {
		return out, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		source, err := sourcePreviewTx(ctx, tx, id)
		if err != nil {
			return out, err
		}
		out.Sources = append(out.Sources, source)
	}
	return out, nil
}

func (s *Service) donationsPage(ctx context.Context, role reviewerRole, userID int64, filter ManagementFilter, page pagination.Request) (Page[AdminDonationSummary], error) {
	var empty Page[AdminDonationSummary]
	if ctx == nil || !page.Valid() || !validManagementFilter(filter) || role == recurringOwner && (filter.Handling != "" || !validOwnerView(filter.OwnerView)) || role != recurringOwner && filter.OwnerView != "" {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var tx *sql.Tx
	var err error
	reviewRole := role
	if role == reviewerSteward {
		tx, reviewRole, _, err = s.beginReviewTx(ctx, role, userID, true)
	} else {
		tx, err = s.beginBrowseTx(ctx, role, userID)
	}
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if reviewRole == reviewerTrainee {
		filter, err = pendingReviewFilter(filter)
		if err != nil {
			return empty, err
		}
	}
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	query := `SELECT d.id AS id FROM donations d JOIN donation_handling h ON h.donation_id=d.id
WHERE (d.status IN ('pending','approved') OR d.terminal_at>?)`
	args := []any{now - terminalRetention}
	if reviewRole == reviewerTrainee {
		query += ` AND ` + pendingUseReviewSQL
		args = append(args, now)
	}
	if role == recurringOwner {
		query += ` AND d.user_id=?`
		args = append(args, userID)
		query, args = appendOwnerView(query, args, filter.OwnerView, now)
	}
	query, args = appendManagementFilter(query, args, filter, now)
	ids, metadata, err := browseIDs(ctx, tx, query, args, page)
	if err != nil {
		return empty, err
	}
	result := Page[AdminDonationSummary]{Data: make([]AdminDonationSummary, 0, len(ids)), Pagination: &metadata}
	summaries, err := readUseSummariesTx(ctx, tx, ids)
	if err != nil {
		return empty, err
	}
	var hidden map[int64]*int64
	if role == recurringOwner {
		hidden, err = ownerHiddenPageTx(ctx, tx, userID, ids)
		if err != nil {
			return empty, err
		}
	}
	for _, id := range ids {
		header, err := logicalHeaderTx(ctx, tx, id, now)
		if err != nil {
			return empty, err
		}
		header.UseSummary = summaries[id]
		if reviewRole == reviewerTrainee {
			header = traineeReviewProjection(header)
		}
		summary, err := donationSummaryTx(ctx, tx, header, now, reviewRole == reviewerTrainee)
		if err != nil {
			return empty, err
		}
		if role == recurringOwner {
			summary.OwnerHiddenAt = hidden[id]
		}
		result.Data = append(result.Data, AdminDonationSummary{DonationSummary: summary, Owner: header.Owner, Reviewer: header.Reviewer, Handling: header.Handling})
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

func (s *Service) DonationsOwnerPage(ctx context.Context, userID int64, filter ManagementFilter, page pagination.Request) (Page[OwnerDonationSummary], error) {
	result, err := s.donationsPage(ctx, recurringOwner, userID, filter, page)
	if err != nil {
		return Page[OwnerDonationSummary]{}, err
	}
	out := Page[OwnerDonationSummary]{Data: make([]OwnerDonationSummary, 0, len(result.Data)), Pagination: result.Pagination}
	for _, item := range result.Data {
		out.Data = append(out.Data, OwnerDonationSummary{DonationSummary: item.DonationSummary, OwnerHiddenAt: item.OwnerHiddenAt})
	}
	return out, nil
}

func (s *Service) DonationsAdminPage(ctx context.Context, filter ManagementFilter, page pagination.Request) (Page[AdminDonationSummary], error) {
	return s.donationsPage(ctx, reviewerAdmin, 0, filter, page)
}

func (s *Service) DonationsStewardPage(ctx context.Context, userID int64, filter ManagementFilter, page pagination.Request) (Page[StewardDonationSummary], error) {
	result, err := s.donationsPage(ctx, reviewerSteward, userID, filter, page)
	if err != nil {
		return Page[StewardDonationSummary]{}, err
	}
	out := Page[StewardDonationSummary]{Data: make([]StewardDonationSummary, 0, len(result.Data)), Pagination: result.Pagination}
	for _, item := range result.Data {
		visible := stewardFromAdmin(AdminDonation{Owner: item.Owner, Reviewer: item.Reviewer}, userID)
		out.Data = append(out.Data, StewardDonationSummary{DonationSummary: item.DonationSummary, Owner: visible.Owner, Reviewer: visible.Reviewer, Handling: item.Handling})
	}
	return out, nil
}
