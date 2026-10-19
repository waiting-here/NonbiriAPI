package donation

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/donationstate"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func donationSummaryTx(ctx context.Context, tx *sql.Tx, header AdminDonation, now int64) (DonationSummary, error) {
	out := DonationSummary{DiscordPublicThanks: header.DiscordPublicThanks, ID: header.ID, Status: header.Status, Revision: header.Revision, Description: header.Description,
		ReviewResult: header.ReviewResult, CreatedAt: header.CreatedAt, UpdatedAt: header.UpdatedAt,
		StateCounts: map[string]string{}, Sources: []SafeSource{}}
	for _, state := range []string{"available", "rate_limited", "pending", "disabled", "suspended", "exhausted", "expired", "ended"} {
		out.StateCounts[state] = "0"
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+donationstate.SQL()+` AS state,COUNT(*)
FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id `+donationstate.PhysicalJoins+`
WHERE dk.donation_id=? GROUP BY state`, now, now, now, now, header.ID)
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
	grouped := `SELECT ` + sourceIdentitySQL + ` AS source_key,MAX(dk.id) AS preview FROM donation_keys dk WHERE dk.donation_id=? GROUP BY source_key`
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
	if ctx == nil || !page.Valid() || !validManagementFilter(filter) || role == recurringOwner && filter.Handling != "" {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginBrowseTx(ctx, role, userID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	query := `SELECT d.id AS id FROM donations d JOIN donation_handling h ON h.donation_id=d.id
WHERE (d.status IN ('pending','approved') OR d.terminal_at>?)`
	args := []any{now - terminalRetention}
	if role == recurringOwner {
		query += ` AND d.user_id=?`
		args = append(args, userID)
	}
	query, args = appendManagementFilter(query, args, filter, now)
	ids, metadata, err := browseIDs(ctx, tx, query, args, page)
	if err != nil {
		return empty, err
	}
	result := Page[AdminDonationSummary]{Data: make([]AdminDonationSummary, 0, len(ids)), Pagination: &metadata}
	for _, id := range ids {
		header, err := logicalHeaderTx(ctx, tx, id, now)
		if err != nil {
			return empty, err
		}
		summary, err := donationSummaryTx(ctx, tx, header, now)
		if err != nil {
			return empty, err
		}
		result.Data = append(result.Data, AdminDonationSummary{DonationSummary: summary, Owner: header.Owner, Reviewer: header.Reviewer, Handling: header.Handling})
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

func (s *Service) DonationsOwnerPage(ctx context.Context, userID int64, filter ManagementFilter, page pagination.Request) (Page[DonationSummary], error) {
	result, err := s.donationsPage(ctx, recurringOwner, userID, filter, page)
	if err != nil {
		return Page[DonationSummary]{}, err
	}
	out := Page[DonationSummary]{Data: make([]DonationSummary, 0, len(result.Data)), Pagination: result.Pagination}
	for _, item := range result.Data {
		out.Data = append(out.Data, item.DonationSummary)
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
