package donation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func (s *Service) GetOwner(ctx context.Context, userID, donationID int64) (Donation, error) {
	if s == nil || s.db == nil || ctx == nil || userID <= 0 || donationID <= 0 {
		return Donation{}, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return Donation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Donation{}, fmt.Errorf("donation: begin owner read: %w", err)
	}
	defer tx.Rollback()
	var owned int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donations WHERE id=? AND user_id=?)`, donationID, userID).Scan(&owned); err != nil {
		return Donation{}, fmt.Errorf("donation: verify owner read: %w", err)
	}
	if owned != 1 {
		return Donation{}, ErrNotFound
	}
	if _, err := materializeDonationExpiryTx(ctx, tx, donationID, now); err != nil {
		return Donation{}, err
	}
	visible, err := donationOrdinarilyVisibleTx(ctx, tx, donationID, now)
	if err != nil {
		return Donation{}, err
	}
	if !visible {
		return Donation{}, ErrNotFound
	}
	value, err := getOwnerDonationTx(ctx, tx, userID, donationID, now)
	if err != nil {
		return Donation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Donation{}, fmt.Errorf("donation: commit owner read: %w", err)
	}
	return value, nil
}

func (s *Service) GetAdmin(ctx context.Context, donationID int64) (AdminDonation, error) {
	if s == nil || s.db == nil || ctx == nil || donationID <= 0 {
		return AdminDonation{}, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return AdminDonation{}, err
	}
	tx, _, err := s.beginRoleTx(ctx, reviewerAdmin, 0)
	if err != nil {
		return AdminDonation{}, err
	}
	defer tx.Rollback()
	if _, err := materializeDonationExpiryTx(ctx, tx, donationID, now); err != nil {
		return AdminDonation{}, err
	}
	ordinary, err := donationOrdinarilyVisibleTx(ctx, tx, donationID, now)
	if err != nil {
		return AdminDonation{}, err
	}
	held := false
	if s.heldRead != nil {
		held, err = s.heldRead.AuthorizeHeldDonationRead(ctx, tx, donationID, now)
		if err != nil {
			return AdminDonation{}, err
		}
	}
	if !ordinary && !held {
		if err := tx.Commit(); err != nil {
			return AdminDonation{}, fmt.Errorf("donation: commit hidden admin read: %w", err)
		}
		return AdminDonation{}, ErrNotFound
	}
	value, err := getAdminDonationTx(ctx, tx, donationID, now)
	if err != nil {
		return AdminDonation{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminDonation{}, fmt.Errorf("donation: commit admin read: %w", err)
	}
	return value, nil
}

func (s *Service) GetSteward(ctx context.Context, userID, donationID int64) (StewardDonation, error) {
	if s == nil || s.db == nil || ctx == nil || userID <= 0 || donationID <= 0 || nilDependency(s.roleAuth) {
		return StewardDonation{}, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return StewardDonation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StewardDonation{}, fmt.Errorf("donation: begin steward read: %w", err)
	}
	defer tx.Rollback()
	if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, userID); err != nil {
		return StewardDonation{}, mapAuthorization(err)
	}
	if _, err := materializeDonationExpiryTx(ctx, tx, donationID, now); err != nil {
		return StewardDonation{}, err
	}
	visible, err := donationOrdinarilyVisibleTx(ctx, tx, donationID, now)
	if err != nil {
		return StewardDonation{}, err
	}
	if !visible {
		held, err := s.managementHeldRead(ctx, tx, reviewerSteward, userID, donationID, now)
		if err != nil {
			return StewardDonation{}, err
		}
		visible = visible || held
	}
	if !visible {
		if err := tx.Commit(); err != nil {
			return StewardDonation{}, fmt.Errorf("donation: commit steward retention read: %w", err)
		}
		return StewardDonation{}, ErrNotFound
	}
	value, err := getAdminDonationTx(ctx, tx, donationID, now)
	if err != nil {
		return StewardDonation{}, err
	}
	if err := tx.Commit(); err != nil {
		return StewardDonation{}, fmt.Errorf("donation: commit steward read: %w", err)
	}
	return stewardFromAdmin(value, userID), nil
}

// ListOwner uses an explicit stable ID cursor. HTTP adapters bind this atom to
// the authenticated owner with an authenticated cursor token.
func (s *Service) ListOwner(ctx context.Context, userID, afterID int64, limit int) ([]Donation, int64, error) {
	if s == nil || s.db == nil || ctx == nil || userID <= 0 || afterID < 0 || limit < 1 || limit > 100 {
		return nil, 0, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return nil, 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("donation: begin owner list: %w", err)
	}
	defer tx.Rollback()
	if err := materializeProjectionExpiriesTx(ctx, tx, now); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM donations
WHERE user_id=? AND id>?
 AND (status IN ('pending','approved') OR terminal_at>?)
ORDER BY id LIMIT ?`, userID, afterID, now-terminalRetention, limit+1)
	if err != nil {
		return nil, 0, fmt.Errorf("donation: list owner ids: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, 0, err
	}
	next := int64(0)
	if len(ids) > limit {
		next = ids[limit-1]
		ids = ids[:limit]
	}
	items := make([]Donation, 0, len(ids))
	for _, id := range ids {
		item, err := getOwnerDonationTx(ctx, tx, userID, id, now)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("donation: commit owner list: %w", err)
	}
	return items, next, nil
}

