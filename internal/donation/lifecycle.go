package donation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

var _ resources.EndpointKeyDeletionHook = (*Service)(nil)

// PrepareEndpointKeyDeletion runs inside the resources caller transaction.
// It performs no authentication, commit, or physical key deletion.
func (s *Service) PrepareEndpointKeyDeletion(
	ctx context.Context,
	tx *sql.Tx,
	ownerUserID int64,
	keyIDs []int64,
	decisionNow int64,
) error {
	if s == nil || ctx == nil || tx == nil || ownerUserID <= 0 || !validUniqueIDs(keyIDs, 1, maxEndpointDeleteKeys) ||
		decisionNow < 0 || decisionNow > maxUnixSecond {
		return ErrInvalidRequest
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(keyIDs)), ",")
	args := make([]any, 0, len(keyIDs)+1)
	args = append(args, ownerUserID)
	for _, id := range keyIDs {
		args = append(args, id)
	}
	var owned int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM endpoint_keys k
JOIN endpoints e ON e.id=k.endpoint_id WHERE e.user_id=? AND k.id IN (`+placeholders+`)`, args...).Scan(&owned); err != nil {
		return fmt.Errorf("donation: verify deletion key ownership: %w", err)
	}
	if owned != len(keyIDs) {
		return ErrNotFound
	}

	lookupArgs := make([]any, len(keyIDs))
	for index, id := range keyIDs {
		lookupArgs[index] = id
	}
	rows, err := tx.QueryContext(ctx, `SELECT donation_id,donation_key_id,endpoint_key_id
FROM donation_key_memberships WHERE endpoint_key_id IN (`+placeholders+`) ORDER BY donation_id,donation_key_id`, lookupArgs...)
	if err != nil {
		return fmt.Errorf("donation: read deletion memberships: %w", err)
	}
	type member struct{ donationID, donationKeyID, endpointKeyID int64 }
	members := make([]member, 0)
	for rows.Next() {
		var value member
		if err := rows.Scan(&value.donationID, &value.donationKeyID, &value.endpointKeyID); err != nil {
			rows.Close()
			return fmt.Errorf("donation: scan deletion membership: %w", err)
		}
		members = append(members, value)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("donation: close deletion memberships: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("donation: iterate deletion memberships: %w", err)
	}
	if len(members) == 0 {
		return nil
	}

	byDonation := make(map[int64][]member)
	ids := make([]int64, 0)
	for _, value := range members {
		if _, exists := byDonation[value.donationID]; !exists {
			ids = append(ids, value.donationID)
		}
		byDonation[value.donationID] = append(byDonation[value.donationID], value)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, donationID := range ids {
		group := byDonation[donationID]
		donationKeyIDs := make([]int64, len(group))
		endpointKeyIDs := make([]int64, len(group))
		for index := range group {
			donationKeyIDs[index] = group[index].donationKeyID
			endpointKeyIDs[index] = group[index].endpointKeyID
		}
		if err := detachBindingsTx(ctx, tx, donationKeyIDs, decisionNow); err != nil {
			return err
		}
		if err := deleteMembershipsTx(ctx, tx, endpointKeyIDs); err != nil {
			return err
		}
		if err := endDonationKeysTx(ctx, tx, donationKeyIDs, "member_removed", decisionNow); err != nil {
			return err
		}
		var remaining int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donation_key_memberships WHERE donation_id=?`, donationID).Scan(&remaining); err != nil {
			return fmt.Errorf("donation: count remaining deletion memberships: %w", err)
		}
		statusExpr := "status"
		terminalExpr := "terminal_at"
		if remaining == 0 {
			statusExpr = "'deleted'"
			terminalExpr = "?"
		}
		query := `UPDATE donations SET status=` + statusExpr + `,revision=revision+1,updated_at=?,terminal_at=` + terminalExpr + `
WHERE id=? AND status IN ('pending','approved')`
		queryArgs := []any{decisionNow}
		if remaining == 0 {
			queryArgs = append(queryArgs, decisionNow)
		}
		queryArgs = append(queryArgs, donationID)
		result, err := tx.ExecContext(ctx, query, queryArgs...)
		if err != nil {
			return fmt.Errorf("donation: advance deletion revision: %w", err)
		}
		if err := requireOne(result); err != nil {
			return ErrInvariant
		}
		var revision int64
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM donations WHERE id=?`, donationID).Scan(&revision); err != nil {
			return fmt.Errorf("donation: read deletion revision: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO donation_reviews(
donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,?,NULL,'','member_removed','',?)`, donationID, revision, decisionNow); err != nil {
			return fmt.Errorf("donation: record member removal: %w", err)
		}
	}
	return nil
}

