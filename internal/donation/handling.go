package donation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	routeAdminBadge       = "/admin/api/donations/badge"
	routeStewardBadge     = "/api/steward/donations/badge"
	routeAdminProcessed   = "/admin/api/donations/{id}/handling/processed"
	routeStewardProcessed = "/api/steward/donations/{id}/handling/processed"
)

// A disabled or unbound key does not end a donation. Logical expiry is checked
// in the query so a cleanup backlog never adds expired donations to the badge.
const logicallyActiveDonationSQL = `(d.status IN ('pending','approved') AND EXISTS(
SELECT 1 FROM donation_keys live WHERE live.donation_id=d.id AND live.ended_at IS NULL
 AND (live.expires_at IS NULL OR live.expires_at>?)))`

type ManagementFilter struct{ Status, Handling, Query string }

func validManagementFilter(filter ManagementFilter) bool {
	return validStatusFilter(filter.Status) && validHandlingFilter(filter.Handling) &&
		utf8.ValidString(filter.Query) && utf8.RuneCountInString(filter.Query) <= 128 &&
		len(filter.Query) <= 512 && !strings.ContainsRune(filter.Query, 0)
}

func validHandlingFilter(value string) bool {
	switch value {
	case "", "legacy", "pending", "processed", "closed":
		return true
	}
	return false
}

func appendManagementFilter(query string, args []any, filter ManagementFilter, now int64) (string, []any) {
	if filter.Status != "" {
		query += ` AND (CASE WHEN d.status IN ('pending','approved') AND NOT ` + logicallyActiveDonationSQL + ` THEN 'expired' ELSE d.status END)=?`
		args = append(args, now, filter.Status)
	}
	if filter.Handling != "" {
		query += ` AND (CASE WHEN h.state='pending' AND NOT ` + logicallyActiveDonationSQL + ` THEN 'closed' ELSE h.state END)=?`
		args = append(args, now, filter.Handling)
	}
	if filter.Query != "" {
		query += ` AND (instr(lower(d.description),lower(?))>0 OR instr(CAST(d.id AS TEXT),?)>0)`
		args = append(args, filter.Query, filter.Query)
	}
	return query, args
}

func readHandlingTx(ctx context.Context, tx *sql.Tx, donationID int64) (DonationHandling, error) {
	var out DonationHandling
	var revision int64
	var role, reason string
	err := tx.QueryRowContext(ctx, `SELECT state,revision,processed_at,processed_by_role,closed_at,closed_reason
FROM donation_handling WHERE donation_id=?`, donationID).Scan(&out.State, &revision, &out.ProcessedAt, &role, &out.ClosedAt, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrInvariant
	}
	if err != nil {
		return out, fmt.Errorf("donation: read handling: %w", err)
	}
	out.Revision = strconv.FormatInt(revision, 10)
	if role != "" {
		if role == string(reviewerSteward) {
			role = "steward"
		}
		out.ProcessedByRole = &role
	}
	if reason != "" {
		out.ClosedReason = &reason
	}
	return out, nil
}

func closePendingHandlingTx(ctx context.Context, tx *sql.Tx, donationID, now int64, reason string) error {
	switch reason {
	case "rejected", "withdrawn", "terminated", "expired", "member_removed", "account_deleted":
	default:
		return ErrInvariant
	}
	_, err := tx.ExecContext(ctx, `UPDATE donation_handling SET state='closed',revision=revision+1,
closed_at=?,closed_reason=?,updated_at=? WHERE donation_id=? AND state='pending'
 AND EXISTS(SELECT 1 FROM donations d WHERE d.id=donation_id AND d.status IN ('rejected','deleted','expired'))`, now, reason, now, donationID)
	if err != nil {
		return fmt.Errorf("donation: close pending handling: %w", err)
	}
	return nil
}

func (s *Service) BadgeAdmin(ctx context.Context) (DonationBadge, error) {
	return s.badge(ctx, reviewerAdmin, 0)
}
func (s *Service) BadgeSteward(ctx context.Context, userID int64) (DonationBadge, error) {
	return s.badge(ctx, reviewerSteward, userID)
}