func (s *Service) ListAdmin(ctx context.Context, status string, afterID int64, limit int) ([]AdminDonation, int64, error) {
	return s.listRole(ctx, 0, ManagementFilter{Status: status}, afterID, limit, reviewerAdmin)
}

func (s *Service) ListSteward(ctx context.Context, userID int64, status string, afterID int64, limit int) ([]StewardDonation, int64, error) {
	return s.ListStewardFiltered(ctx, userID, ManagementFilter{Status: status}, afterID, limit)
}

func (s *Service) ListAdminFiltered(ctx context.Context, filter ManagementFilter, afterID int64, limit int) ([]AdminDonation, int64, error) {
	return s.listRole(ctx, 0, filter, afterID, limit, reviewerAdmin)
}

func (s *Service) ListStewardFiltered(ctx context.Context, userID int64, filter ManagementFilter, afterID int64, limit int) ([]StewardDonation, int64, error) {
	items, next, err := s.listRole(ctx, userID, filter, afterID, limit, reviewerSteward)
	if err != nil {
		return nil, 0, err
	}
	out := make([]StewardDonation, len(items))
	for index := range items {
		out[index] = stewardFromAdmin(items[index], userID)
	}
	return out, next, nil
}

func (s *Service) listRole(ctx context.Context, userID int64, filter ManagementFilter, afterID int64, limit int, role reviewerRole) ([]AdminDonation, int64, error) {
	if s == nil || s.db == nil || ctx == nil || afterID < 0 || limit < 1 || limit > 100 ||
		!validManagementFilter(filter) {
		return nil, 0, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	now, err := s.nowUnix()
	if err != nil {
		return nil, 0, err
	}
	tx, _, err := s.beginRoleTx(ctx, role, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("donation: begin role list: %w", err)
	}
	defer tx.Rollback()
	query := `SELECT d.id FROM donations d JOIN donation_handling h ON h.donation_id=d.id WHERE d.id>?
 AND (d.status IN ('pending','approved') OR d.terminal_at>?)`
	args := []any{afterID, now - terminalRetention}
	query, args = appendManagementFilter(query, args, filter, now)
	query += ` ORDER BY d.id LIMIT ?`
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("donation: list role ids: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, 0, err
	}
	next := int64(0)
	if len(ids) > limit {
		next = ids[limit-1]
		ids = ids[:limit]
	}
	items := make([]AdminDonation, 0, len(ids))
	for _, id := range ids {
		if _, err := materializeDonationExpiryTx(ctx, tx, id, now); err != nil {
			return nil, 0, err
		}
		item, err := getAdminDonationTx(ctx, tx, id, now)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("donation: commit role list: %w", err)
	}
	return items, next, nil
}

func materializeProjectionExpiriesTx(ctx context.Context, tx *sql.Tx, now int64) error {
	if err := materializeDueExpiriesTx(ctx, tx, now, 100); err != nil {
		return err
	}
	var remains int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id
WHERE d.status IN ('pending','approved') AND dk.ended_at IS NULL
 AND dk.expires_at IS NOT NULL AND dk.expires_at<=?)`, now).Scan(&remains); err != nil {
		return fmt.Errorf("donation: verify projection expiry catch-up: %w", err)
	}
	if remains != 0 {
		return ErrUnavailable
	}
	return nil
}

func scanIDs(rows *sql.Rows) ([]int64, error) {
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("donation: scan list identity: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("donation: iterate list identities: %w", err)
	}
	return ids, nil
}

func donationOrdinarilyVisibleTx(ctx context.Context, tx *sql.Tx, donationID, now int64) (bool, error) {
	var status string
	var terminalAt sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT status,terminal_at FROM donations WHERE id=?`, donationID).Scan(
		&status, &terminalAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("donation: inspect ordinary visibility: %w", err)
	}
	switch status {
	case "pending", "approved":
		if terminalAt.Valid {
			return false, ErrInvariant
		}
		return true, nil
	case "rejected", "deleted", "expired":
		if !terminalAt.Valid || terminalAt.Int64 < 0 || terminalAt.Int64 > maxUnixSecond {
			return false, ErrInvariant
		}
		if terminalAt.Int64 > maxUnixSecond-terminalRetention {
			return true, nil
		}
		return now < terminalAt.Int64+terminalRetention, nil
	default:
		return false, ErrInvariant
	}
}