func terminalizeDonationTx(
	ctx context.Context,
	tx *sql.Tx,
	donationID, userID, expectedRevision int64,
	wantStatus, finalStatus, endedReason, action, note string,
	now int64,
) error {
	var status string
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT status,revision FROM donations WHERE id=? AND user_id=?`, donationID, userID).Scan(&status, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("donation: read terminal state: %w", err)
	}
	if status != wantStatus || revision != expectedRevision {
		return ErrConflict
	}
	if err := clearDonationMembershipsTx(ctx, tx, donationID, endedReason, now); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE donations SET status=?,revision=revision+1,updated_at=?,terminal_at=?
WHERE id=? AND user_id=? AND status=? AND revision=?`, finalStatus, now, now, donationID, userID, wantStatus, expectedRevision)
	if err != nil {
		return fmt.Errorf("donation: terminalize submission: %w", err)
	}
	if err := requireOne(result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO donation_reviews(
donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,?,NULL,'',?,?,?)`, donationID, expectedRevision+1, action, note, now); err != nil {
		return fmt.Errorf("donation: record terminal action: %w", err)
	}
	return nil
}

func clearDonationMembershipsTx(ctx context.Context, tx *sql.Tx, donationID int64, endedReason string, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT donation_key_id,endpoint_key_id FROM donation_key_memberships
WHERE donation_id=? ORDER BY donation_key_id`, donationID)
	if err != nil {
		return fmt.Errorf("donation: read terminal memberships: %w", err)
	}
	donationKeyIDs := make([]int64, 0)
	endpointKeyIDs := make([]int64, 0)
	for rows.Next() {
		var donationKeyID, endpointKeyID int64
		if err := rows.Scan(&donationKeyID, &endpointKeyID); err != nil {
			rows.Close()
			return fmt.Errorf("donation: scan terminal membership: %w", err)
		}
		donationKeyIDs = append(donationKeyIDs, donationKeyID)
		endpointKeyIDs = append(endpointKeyIDs, endpointKeyID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("donation: close terminal memberships: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("donation: iterate terminal memberships: %w", err)
	}
	if len(donationKeyIDs) == 0 {
		return nil
	}
	if err := detachBindingsTx(ctx, tx, donationKeyIDs, now); err != nil {
		return err
	}
	if err := deleteMembershipsTx(ctx, tx, endpointKeyIDs); err != nil {
		return err
	}
	return endDonationKeysTx(ctx, tx, donationKeyIDs, endedReason, now)
}

func detachBindingsTx(ctx context.Context, tx *sql.Tx, donationKeyIDs []int64, now int64) error {
	if len(donationKeyIDs) == 0 {
		return nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(donationKeyIDs)), ",")
	args := make([]any, len(donationKeyIDs))
	for index, id := range donationKeyIDs {
		args[index] = id
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT charity_model_id FROM charity_model_bindings
WHERE donation_key_id IN (`+placeholders+`) ORDER BY charity_model_id`, args...)
	if err != nil {
		return fmt.Errorf("donation: read affected charity models: %w", err)
	}
	modelIDs := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("donation: scan affected charity model: %w", err)
		}
		modelIDs = append(modelIDs, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("donation: close affected charity models: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("donation: iterate affected charity models: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM charity_model_bindings WHERE donation_key_id IN (`+placeholders+`)`, args...); err != nil {
		return fmt.Errorf("donation: delete charity bindings: %w", err)
	}
	for _, modelID := range modelIDs {
		result, err := tx.ExecContext(ctx, `UPDATE charity_models SET binding_revision=binding_revision+1,updated_at=?
WHERE id=? AND binding_revision<9223372036854775807`, now, modelID)
		if err != nil {
			return fmt.Errorf("donation: advance charity binding revision: %w", err)
		}
		if err := requireOne(result); err != nil {
			return ErrInvariant
		}
	}
	return nil
}