func (s *Service) badge(ctx context.Context, role reviewerRole, userID int64) (DonationBadge, error) {
	if ctx == nil {
		return DonationBadge{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, _, err := s.beginRoleTx(ctx, role, userID)
	if err != nil {
		return DonationBadge{}, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return DonationBadge{}, err
	}
	var count int64
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donation_handling h JOIN donations d ON d.id=h.donation_id
WHERE h.state='pending' AND `+logicallyActiveDonationSQL, now).Scan(&count)
	if err != nil {
		return DonationBadge{}, fmt.Errorf("donation: count pending handling: %w", err)
	}
	return DonationBadge{PendingCount: strconv.FormatInt(count, 10), ServerNow: now}, nil
}

func (s *Service) ProcessAdmin(ctx context.Context, donationID int64, mutation resources.ControlMutation, expectedRevision int64) (resources.MutationResult[HandlingReceipt], error) {
	if !validMutation(mutation, http.MethodPost, routeAdminProcessed, donationID) {
		return resources.MutationResult[HandlingReceipt]{}, ErrInvalidRequest
	}
	return s.process(ctx, reviewerAdmin, 0, donationID, mutation, expectedRevision)
}

func (s *Service) ProcessSteward(ctx context.Context, userID, donationID int64, mutation resources.ControlMutation, expectedRevision int64) (resources.MutationResult[HandlingReceipt], error) {
	if !validMutation(mutation, http.MethodPost, routeStewardProcessed, donationID) {
		return resources.MutationResult[HandlingReceipt]{}, ErrInvalidRequest
	}
	return s.process(ctx, reviewerSteward, userID, donationID, mutation, expectedRevision)
}

func (s *Service) process(ctx context.Context, role reviewerRole, userID, donationID int64, mutation resources.ControlMutation, expectedRevision int64) (resources.MutationResult[HandlingReceipt], error) {
	var empty resources.MutationResult[HandlingReceipt]
	if ctx == nil || donationID <= 0 || expectedRevision <= 0 {
		return empty, ErrInvalidRequest
	}
	tx, actorID, err := s.beginRoleTx(ctx, role, userID)
	if err != nil {
		return empty, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	if err := requireManagedDonationTx(ctx, tx, role, donationID, now); err != nil {
		return empty, err
	}
	decision, err := beginMutation(ctx, tx, string(role), actorID, idempotency.ScopeControlMutation, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[HandlingReceipt](decision)
	}
	expiry, err := materializeDonationExpiryStateTx(ctx, tx, donationID, now)
	if err != nil {
		return empty, err
	}
	if expiry.terminal {
		if err := tx.Rollback(); err != nil {
			return empty, err
		}
		committed = true
		if err := s.materializeRoleExpiryStandalone(ctx, role, actorID, donationID, now); err != nil {
			return empty, err
		}
		return empty, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE donation_handling SET state='processed',revision=revision+1,
processed_at=?,processed_by_user_id=?,processed_by_role=?,updated_at=?
WHERE donation_id=? AND state='pending' AND revision=? AND revision<9223372036854775807
 AND EXISTS(SELECT 1 FROM donations d WHERE d.id=donation_id AND `+logicallyActiveDonationSQL+`)`, now, actorID, string(role), now, donationID, expectedRevision, now)
	if err != nil {
		return empty, fmt.Errorf("donation: process handling: %w", err)
	}
	if err := requireOne(result); err != nil {
		return empty, err
	}
	handling, err := readHandlingTx(ctx, tx, donationID)
	if err != nil {
		return empty, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, HandlingReceipt{DonationID: strconv.FormatInt(donationID, 10), Handling: handling})
	if err != nil {
		return empty, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return empty, err
	}
	return out, nil
}

// Management authority is global for both roles. Steward mutations remain
// limited to ordinary history; held-only read permission does not widen writes.
// Actor authentication must precede this object check.
func requireManagedDonationTx(ctx context.Context, tx *sql.Tx, role reviewerRole, donationID, now int64) error {
	var exists int
	query := `SELECT 1 FROM donations WHERE id=?`
	args := []any{donationID}
	if role == reviewerSteward {
		query += ` AND (status IN ('pending','approved') OR terminal_at>?)`
		args = append(args, now-terminalRetention)
	}
	err := tx.QueryRowContext(ctx, query, args...).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("donation: verify managed donation: %w", err)
	}
	return nil
}