func getOwnerDonationTx(ctx context.Context, tx *sql.Tx, userID, donationID, now int64) (Donation, error) {
	admin, err := getDonationProjectionTx(ctx, tx, donationID, now)
	if err != nil {
		return Donation{}, err
	}
	if admin.Owner == nil || admin.Owner.UserID != strconv.FormatInt(userID, 10) {
		return Donation{}, ErrNotFound
	}
	return Donation{
		DiscordPublicThanks: admin.DiscordPublicThanks,
		ID:                  admin.ID, Status: admin.Status, Revision: admin.Revision, Description: admin.Description,
		ReviewResult: admin.ReviewResult, Keys: ownerKeys(admin.Keys),
		CreatedAt: admin.CreatedAt, UpdatedAt: admin.UpdatedAt,
	}, nil
}

func getAdminDonationTx(ctx context.Context, tx *sql.Tx, donationID, now int64) (AdminDonation, error) {
	return getDonationProjectionTx(ctx, tx, donationID, now)
}

func getDonationProjectionTx(ctx context.Context, tx *sql.Tx, donationID, now int64) (AdminDonation, error) {
	out, err := getDonationHeaderTx(ctx, tx, donationID)
	if err != nil {
		return AdminDonation{}, err
	}
	out.Keys, err = readDonationKeysTx(ctx, tx, donationID, out.Status, now)
	return out, err
}

// Header reads deliberately avoid loading a donation's complete key collection.
func getDonationHeaderTx(ctx context.Context, tx *sql.Tx, donationID int64) (AdminDonation, error) {
	var out AdminDonation
	var id, revision int64
	var userID, reviewedBy sql.NullInt64
	var discordID sql.NullString
	var username, guildNick string
	var reviewedAt sql.NullInt64
	var reviewRole, reviewNote string
	var thanks sql.NullBool
	err := tx.QueryRowContext(ctx, `SELECT d.id,d.status,d.revision,d.description,d.review_note,
d.reviewed_by_user_id,d.reviewed_by_role,d.reviewed_at,d.created_at,d.updated_at,
d.user_id,u.discord_id,COALESCE(u.username,''),COALESCE(u.guild_nick,''),d.discord_public_thanks
FROM donations d LEFT JOIN users u ON u.id=d.user_id WHERE d.id=?`, donationID).Scan(
		&id, &out.Status, &revision, &out.Description, &reviewNote, &reviewedBy, &reviewRole,
		&reviewedAt, &out.CreatedAt, &out.UpdatedAt, &userID, &discordID, &username, &guildNick, &thanks)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminDonation{}, ErrNotFound
	}
	if err != nil {
		return AdminDonation{}, fmt.Errorf("donation: read projection: %w", err)
	}
	out.ID = strconv.FormatInt(id, 10)
	if thanks.Valid {
		out.DiscordPublicThanks = &thanks.Bool
	}
	out.Revision = strconv.FormatInt(revision, 10)
	if reviewedAt.Valid && (out.Status == "approved" || out.Status == "rejected" || out.Status == "expired" || out.Status == "deleted") {
		decision := "approve"
		if out.Status == "rejected" {
			decision = "reject"
		}
		out.ReviewResult = &ReviewResult{Decision: decision, Reason: reviewNote, ReviewedAt: reviewedAt.Int64}
	}
	if userID.Valid {
		display := guildNick
		if display == "" {
			display = username
		}
		owner := DonationOwner{UserID: strconv.FormatInt(userID.Int64, 10), DisplayName: display}
		if discordID.Valid {
			value := discordID.String
			owner.DiscordID = &value
		}
		out.Owner = &owner
	}
	if reviewRole != "" {
		if reviewRole == string(reviewerSteward) || reviewRole == "level5" {
			reviewRole = "steward"
		}
		reviewer := DonationReviewer{Role: reviewRole}
		if reviewedBy.Valid {
			value := strconv.FormatInt(reviewedBy.Int64, 10)
			reviewer.UserID = &value
		}
		out.Reviewer = &reviewer
	}
	out.Handling, err = readHandlingTx(ctx, tx, donationID)
	if err != nil {
		return AdminDonation{}, err
	}
	return out, nil
}