func deleteMembershipsTx(ctx context.Context, tx *sql.Tx, endpointKeyIDs []int64) error {
	if len(endpointKeyIDs) == 0 {
		return nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(endpointKeyIDs)), ",")
	args := make([]any, len(endpointKeyIDs))
	for index, id := range endpointKeyIDs {
		args[index] = id
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM donation_key_memberships WHERE endpoint_key_id IN (`+placeholders+`)`, args...); err != nil {
		return fmt.Errorf("donation: delete memberships: %w", err)
	}
	return nil
}

func endDonationKeysTx(ctx context.Context, tx *sql.Tx, donationKeyIDs []int64, endedReason string, now int64) error {
	if len(donationKeyIDs) == 0 {
		return nil
	}
	if endedReason == "" || now < 0 || now > maxUnixSecond-reportMatchRetention {
		return ErrInvariant
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(donationKeyIDs)), ",")
	args := []any{now, endedReason, now + reportMatchRetention, now}
	query := `UPDATE donation_keys SET enabled=0,ended_at=?,ended_reason=?,report_match_until=?,updated_at=?
WHERE id IN (` + placeholders + `) AND ended_at IS NULL`
	for _, id := range donationKeyIDs {
		args = append(args, id)
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("donation: end donation keys: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("donation: inspect ended keys: %w", err)
	}
	if count != int64(len(donationKeyIDs)) {
		return ErrInvariant
	}
	return nil
}

type expiryMaterialization struct {
	changed  bool
	terminal bool
}

func materializeDonationExpiryStateTx(ctx context.Context, tx *sql.Tx, donationID, now int64) (expiryMaterialization, error) {
	var status string
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT status,revision FROM donations WHERE id=?`, donationID).Scan(&status, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return expiryMaterialization{}, ErrNotFound
	}
	if err != nil {
		return expiryMaterialization{}, fmt.Errorf("donation: read expiry state: %w", err)
	}
	if status != "pending" && status != "approved" {
		return expiryMaterialization{}, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT dk.id,m.endpoint_key_id
FROM donation_keys dk JOIN donation_key_memberships m ON m.donation_key_id=dk.id
WHERE dk.donation_id=? AND dk.ended_at IS NULL AND dk.expires_at IS NOT NULL AND dk.expires_at<=?
ORDER BY dk.expires_at,dk.id`, donationID, now)
	if err != nil {
		return expiryMaterialization{}, fmt.Errorf("donation: read due donation keys: %w", err)
	}
	donationKeyIDs := make([]int64, 0)
	endpointKeyIDs := make([]int64, 0)
	for rows.Next() {
		var donationKeyID, endpointKeyID int64
		if err := rows.Scan(&donationKeyID, &endpointKeyID); err != nil {
			rows.Close()
			return expiryMaterialization{}, fmt.Errorf("donation: scan due donation key: %w", err)
		}
		donationKeyIDs = append(donationKeyIDs, donationKeyID)
		endpointKeyIDs = append(endpointKeyIDs, endpointKeyID)
	}
	if err := rows.Close(); err != nil {
		return expiryMaterialization{}, fmt.Errorf("donation: close due donation keys: %w", err)
	}
	if err := rows.Err(); err != nil {
		return expiryMaterialization{}, fmt.Errorf("donation: iterate due donation keys: %w", err)
	}
	if len(donationKeyIDs) == 0 {
		return expiryMaterialization{}, nil
	}
	if err := detachBindingsTx(ctx, tx, donationKeyIDs, now); err != nil {
		return expiryMaterialization{}, err
	}
	if err := deleteMembershipsTx(ctx, tx, endpointKeyIDs); err != nil {
		return expiryMaterialization{}, err
	}
	if err := endDonationKeysTx(ctx, tx, donationKeyIDs, "expired", now); err != nil {
		return expiryMaterialization{}, err
	}
	var remaining int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donation_keys
WHERE donation_id=? AND ended_at IS NULL`, donationID).Scan(&remaining); err != nil {
		return expiryMaterialization{}, fmt.Errorf("donation: count live donation keys after expiry: %w", err)
	}
	terminal := remaining == 0
	newStatus := status
	var terminalAt any
	if terminal {
		newStatus = "expired"
		terminalAt = now
	}
	result, err := tx.ExecContext(ctx, `UPDATE donations SET status=?,revision=revision+1,updated_at=?,terminal_at=?
WHERE id=? AND status=? AND revision=?`, newStatus, now, terminalAt, donationID, status, revision)
	if err != nil {
		return expiryMaterialization{}, fmt.Errorf("donation: materialize expiry: %w", err)
	}
	if err := requireOne(result); err != nil {
		return expiryMaterialization{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO donation_reviews(
donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,?,NULL,'','expire','',?)`, donationID, revision+1, now); err != nil {
		return expiryMaterialization{}, fmt.Errorf("donation: record expiry: %w", err)
	}
	return expiryMaterialization{changed: true, terminal: terminal}, nil
}

func materializeDonationExpiryTx(ctx context.Context, tx *sql.Tx, donationID, now int64) (bool, error) {
	result, err := materializeDonationExpiryStateTx(ctx, tx, donationID, now)
	return result.terminal, err
}

// MaterializeExpiryTx is the transaction-local expiry ownership seam used by
// charity claim and routing. The caller supplies the transaction and retains
// responsibility for authorization and commit.
func (s *Service) MaterializeExpiryTx(ctx context.Context, tx *sql.Tx, donationID, decisionNow int64) (bool, error) {
	if s == nil || ctx == nil || tx == nil || donationID <= 0 || decisionNow < 0 || decisionNow > maxUnixSecond {
		return false, ErrInvalidRequest
	}
	return materializeDonationExpiryTx(ctx, tx, donationID, decisionNow)
}

// MaterializeDueExpiriesTx performs bounded read-path expiry work inside the
// caller's transaction. It never authorizes or commits independently.
func (s *Service) MaterializeDueExpiriesTx(ctx context.Context, tx *sql.Tx, decisionNow int64, limit int) error {
	if s == nil || ctx == nil || tx == nil || decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > 100 {
		return ErrInvalidRequest
	}
	return materializeDueExpiriesTx(ctx, tx, decisionNow, limit)
}

func materializeDueExpiriesTx(ctx context.Context, tx *sql.Tx, now int64, limit int) error {
	rows, err := tx.QueryContext(ctx, `SELECT dk.donation_id
FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id
WHERE d.status IN ('pending','approved') AND dk.ended_at IS NULL
 AND dk.expires_at IS NOT NULL AND dk.expires_at<=?
GROUP BY dk.donation_id ORDER BY MIN(dk.expires_at),dk.donation_id LIMIT ?`, now, limit)
	if err != nil {
		return fmt.Errorf("donation: read due expiries: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := materializeDonationExpiryTx(ctx, tx, id, now); err != nil {
			return err
		}
	}
	return nil
}

// MaterializeExpiries is the bounded worker adapter. It shares the exact same
// transaction state machine as read/claim-adjacent control paths.
func (s *Service) MaterializeExpiries(ctx context.Context, decisionNow int64, limit int) (int, error) {
	if s == nil || s.db == nil || ctx == nil || decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > 100 {
		return 0, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("donation: begin expiry worker: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT dk.donation_id
FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id
WHERE d.status IN ('pending','approved') AND dk.ended_at IS NULL
 AND dk.expires_at IS NOT NULL AND dk.expires_at<=?
GROUP BY dk.donation_id ORDER BY MIN(dk.expires_at),dk.donation_id LIMIT ?`, decisionNow, limit)
	if err != nil {
		return 0, fmt.Errorf("donation: list expiry work: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := materializeDonationExpiryTx(ctx, tx, id, decisionNow); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("donation: commit expiry worker: %w", err)
	}
	return len(ids), nil
}

// PrepareAccountDeletion is the L1 transaction adapter. It removes live
// authority and private donor fields but leaves append-only ledger facts and
// already-frozen attempts to their own owners.
func (s *Service) PrepareAccountDeletion(ctx context.Context, tx *sql.Tx, userID, decisionNow int64) error {
	if s == nil || ctx == nil || tx == nil || userID <= 0 || decisionNow < 0 || decisionNow > maxUnixSecond {
		return ErrInvalidRequest
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,status,revision FROM donations WHERE user_id=? ORDER BY id`, userID)
	if err != nil {
		return fmt.Errorf("donation: read account donations: %w", err)
	}
	type row struct {
		id, revision int64
		status       string
	}
	values := make([]row, 0)
	for rows.Next() {
		var value row
		if err := rows.Scan(&value.id, &value.status, &value.revision); err != nil {
			rows.Close()
			return fmt.Errorf("donation: scan account donation: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("donation: close account donations: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("donation: iterate account donations: %w", err)
	}
	for _, value := range values {
		if value.status == "pending" || value.status == "approved" {
			if err := clearDonationMembershipsTx(ctx, tx, value.id, "account_deleted", decisionNow); err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `UPDATE donations SET status='deleted',revision=revision+1,
description='',review_note='',user_id=NULL,updated_at=?,terminal_at=? WHERE id=? AND user_id=? AND revision=?`,
				decisionNow, decisionNow, value.id, userID, value.revision)
			if err != nil {
				return fmt.Errorf("donation: revoke account donation: %w", err)
			}
			if err := requireOne(result); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO donation_reviews(
donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,?,NULL,'','terminate','',?)`, value.id, value.revision+1, decisionNow); err != nil {
				return fmt.Errorf("donation: record account revocation: %w", err)
			}
		} else {
			result, err := tx.ExecContext(ctx, `UPDATE donations SET description='',review_note='',user_id=NULL,updated_at=?
WHERE id=? AND user_id=?`, decisionNow, value.id, userID)
			if err != nil {
				return fmt.Errorf("donation: deidentify terminal donation: %w", err)
			}
			if err := requireOne(result); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE donation_keys SET safe_note='',updated_at=? WHERE donation_id=?`, decisionNow, value.id); err != nil {
			return fmt.Errorf("donation: scrub donation key note: %w", err)
		}
		// donation_reviews are schema-enforced append-only decision facts. Their
		// actor FK is deidentified by account deletion; the mutable projection
		// above is the only review text cleared in this transaction.
	}
	return nil
}

func (s *Service) DeidentifyUser(ctx context.Context, tx *sql.Tx, userID, decisionNow int64) error {
	return s.PrepareAccountDeletion(ctx, tx, userID, decisionNow)
}

func (s *Service) ExportUser(ctx context.Context, tx *sql.Tx, userID int64, limit int) ([]ExportDonation, error) {
	if s == nil || ctx == nil || tx == nil || userID <= 0 || limit < 1 || limit > 10000 {
		return nil, ErrInvalidRequest
	}
	decisionNow, err := s.nowUnix()
	if err != nil {
		return nil, err
	}
	return s.ExportUserTx(ctx, tx, userID, decisionNow, limit)
}

// ExportUserTx returns the donor's ordinary-retention view from the
// caller-owned transaction and fixed decision time. Active legal hold never
// widens this owner export: terminal rows at or beyond their 400-day deadline
// are excluded even if physical cleanup is paused.
func (s *Service) ExportUserTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, decisionNow int64,
	limit int,
) ([]ExportDonation, error) {
	if s == nil || ctx == nil || tx == nil || userID <= 0 || decisionNow < 0 || decisionNow > maxUnixSecond ||
		limit < 1 || limit > 10000 {
		return nil, ErrInvalidRequest
	}
	cutoff := decisionNow - terminalRetention
	rows, err := tx.QueryContext(ctx, `SELECT id FROM donations
WHERE user_id=? AND (
 status IN ('pending','approved') OR
 (status IN ('rejected','deleted','expired') AND terminal_at IS NOT NULL AND terminal_at>?)
)
ORDER BY id LIMIT ?`, userID, cutoff, limit+1)
	if err != nil {
		return nil, fmt.Errorf("donation: read export identities: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	if len(ids) > limit {
		return nil, ErrResourceLimit
	}
	items := make([]ExportDonation, 0, len(ids))
	for _, id := range ids {
		if _, err := materializeDonationExpiryTx(ctx, tx, id, decisionNow); err != nil {
			return nil, err
		}
		projection, err := getDonationProjectionTx(ctx, tx, id, decisionNow)
		if err != nil {
			return nil, err
		}
		if projection.Owner == nil || projection.Owner.UserID != strconv.FormatInt(userID, 10) {
			return nil, ErrNotFound
		}
		items = append(items, ExportDonation{
			ID: projection.ID, Status: projection.Status, Description: projection.Description,
			ReviewResult: projection.ReviewResult, Keys: exportDonationKeys(projection.Keys),
			CreatedAt: projection.CreatedAt, UpdatedAt: projection.UpdatedAt,
		})
	}
	return items, nil
}

func exportDonationKeys(values []AdminDonationKey) []ExportDonationKey {
	out := make([]ExportDonationKey, len(values))
	for index, value := range values {
		owner := ownerKey(value)
		out[index] = ExportDonationKey{
			ID: owner.ID, EndpointKeyID: owner.EndpointKeyID,
			DisplayHead: owner.DisplayHead, DisplayTail: owner.DisplayTail,
			SafeSource: owner.SafeSource, PhysicalEnabled: owner.PhysicalEnabled,
			CharityState: owner.CharityState, Limits: owner.Limits, Usage: owner.Usage,
			TokenReserve: owner.TokenReserve, AuthorizedExpiresAt: value.AuthorizedExpiresAt,
			ExpiresAt: owner.ExpiresAt, Streak: owner.Streak, EndedReason: owner.EndedReason,
		}
	}
	return out
}

// Cleanup deletes only terminal donation aggregates whose original 400-day
// deadline has elapsed. Active legal hold covers exactly donation+keys+reviews;
// it does not change membership or reservation cleanup.
func (s *Service) Cleanup(ctx context.Context, decisionNow int64, limit int) (int, error) {
	if s == nil || s.db == nil || ctx == nil || decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > 100 {
		return 0, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("donation: begin retention cleanup: %w", err)
	}
	defer tx.Rollback()
	count, err := s.CleanupTx(ctx, tx, decisionNow, limit)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("donation: commit retention cleanup: %w", err)
	}
	return count, nil
}

// CleanupTx performs one bounded terminal-donation cleanup batch in the
// caller-owned transaction. The caller decides whether the batch commits.
func (s *Service) CleanupTx(ctx context.Context, tx *sql.Tx, decisionNow int64, limit int) (int, error) {
	if s == nil || ctx == nil || tx == nil || decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > 100 {
		return 0, ErrInvalidRequest
	}
	cutoff := decisionNow - terminalRetention
	if cutoff < 0 {
		cutoff = 0
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.id FROM donations d
WHERE d.status IN ('rejected','deleted','expired') AND d.terminal_at IS NOT NULL AND d.terminal_at<=?
AND NOT EXISTS(SELECT 1 FROM donation_keys dk JOIN donation_usage_reservations r
 ON r.donation_key_id=dk.id AND r.state='reserved' WHERE dk.donation_id=d.id)
AND NOT EXISTS(SELECT 1 FROM legal_holds h WHERE h.object_kind='donation'
 AND h.object_ref=CAST(d.id AS TEXT) AND h.state='active')
ORDER BY d.terminal_at,d.id LIMIT ?`, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("donation: list retention work: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, `DELETE FROM donations WHERE id=? AND status IN ('rejected','deleted','expired')`, id)
		if err != nil {
			return 0, fmt.Errorf("donation: delete retained aggregate: %w", err)
		}
		if err := requireOne(result); err != nil {
			return 0, ErrInvariant
		}
	}
	return len(ids), nil
}

func incrementU128(value db.U128) (db.U128, error) {
	result := value.Big()
	result.Add(result, bigOne)
	out, err := db.U128FromBig(result)
	if err != nil {
		return db.U128{}, ErrInvariant
	}
	return out, nil
}

var bigOne = func() *big.Int {
	value := new(big.Int)
	value.SetInt64(1)
	return value
}()