func readDonationKeysTx(ctx context.Context, tx *sql.Tx, donationID int64, donationStatus string, now int64) ([]AdminDonationKey, error) {
	return readDonationKeySelectionTx(ctx, tx, donationID, donationStatus, now, nil)
}

// selectedIDs, when present, were obtained from an authorized page in this
// transaction. The parent condition is retained for every projected key.
func readDonationKeySelectionTx(ctx context.Context, tx *sql.Tx, donationID int64, donationStatus string, now int64, selectedIDs []int64) ([]AdminDonationKey, error) {
	where := `dk.donation_id=?`
	args := []any{donationID}
	if selectedIDs != nil {
		if len(selectedIDs) == 0 {
			return []AdminDonationKey{}, nil
		}
		if len(selectedIDs) > 100 {
			return nil, ErrInvalidRequest
		}
		where += ` AND dk.id IN (`
		for index, id := range selectedIDs {
			if id <= 0 {
				return nil, ErrInvalidRequest
			}
			if index > 0 {
				where += ","
			}
			where += "?"
			args = append(args, id)
		}
		where += ")"
	}
	rows, err := tx.QueryContext(ctx, `SELECT dk.id,dk.endpoint_key_id,dk.display_head,dk.display_tail,
dk.canonical_base_url,dk.connector_type,dk.mainstream_channel_id,dk.mainstream_channel_revision,
dk.mainstream_channel_name,dk.mainstream_channel_category,COALESCE(e.enabled,0),COALESCE(k.enabled,0),
dk.price_limit_mag,dk.call_limit_mag,dk.token_limit_mag,
dk.price_used_mag,dk.price_reserved_mag,dk.calls_used,dk.calls_reserved,dk.tokens_used,dk.tokens_reserved,
dk.token_reserve,dk.enabled,dk.failure_disabled,dk.failure_streak,dk.streak_generation,dk.failure_disable_threshold,dk.safe_note,
dk.authorized_expires_at,dk.expires_at,dk.ended_reason,
EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=dk.endpoint_key_id),
EXISTS(SELECT 1 FROM donation_key_memberships m WHERE m.donation_key_id=dk.id),
CASE WHEN k.id IS NOT NULL THEN COALESCE(kl.max_concurrency,0) END,
CASE WHEN k.id IS NOT NULL THEN COALESCE(kl.max_rpm,0) END,
(SELECT COUNT(*) FROM charity_model_bindings b WHERE b.donation_key_id=dk.id)
FROM donation_keys dk
LEFT JOIN endpoint_keys k ON k.id=dk.endpoint_key_id
LEFT JOIN endpoint_key_limits kl ON kl.endpoint_key_id=k.id
LEFT JOIN endpoints e ON e.id=k.endpoint_id
WHERE `+where+` ORDER BY dk.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("donation: read key projections: %w", err)
	}
	defer rows.Close()
	items := make([]AdminDonationKey, 0)
	for rows.Next() {
		var item AdminDonationKey
		var id int64
		var endpointKeyID sql.NullInt64
		var endpointEnabled, keyPhysicalEnabled, enabled, failureDisabled, suspended, member int
		var priceLimit, callLimit, tokenLimit []byte
		var priceUsed, priceReserved, callsUsed, callsReserved, tokensUsed, tokensReserved []byte
		var streak, generation []byte
		var ended sql.NullString
		var authorizedExpires, expires sql.NullInt64
		var channelID, channelName, channelCategory sql.NullString
		var channelRevision sql.NullInt64
		var bindingCount int64
		if err := rows.Scan(&id, &endpointKeyID, &item.DisplayHead, &item.DisplayTail,
			&item.SafeSource.BaseURL, &item.SafeSource.ConnectorType, &channelID, &channelRevision,
			&channelName, &channelCategory, &endpointEnabled, &keyPhysicalEnabled,
			&priceLimit, &callLimit, &tokenLimit, &priceUsed, &priceReserved, &callsUsed, &callsReserved,
			&tokensUsed, &tokensReserved, &item.TokenReserve, &enabled, &failureDisabled, &streak,
			&generation, &item.FailureDisableThreshold, &item.SafeNote, &authorizedExpires, &expires, &ended, &suspended, &member, &item.MaxConcurrency, &item.MaxRPM, &bindingCount); err != nil {
			return nil, fmt.Errorf("donation: scan key projection: %w", err)
		}
		item.ID = strconv.FormatInt(id, 10)
		item.BindingCount = strconv.FormatInt(bindingCount, 10)
		item.Idle = bindingCount == 0
		if endpointKeyID.Valid {
			value := strconv.FormatInt(endpointKeyID.Int64, 10)
			item.EndpointKeyID = &value
		}
		if authorizedExpires.Valid {
			value := authorizedExpires.Int64
			item.AuthorizedExpiresAt = &value
		}
		if expires.Valid {
			value := expires.Int64
			item.ExpiresAt = &value
		}
		item.SafeSource.Kind = "custom"
		completeMainstream := channelID.Valid && channelRevision.Valid && channelName.Valid && channelCategory.Valid
		completeCustom := !channelID.Valid && !channelRevision.Valid && !channelName.Valid && !channelCategory.Valid
		if !completeMainstream && !completeCustom {
			return nil, ErrInvariant
		}
		if completeMainstream {
			item.SafeSource.Kind = "mainstream"
			channelIDValue := channelID.String
			channelNameValue := channelName.String
			channelRevisionValue := strconv.FormatInt(channelRevision.Int64, 10)
			channelCategoryValue := channelCategory.String
			item.SafeSource.ChannelID = &channelIDValue
			item.SafeSource.Name = &channelNameValue
			item.SafeSource.ChannelRevision = &channelRevisionValue
			item.SafeSource.Category = &channelCategoryValue
		}
		item.PhysicalEnabled = endpointEnabled == 1 && keyPhysicalEnabled == 1
		item.Limits.Price, err = nullableAmountFromBlob(priceLimit)
		if err == nil {
			item.Limits.Calls, err = nullableDecimalFromBlob(callLimit)
		}
		if err == nil {
			item.Limits.Tokens, err = nullableDecimalFromBlob(tokenLimit)
		}
		if err != nil {
			return nil, err
		}
		item.Usage.PriceUsed, err = amountFromBlob(priceUsed)
		if err == nil {
			item.Usage.PriceInflight, err = amountFromBlob(priceReserved)
		}
		if err == nil {
			item.Usage.CallsUsed, err = decimalFromBlob(callsUsed)
		}
		if err == nil {
			item.Usage.CallsInflight, err = decimalFromBlob(callsReserved)
		}
		if err == nil {
			item.Usage.TokensUsed, err = decimalFromBlob(tokensUsed)
		}
		if err == nil {
			item.Usage.TokensInflight, err = decimalFromBlob(tokensReserved)
		}
		if err != nil {
			return nil, err
		}
		item.Streak.Count, err = decimalFromBlob(streak)
		if err == nil {
			_, err = db.ParseU128Decimal(item.FailureDisableThreshold)
		}
		if err == nil {
			item.Streak.Generation, err = decimalFromBlob(generation)
		}
		if err != nil {
			return nil, err
		}
		item.Streak.FailureDisabled = failureDisabled == 1
		if ended.Valid {
			value := ended.String
			item.EndedReason = &value
		}
		exhausted, err := anyCapExhausted(priceLimit, priceUsed, priceReserved, callLimit, callsUsed, callsReserved, tokenLimit, tokensUsed, tokensReserved)
		if err != nil {
			return nil, err
		}
		switch {
		case ended.Valid || member == 0:
			if ended.Valid && ended.String == "expired" || donationStatus == "expired" {
				item.CharityState = "expired"
			} else {
				item.CharityState = "ended"
			}
		case expires.Valid && now >= expires.Int64 || donationStatus == "expired":
			item.CharityState = "expired"
		case donationStatus == "pending":
			item.CharityState = "pending"
		case !item.PhysicalEnabled || enabled == 0 || failureDisabled == 1:
			item.CharityState = "disabled"
		case suspended == 1:
			item.CharityState = "suspended"
		case exhausted:
			item.CharityState = "exhausted"
		default:
			item.CharityState = "available"
		}
		if err := readSplitTokenProjection(ctx, tx, id, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("donation: iterate key projections: %w", err)
	}
	return items, nil
}

func stewardFromAdmin(value AdminDonation, _ int64) StewardDonation {
	var owner *StewardDonationOwner
	if value.Owner != nil {
		copy := StewardDonationOwner(*value.Owner)
		owner = &copy
	}
	return StewardDonation{
		DiscordPublicThanks: value.DiscordPublicThanks,
		ID:                  value.ID, Status: value.Status, Revision: value.Revision, Description: value.Description,
		ReviewResult: value.ReviewResult, Keys: stewardKeys(value.Keys),
		Owner: owner, Reviewer: value.Reviewer, Handling: value.Handling, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func ownerKeys(values []AdminDonationKey) []DonationKey {
	out := make([]DonationKey, len(values))
	for index := range values {
		out[index] = ownerKey(values[index])
	}
	return out
}

func stewardKeys(values []AdminDonationKey) []StewardDonationKey {
	out := make([]StewardDonationKey, len(values))
	for index := range values {
		out[index] = StewardDonationKey(values[index])
	}
	return out
}

func ownerKey(value AdminDonationKey) DonationKey {
	source := SafeSource{
		Kind:          value.SafeSource.Kind,
		ConnectorType: value.SafeSource.ConnectorType,
		BaseURL:       value.SafeSource.BaseURL,
		ChannelID:     value.SafeSource.ChannelID,
		Name:          value.SafeSource.Name,
	}
	return DonationKey{
		TokenBreakdown:          value.TokenBreakdown,
		FailureDisableThreshold: value.FailureDisableThreshold,
		ID:                      value.ID, EndpointKeyID: value.EndpointKeyID, DisplayHead: value.DisplayHead,
		DisplayTail: value.DisplayTail, SafeSource: source, PhysicalEnabled: value.PhysicalEnabled,
		CharityState: value.CharityState, Limits: value.Limits, Usage: value.Usage,
		TokenReserve: value.TokenReserve, ExpiresAt: value.ExpiresAt, Streak: value.Streak,
		EndedReason: value.EndedReason,
	}
}

func nullableAmountFromBlob(blob []byte) (*string, error) {
	if blob == nil {
		return nil, nil
	}
	value, err := amountFromBlob(blob)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func nullableDecimalFromBlob(blob []byte) (*string, error) {
	if blob == nil {
		return nil, nil
	}
	value, err := decimalFromBlob(blob)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func decimalFromBlob(blob []byte) (string, error) {
	value, err := db.DecodeU128(blob)
	if err != nil {
		return "", ErrInvariant
	}
	return value.Decimal(), nil
}

func amountFromBlob(blob []byte) (string, error) {
	value, err := db.DecodeU128(blob)
	if err != nil {
		return "", ErrInvariant
	}
	return formatMilli(value.Big()), nil
}

func formatMilli(value *big.Int) string {
	if value == nil || value.Sign() == 0 {
		return "0"
	}
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(value, big.NewInt(1000), remainder)
	text := whole.String()
	if remainder.Sign() != 0 {
		fraction := fmt.Sprintf("%03d", remainder.Int64())
		for len(fraction) != 0 && fraction[len(fraction)-1] == '0' {
			fraction = fraction[:len(fraction)-1]
		}
		text += "." + fraction
	}
	return text
}

func anyCapExhausted(blobs ...[]byte) (bool, error) {
	for index := 0; index < len(blobs); index += 3 {
		limit := blobs[index]
		if limit == nil {
			continue
		}
		l, err := db.DecodeU128(limit)
		if err != nil {
			return false, ErrInvariant
		}
		used, err := db.DecodeU128(blobs[index+1])
		if err != nil {
			return false, ErrInvariant
		}
		reserved, err := db.DecodeU128(blobs[index+2])
		if err != nil {
			return false, ErrInvariant
		}
		if new(big.Int).Add(used.Big(), reserved.Big()).Cmp(l.Big()) >= 0 {
			return true, nil
		}
	}
	return false, nil
}

func validStatusFilter(status string) bool {
	switch status {
	case "", "pending", "approved", "rejected", "deleted", "expired":
		return true
	default:
		return false
	}
}
