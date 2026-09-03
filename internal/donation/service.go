package donation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	maxUnixSecond         = int64(253402300799)
	maxDonationKeys       = 100
	maxEndpointDeleteKeys = 10000
	maxDonationTextRunes  = 1024
	maxSafeNoteRunes      = 256
	terminalRetention     = int64(400 * 24 * 60 * 60)
	reportMatchRetention  = int64(90 * 24 * 60 * 60)
)

const (
	routeDonations        = "/api/donations"
	routeDonation         = "/api/donations/{id}"
	routeWithdraw         = "/api/donations/{id}/withdraw"
	routeTerminate        = "/api/donations/{id}/terminate"
	routeAdminDonations   = "/admin/api/donations"
	routeAdminDonation    = "/admin/api/donations/{id}"
	routeAdminReview      = "/admin/api/donations/{id}/review"
	routeAdminKey         = "/admin/api/donations/{id}/keys/{keyId}"
	routeStewardDonations = "/api/steward/donations"
	routeStewardDonation  = "/api/steward/donations/{id}"
	routeStewardReview    = "/api/steward/donations/{id}/review"
	routeStewardKey       = "/api/steward/donations/{id}/keys/{keyId}"
)

type Config struct {
	Store      *db.Store
	OwnerAuth  OwnerFinalTxAuthorizer
	RoleAuth   RoleFinalTxAuthorizer
	CursorKeys resources.CursorKeyDeriver
	Now        func() time.Time
}

type Service struct {
	db         *sql.DB
	ownerAuth  OwnerFinalTxAuthorizer
	roleAuth   RoleFinalTxAuthorizer
	heldRead   AdminHeldReadAuthorizer
	cursorKeys resources.CursorKeyDeriver
	now        func() time.Time
}

func New(config Config) (*Service, error) {
	if config.Store == nil || config.Store.DB() == nil {
		return nil, errors.New("donation: store is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{db: config.Store.DB(), ownerAuth: config.OwnerAuth, roleAuth: config.RoleAuth,
		cursorKeys: config.CursorKeys, now: config.Now}, nil
}

func (s *Service) nowUnix() (int64, error) {
	if s == nil || s.now == nil {
		return 0, ErrUnavailable
	}
	value := s.now().Unix()
	if value < 0 || value > maxUnixSecond-idempotency.ReplayWindowSeconds {
		return 0, ErrUnavailable
	}
	return value, nil
}

func (s *Service) Create(
	ctx context.Context,
	userID int64,
	mutation resources.ControlMutation,
	input CreateInput,
) (resources.MutationResult[Donation], error) {
	if s == nil || ctx == nil || userID <= 0 || mutation.Method != http.MethodPost || mutation.Route != routeDonations ||
		len(mutation.PathIDs) != 0 || mutation.Query != "" || !validDonationText(input.Description) ||
		!input.OwnershipAuthorized || !validCreateKeys(input.Keys) {
		return resources.MutationResult[Donation]{}, ErrInvalidRequest
	}
	tx, err := s.beginOwnerTx(ctx, userID)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	decision, err := beginMutation(ctx, tx, "user", userID, idempotency.ScopeDonation, mutation, now)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[Donation](decision)
	}
	for _, key := range input.Keys {
		if key.ExpiresAt != nil && (*key.ExpiresAt <= now || *key.ExpiresAt > maxUnixSecond) {
			return resources.MutationResult[Donation]{}, ErrInvalidRequest
		}
	}
	if enabled, err := configBool(ctx, tx, "donation_accept_enabled"); err != nil {
		return resources.MutationResult[Donation]{}, err
	} else if !enabled {
		return resources.MutationResult[Donation]{}, ErrFeatureDisabled
	}

	keys, err := validateSubmissionKeys(ctx, tx, userID, input.Keys)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	autoApprove, err := submissionAutoApproval(keys)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO donations(
user_id,status,revision,description,review_note,reviewed_by_role,created_at,updated_at)
VALUES(?,'pending',1,?,'','',?,?)`, userID, input.Description, now, now)
	if err != nil {
		return resources.MutationResult[Donation]{}, fmt.Errorf("donation: create submission: %w", err)
	}
	donationID, err := result.LastInsertId()
	if err != nil || donationID <= 0 {
		return resources.MutationResult[Donation]{}, ErrInvariant
	}
	zero := db.EncodeU128(db.U128{})
	one := db.U128{}
	one[15] = 1
	defaultSettings := make([]KeySetting, 0, len(keys))
	for _, key := range keys {
		result, err = tx.ExecContext(ctx, `INSERT INTO donation_keys(
donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,
price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,
failure_streak,streak_generation,next_claim_seq,next_fold_seq,created_at,updated_at,
authorized_expires_at,expires_at,mainstream_channel_id,mainstream_channel_revision,
mainstream_channel_name,mainstream_channel_category,source_endpoint_key_id,report_fingerprint)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			donationID, key.id, key.head, key.tail, key.baseURL, key.connector,
			zero, zero, zero, zero, zero, zero, zero, db.EncodeU128(one), db.EncodeU128(one), db.EncodeU128(one), now, now,
			key.expiresAt, key.expiresAt, key.channelID, key.channelRevision, key.channelName, key.channelCategory,
			key.id, key.reportFingerprint)
		if err != nil {
			return resources.MutationResult[Donation]{}, classifyWrite("create donation key", err)
		}
		donationKeyID, err := result.LastInsertId()
		if err != nil || donationKeyID <= 0 {
			return resources.MutationResult[Donation]{}, ErrInvariant
		}
		defaultSettings = append(defaultSettings, KeySetting{
			DonationKeyID: donationKeyID,
			Enabled:       true,
			ExpiresAt:     key.expiresAt,
		})
		if _, err := tx.ExecContext(ctx, `INSERT INTO donation_key_memberships(
endpoint_key_id,donation_key_id,donation_id,created_at) VALUES(?,?,?,?)`,
			key.id, donationKeyID, donationID, now); err != nil {
			return resources.MutationResult[Donation]{}, classifyWrite("create membership", err)
		}
	}
	if autoApprove {
		if err := applyApprovalKeySettingsTx(ctx, tx, donationID, defaultSettings, now); err != nil {
			return resources.MutationResult[Donation]{}, err
		}
		result, err := tx.ExecContext(ctx, `UPDATE donations SET status='approved',review_note='',
reviewed_by_user_id=NULL,reviewed_by_role='',reviewed_at=?,updated_at=?
WHERE id=? AND status='pending' AND revision=1`, now, now, donationID)
		if err != nil {
			return resources.MutationResult[Donation]{}, fmt.Errorf("donation: auto-approve submission: %w", err)
		}
		if err := requireOne(result); err != nil {
			return resources.MutationResult[Donation]{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO donation_reviews(
donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,1,NULL,'','approve','',?)`, donationID, now); err != nil {
			return resources.MutationResult[Donation]{}, fmt.Errorf("donation: record automatic approval: %w", err)
		}
	}
	value, err := getOwnerDonationTx(ctx, tx, userID, donationID, now)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusCreated, value)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	return out, nil
}

func (s *Service) Edit(
	ctx context.Context,
	userID, donationID int64,
	mutation resources.ControlMutation,
	input EditInput,
) (resources.MutationResult[Donation], error) {
	if s == nil || ctx == nil || userID <= 0 || donationID <= 0 || input.ExpectedRevision <= 0 ||
		!validDonationText(input.Description) || !validMutation(mutation, http.MethodPatch, routeDonation, donationID) {
		return resources.MutationResult[Donation]{}, ErrInvalidRequest
	}
	return s.ownerDonationMutation(ctx, userID, donationID, mutation, func(ctx context.Context, tx *sql.Tx, now int64) error {
		result, err := tx.ExecContext(ctx, `UPDATE donations SET description=?,revision=revision+1,updated_at=?
WHERE id=? AND user_id=? AND status='pending' AND revision=?`,
			input.Description, now, donationID, userID, input.ExpectedRevision)
		if err != nil {
			return fmt.Errorf("donation: edit submission: %w", err)
		}
		return requireOne(result)
	})
}

func (s *Service) Withdraw(
	ctx context.Context,
	userID, donationID int64,
	mutation resources.ControlMutation,
	input RevisionInput,
) (resources.MutationResult[Donation], error) {
	if s == nil || ctx == nil || userID <= 0 || donationID <= 0 || input.ExpectedRevision <= 0 ||
		!validMutation(mutation, http.MethodPost, routeWithdraw, donationID) {
		return resources.MutationResult[Donation]{}, ErrInvalidRequest
	}
	return s.ownerDonationMutation(ctx, userID, donationID, mutation, func(ctx context.Context, tx *sql.Tx, now int64) error {
		return terminalizeDonationTx(ctx, tx, donationID, userID, input.ExpectedRevision, "pending", "deleted", "withdrawn", "withdraw", "", now)
	})
}

func (s *Service) Terminate(
	ctx context.Context,
	userID, donationID int64,
	mutation resources.ControlMutation,
	input TerminateInput,
) (resources.MutationResult[Donation], error) {
	if s == nil || ctx == nil || userID <= 0 || donationID <= 0 || input.ExpectedRevision <= 0 ||
		strings.TrimSpace(input.Confirmation) == "" || !validMutation(mutation, http.MethodPost, routeTerminate, donationID) {
		return resources.MutationResult[Donation]{}, ErrInvalidRequest
	}
	tx, err := s.beginOwnerTx(ctx, userID)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}

	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM donations WHERE id=? AND user_id=?`,
		donationID, userID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return resources.MutationResult[Donation]{}, ErrNotFound
	}
	if err != nil {
		return resources.MutationResult[Donation]{}, fmt.Errorf("donation: read termination state: %w", err)
	}
	if status == "approved" {
		expiry, err := materializeDonationExpiryStateTx(ctx, tx, donationID, now)
		if err != nil {
			return resources.MutationResult[Donation]{}, err
		}
		if expiry.changed {
			if err := commitTx(tx, &committed); err != nil {
				return resources.MutationResult[Donation]{}, err
			}
			return resources.MutationResult[Donation]{}, ErrConflict
		}
	}

	decision, err := beginMutation(ctx, tx, "user", userID, idempotency.ScopeDonation, mutation, now)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[Donation](decision)
	}
	if err := terminalizeDonationTx(ctx, tx, donationID, userID, input.ExpectedRevision,
		"approved", "deleted", "terminated", "terminate", "", now); err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	value, err := getOwnerDonationTx(ctx, tx, userID, donationID, now)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, value)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	return out, nil
}

func (s *Service) ownerDonationMutation(
	ctx context.Context,
	userID, donationID int64,
	mutation resources.ControlMutation,
	apply func(context.Context, *sql.Tx, int64) error,
) (resources.MutationResult[Donation], error) {
	tx, err := s.beginOwnerTx(ctx, userID)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	decision, err := beginMutation(ctx, tx, "user", userID, idempotency.ScopeDonation, mutation, now)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[Donation](decision)
	}
	if err := apply(ctx, tx, now); err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	value, err := getOwnerDonationTx(ctx, tx, userID, donationID, now)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, value)
	if err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return resources.MutationResult[Donation]{}, err
	}
	return out, nil
}

type reviewerRole string

const (
	reviewerAdmin   reviewerRole = "admin"
	reviewerSteward reviewerRole = "level5"
)

func (s *Service) ReviewAdmin(
	ctx context.Context,
	mutation resources.ControlMutation,
	donationID int64,
	input ReviewInput,
) (resources.MutationResult[AdminDonation], error) {
	if !validMutation(mutation, http.MethodPost, routeAdminReview, donationID) {
		return resources.MutationResult[AdminDonation]{}, ErrInvalidRequest
	}
	result, err := s.review(ctx, reviewerAdmin, 0, donationID, mutation, input)
	return result.adminResult(), err
}

func (s *Service) ReviewSteward(
	ctx context.Context,
	actorUserID, donationID int64,
	mutation resources.ControlMutation,
	input ReviewInput,
) (resources.MutationResult[StewardDonation], error) {
	if actorUserID <= 0 || !validMutation(mutation, http.MethodPost, routeStewardReview, donationID) {
		return resources.MutationResult[StewardDonation]{}, ErrInvalidRequest
	}
	result, err := s.review(ctx, reviewerSteward, actorUserID, donationID, mutation, input)
	return result.stewardResult(), err
}

type roleDonationMutation struct {
	admin    AdminDonation
	steward  StewardDonation
	status   int
	body     []byte
	replayed bool
}

func (result roleDonationMutation) adminResult() resources.MutationResult[AdminDonation] {
	return resources.MutationResult[AdminDonation]{Value: result.admin, Status: result.status,
		Body: append([]byte(nil), result.body...), Replayed: result.replayed}
}

func (result roleDonationMutation) stewardResult() resources.MutationResult[StewardDonation] {
	return resources.MutationResult[StewardDonation]{Value: result.steward, Status: result.status,
		Body: append([]byte(nil), result.body...), Replayed: result.replayed}
}

func (s *Service) review(
	ctx context.Context,
	role reviewerRole,
	actorUserID, donationID int64,
	mutation resources.ControlMutation,
	input ReviewInput,
) (roleDonationMutation, error) {
	if s == nil || ctx == nil || donationID <= 0 || !validReviewInput(input) {
		return roleDonationMutation{}, ErrInvalidRequest
	}
	tx, actorID, err := s.beginRoleTx(ctx, role, actorUserID)
	if err != nil {
		return roleDonationMutation{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := s.nowUnix()
	if err != nil {
		return roleDonationMutation{}, err
	}
	if role == reviewerSteward {
		if err := requireStewardDonationOwnershipTx(ctx, tx, donationID, actorID); err != nil {
			return roleDonationMutation{}, err
		}
	}
	decision, err := beginMutation(ctx, tx, string(role), actorID, idempotency.ScopeControlMutation, mutation, now)
	if err != nil {
		return roleDonationMutation{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayRoleDonation(decision, role)
	}
	expiry, err := materializeDonationExpiryStateTx(ctx, tx, donationID, now)
	if err != nil {
		return roleDonationMutation{}, err
	}
	if expiry.changed {
		if err := tx.Rollback(); err != nil {
			return roleDonationMutation{}, fmt.Errorf("donation: roll back expired review intent: %w", err)
		}
		committed = true
		if err := s.materializeRoleExpiryStandalone(ctx, role, actorID, donationID, now); err != nil {
			return roleDonationMutation{}, err
		}
		return roleDonationMutation{}, ErrConflict
	}
	if err := reviewDonationTx(ctx, tx, donationID, actorID, string(role), input, now); err != nil {
		return roleDonationMutation{}, err
	}
	value, err := getAdminDonationTx(ctx, tx, donationID, now)
	if err != nil {
		return roleDonationMutation{}, err
	}
	out, err := finishRoleDonation(ctx, tx, decision, role, value)
	if err != nil {
		return roleDonationMutation{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return roleDonationMutation{}, err
	}
	return out, nil
}

func reviewDonationTx(ctx context.Context, tx *sql.Tx, donationID, actorID int64, role string, input ReviewInput, now int64) error {
	var status string
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT status,revision FROM donations WHERE id=?`, donationID).Scan(&status, &revision); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("donation: read review state: %w", err)
	}
	if revision != input.ExpectedRevision || status != "pending" && !(status == "approved" && input.Decision == "approve") {
		return ErrConflict
	}
	if input.Decision == "reject" {
		if status != "pending" {
			return ErrConflict
		}
		if err := clearDonationMembershipsTx(ctx, tx, donationID, "terminated", now); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE donations SET status='rejected',revision=revision+1,
review_note=?,reviewed_by_user_id=?,reviewed_by_role=?,reviewed_at=?,updated_at=?,terminal_at=?
WHERE id=? AND status='pending' AND revision=?`, input.Reason, actorID, role, now, now, now, donationID, revision)
		if err != nil {
			return fmt.Errorf("donation: reject submission: %w", err)
		}
		if err := requireOne(result); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO donation_reviews(
donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,?,?,?, 'reject',?,?)`, donationID, revision+1, actorID, role, input.Reason, now)
		return err
	}

	if err := applyApprovalKeySettingsTx(ctx, tx, donationID, input.KeySettings, now); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE donations SET status='approved',revision=revision+1,
review_note=?,reviewed_by_user_id=?,reviewed_by_role=?,reviewed_at=?,updated_at=?,terminal_at=NULL
WHERE id=? AND status=? AND revision=?`, input.Reason, actorID, role, now, now, donationID, status, revision)
	if err != nil {
		return fmt.Errorf("donation: approve submission: %w", err)
	}
	if err := requireOne(result); err != nil {
		return err
	}
	action := "approve"
	if status == "approved" {
		action = "limit_update"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO donation_reviews(
donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,?,?,?,?,?,?)`, donationID, revision+1, actorID, role, action, input.Reason, now); err != nil {
		return fmt.Errorf("donation: record approval: %w", err)
	}
	_, err = materializeDonationExpiryTx(ctx, tx, donationID, now)
	return err
}

func applyApprovalKeySettingsTx(
	ctx context.Context,
	tx *sql.Tx,
	donationID int64,
	keySettings []KeySetting,
	now int64,
) error {
	settings, err := validateReviewSettings(ctx, tx, donationID, keySettings)
	if err != nil {
		return err
	}
	var suspended int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM donation_key_memberships m JOIN endpoint_key_suspensions s ON s.endpoint_key_id=m.endpoint_key_id
WHERE m.donation_id=?)`, donationID).Scan(&suspended); err != nil {
		return fmt.Errorf("donation: inspect approval suspension: %w", err)
	}
	if suspended != 0 {
		return ErrConflict
	}
	for _, setting := range settings {
		result, err := tx.ExecContext(ctx, `UPDATE donation_keys SET
price_limit_mag=?,call_limit_mag=?,token_limit_mag=?,token_reserve=?,enabled=?,safe_note=?,expires_at=?,updated_at=?
WHERE id=? AND donation_id=? AND ended_at IS NULL`, setting.priceLimit, setting.callLimit, setting.tokenLimit,
			setting.tokenReserve, boolInt(setting.enabled), setting.safeNote, setting.expiresAt, now, setting.id, donationID)
		if err != nil {
			return fmt.Errorf("donation: apply approval key settings: %w", err)
		}
		if err := requireOne(result); err != nil {
			return err
		}
	}
	return nil
}

type validatedKeySetting struct {
	id                                int64
	priceLimit, callLimit, tokenLimit any
	tokenReserve                      int64
	enabled                           bool
	safeNote                          string
	expiresAt                         any
}

func validateReviewSettings(ctx context.Context, tx *sql.Tx, donationID int64, settings []KeySetting) ([]validatedKeySetting, error) {
	if len(settings) < 1 || len(settings) > maxDonationKeys {
		return nil, ErrInvalidRequest
	}
	seen := make(map[int64]struct{}, len(settings))
	validated := make([]validatedKeySetting, 0, len(settings))
	for _, setting := range settings {
		if setting.DonationKeyID <= 0 || setting.TokenReserve < 0 || setting.TokenReserve > 2147483647 ||
			!validSafeNote(setting.SafeNote) || !validExpiryValue(setting.ExpiresAt) {
			return nil, ErrInvalidRequest
		}
		if _, exists := seen[setting.DonationKeyID]; exists {
			return nil, ErrInvalidRequest
		}
		seen[setting.DonationKeyID] = struct{}{}
		price, err := parseNullableAmountLimit(setting.PriceLimit)
		if err != nil {
			return nil, ErrInvalidRequest
		}
		calls, err := parseNullableU128(setting.CallsLimit)
		if err != nil {
			return nil, ErrInvalidRequest
		}
		tokens, err := parseNullableU128(setting.TokensLimit)
		if err != nil {
			return nil, ErrInvalidRequest
		}
		validated = append(validated, validatedKeySetting{
			id: setting.DonationKeyID, priceLimit: price, callLimit: calls, tokenLimit: tokens,
			tokenReserve: setting.TokenReserve, enabled: setting.Enabled, safeNote: setting.SafeNote,
			expiresAt: nullableInt64Argument(setting.ExpiresAt),
		})
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,authorized_expires_at FROM donation_keys WHERE donation_id=? AND ended_at IS NULL ORDER BY id`, donationID)
	if err != nil {
		return nil, fmt.Errorf("donation: read review key set: %w", err)
	}
	defer rows.Close()
	current := make([]int64, 0, len(settings))
	for rows.Next() {
		var id int64
		var authorized sql.NullInt64
		if err := rows.Scan(&id, &authorized); err != nil {
			return nil, fmt.Errorf("donation: scan review key set: %w", err)
		}
		var requested *int64
		for _, setting := range settings {
			if setting.DonationKeyID == id {
				requested = setting.ExpiresAt
				break
			}
		}
		if !effectiveExpiryWithinAuthorization(requested, authorized) {
			return nil, ErrInvalidRequest
		}
		current = append(current, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("donation: iterate review key set: %w", err)
	}
	if len(current) != len(seen) {
		return nil, ErrConflict
	}
	for _, id := range current {
		if _, exists := seen[id]; !exists {
			return nil, ErrConflict
		}
	}
	return validated, nil
}

func (s *Service) ManageKeyAdmin(
	ctx context.Context,
	donationID, keyID int64,
	mutation resources.ControlMutation,
	input KeyManagementInput,
) (resources.MutationResult[AdminDonation], error) {
	if !validMutation(mutation, http.MethodPatch, routeAdminKey, donationID, keyID) {
		return resources.MutationResult[AdminDonation]{}, ErrInvalidRequest
	}
	result, err := s.manageKey(ctx, reviewerAdmin, 0, donationID, keyID, mutation, input)
	return result.adminResult(), err
}

func (s *Service) ManageKeySteward(
	ctx context.Context,
	actorUserID, donationID, keyID int64,
	mutation resources.ControlMutation,
	input KeyManagementInput,
) (resources.MutationResult[StewardDonation], error) {
	if actorUserID <= 0 || !validMutation(mutation, http.MethodPatch, routeStewardKey, donationID, keyID) {
		return resources.MutationResult[StewardDonation]{}, ErrInvalidRequest
	}
	result, err := s.manageKey(ctx, reviewerSteward, actorUserID, donationID, keyID, mutation, input)
	return result.stewardResult(), err
}

func (s *Service) manageKey(
	ctx context.Context,
	role reviewerRole,
	actorUserID, donationID, keyID int64,
	mutation resources.ControlMutation,
	input KeyManagementInput,
) (roleDonationMutation, error) {
	if s == nil || ctx == nil || donationID <= 0 || keyID <= 0 || !validKeyManagement(input) {
		return roleDonationMutation{}, ErrInvalidRequest
	}
	tx, actorID, err := s.beginRoleTx(ctx, role, actorUserID)
	if err != nil {
		return roleDonationMutation{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := s.nowUnix()
	if err != nil {
		return roleDonationMutation{}, err
	}
	if role == reviewerSteward {
		if err := requireStewardDonationOwnershipTx(ctx, tx, donationID, actorID); err != nil {
			return roleDonationMutation{}, err
		}
	}
	decision, err := beginMutation(ctx, tx, string(role), actorID, idempotency.ScopeControlMutation, mutation, now)
	if err != nil {
		return roleDonationMutation{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayRoleDonation(decision, role)
	}
	expiry, err := materializeDonationExpiryStateTx(ctx, tx, donationID, now)
	if err != nil {
		return roleDonationMutation{}, err
	}
	if expiry.changed {
		if err := tx.Rollback(); err != nil {
			return roleDonationMutation{}, fmt.Errorf("donation: roll back expired key intent: %w", err)
		}
		committed = true
		if err := s.materializeRoleExpiryStandalone(ctx, role, actorID, donationID, now); err != nil {
			return roleDonationMutation{}, err
		}
		return roleDonationMutation{}, ErrConflict
	}
	var status string
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT status,revision FROM donations WHERE id=?`, donationID).Scan(&status, &revision); errors.Is(err, sql.ErrNoRows) {
		return roleDonationMutation{}, ErrNotFound
	} else if err != nil {
		return roleDonationMutation{}, fmt.Errorf("donation: read key management state: %w", err)
	}
	if status != "approved" || revision != input.ExpectedRevision {
		return roleDonationMutation{}, ErrConflict
	}
	if err := manageDonationKeyTx(ctx, tx, donationID, keyID, actorID, string(role), input, now); err != nil {
		return roleDonationMutation{}, err
	}
	value, err := getAdminDonationTx(ctx, tx, donationID, now)
	if err != nil {
		return roleDonationMutation{}, err
	}
	out, err := finishRoleDonation(ctx, tx, decision, role, value)
	if err != nil {
		return roleDonationMutation{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return roleDonationMutation{}, err
	}
	return out, nil
}

func replayRoleDonation(decision idempotency.Decision, role reviewerRole) (roleDonationMutation, error) {
	result := roleDonationMutation{status: decision.HTTPStatus, body: append([]byte(nil), decision.ResponseBody...), replayed: true}
	if role == reviewerSteward {
		if err := json.Unmarshal(decision.ResponseBody, &result.steward); err != nil {
			return roleDonationMutation{}, ErrInvariant
		}
		return result, nil
	}
	if err := json.Unmarshal(decision.ResponseBody, &result.admin); err != nil {
		return roleDonationMutation{}, ErrInvariant
	}
	return result, nil
}

func finishRoleDonation(ctx context.Context, tx *sql.Tx, decision idempotency.Decision, role reviewerRole, value AdminDonation) (roleDonationMutation, error) {
	if role == reviewerSteward {
		steward := stewardFromAdmin(value)
		out, err := finishJSON(ctx, tx, decision, http.StatusOK, steward)
		if err != nil {
			return roleDonationMutation{}, err
		}
		return roleDonationMutation{steward: out.Value, status: out.Status, body: out.Body}, nil
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, value)
	if err != nil {
		return roleDonationMutation{}, err
	}
	return roleDonationMutation{admin: out.Value, status: out.Status, body: out.Body}, nil
}

func manageDonationKeyTx(ctx context.Context, tx *sql.Tx, donationID, keyID, actorID int64, role string, input KeyManagementInput, now int64) error {
	var currentEnabled, failureDisabled int
	var generationBlob []byte
	var authorizedExpiry sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT enabled,failure_disabled,streak_generation,authorized_expires_at FROM donation_keys
WHERE id=? AND donation_id=? AND ended_at IS NULL
AND EXISTS(SELECT 1 FROM donation_key_memberships m WHERE m.donation_key_id=donation_keys.id)`, keyID, donationID).Scan(&currentEnabled, &failureDisabled, &generationBlob, &authorizedExpiry); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("donation: read managed key: %w", err)
	}
	updates := []string{"updated_at=?"}
	args := []any{now}
	action := "limit_update"
	if input.Enabled != nil {
		updates = append(updates, "enabled=?")
		args = append(args, boolInt(*input.Enabled))
		if *input.Enabled {
			action = "enable"
		} else {
			action = "disable"
		}
	}
	appendLimit := func(column string, value **string, amount bool) error {
		if value == nil {
			return nil
		}
		var encoded any
		var err error
		if amount {
			encoded, err = parseNullableAmountLimit(*value)
		} else {
			encoded, err = parseNullableU128(*value)
		}
		if err != nil {
			return err
		}
		updates = append(updates, column+"=?")
		args = append(args, encoded)
		return nil
	}
	if err := appendLimit("price_limit_mag", input.PriceLimit, true); err != nil {
		return ErrInvalidRequest
	}
	if err := appendLimit("call_limit_mag", input.CallsLimit, false); err != nil {
		return ErrInvalidRequest
	}
	if err := appendLimit("token_limit_mag", input.TokensLimit, false); err != nil {
		return ErrInvalidRequest
	}
	if input.TokenReserve != nil {
		updates = append(updates, "token_reserve=?")
		args = append(args, *input.TokenReserve)
	}
	if input.SafeNote != nil {
		updates = append(updates, "safe_note=?")
		args = append(args, *input.SafeNote)
		action = "note_update"
	}
	if input.ExpiresAt != nil {
		if !effectiveExpiryWithinAuthorization(*input.ExpiresAt, authorizedExpiry) {
			return ErrInvalidRequest
		}
		updates = append(updates, "expires_at=?")
		args = append(args, nullableInt64Argument(*input.ExpiresAt))
	}
	newGeneration := input.ResetFailureStreak || input.Enabled != nil && *input.Enabled && (currentEnabled == 0 || failureDisabled == 1)
	if newGeneration {
		generation, err := db.DecodeU128(generationBlob)
		if err != nil {
			return ErrInvariant
		}
		generation, err = incrementU128(generation)
		if err != nil {
			return err
		}
		zero := db.EncodeU128(db.U128{})
		one := db.U128{}
		one[15] = 1
		updates = append(updates, "streak_generation=?", "failure_streak=?", "next_claim_seq=?", "next_fold_seq=?", "failure_disabled=0")
		args = append(args, db.EncodeU128(generation), zero, db.EncodeU128(one), db.EncodeU128(one))
	}
	args = append(args, keyID, donationID)
	result, err := tx.ExecContext(ctx, `UPDATE donation_keys SET `+strings.Join(updates, ",")+`
WHERE id=? AND donation_id=? AND ended_at IS NULL`, args...)
	if err != nil {
		return fmt.Errorf("donation: update managed key: %w", err)
	}
	if err := requireOne(result); err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, `UPDATE donations SET revision=revision+1,updated_at=?
WHERE id=? AND status='approved' AND revision=?`, now, donationID, input.ExpectedRevision)
	if err != nil {
		return fmt.Errorf("donation: advance managed revision: %w", err)
	}
	if err := requireOne(result); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO donation_reviews(
donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,?,?,?,?,?,?)`, donationID, input.ExpectedRevision+1, actorID, role, action, "", now)
	if err != nil {
		return fmt.Errorf("donation: record key management: %w", err)
	}
	_, err = materializeDonationExpiryTx(ctx, tx, donationID, now)
	return err
}

func (s *Service) beginOwnerTx(ctx context.Context, userID int64) (*sql.Tx, error) {
	if s == nil || s.db == nil || userID <= 0 || nilDependency(s.ownerAuth) {
		return nil, ErrUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("donation: begin owner transaction: %w", err)
	}
	if err := s.ownerAuth.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		_ = tx.Rollback()
		return nil, mapAuthorization(err)
	}
	return tx, nil
}

func (s *Service) beginRoleTx(ctx context.Context, role reviewerRole, actorUserID int64) (*sql.Tx, int64, error) {
	if s == nil || s.db == nil || nilDependency(s.roleAuth) {
		return nil, 0, ErrUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("donation: begin role transaction: %w", err)
	}
	rollback := func(err error) (*sql.Tx, int64, error) {
		_ = tx.Rollback()
		return nil, 0, err
	}
	switch role {
	case reviewerAdmin:
		if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin=1`).Scan(&actorUserID); errors.Is(err, sql.ErrNoRows) {
			return rollback(ErrForbidden)
		} else if err != nil {
			return rollback(fmt.Errorf("donation: read singleton admin: %w", err))
		}
		if err := s.roleAuth.AuthorizeAdminMutation(ctx, tx, actorUserID); err != nil {
			return rollback(mapAuthorization(err))
		}
	case reviewerSteward:
		if actorUserID <= 0 {
			return rollback(ErrInvalidRequest)
		}
		if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, actorUserID); err != nil {
			return rollback(mapAuthorization(err))
		}
	default:
		return rollback(ErrInvalidRequest)
	}
	return tx, actorUserID, nil
}

func requireStewardDonationOwnershipTx(ctx context.Context, tx *sql.Tx, donationID, actorUserID int64) error {
	var owned int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM donations WHERE id=? AND user_id=?`, donationID, actorUserID).Scan(&owned)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("donation: verify steward donation ownership: %w", err)
	}
	if owned != 1 {
		return ErrInvariant
	}
	return nil
}

func (s *Service) materializeRoleExpiryStandalone(
	ctx context.Context,
	role reviewerRole,
	actorUserID, donationID, now int64,
) error {
	tx, actorID, err := s.beginRoleTx(ctx, role, actorUserID)
	if err != nil {
		return err
	}
	committed := false
	defer finishTx(tx, &committed)
	if role == reviewerSteward {
		if err := requireStewardDonationOwnershipTx(ctx, tx, donationID, actorID); err != nil {
			return err
		}
	}
	if _, err := materializeDonationExpiryTx(ctx, tx, donationID, now); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

type submissionKey struct {
	id                             int64
	head, tail, baseURL, connector string
	expiresAt                      *int64
	channelID, channelName         sql.NullString
	channelRevision                sql.NullInt64
	channelCategory                sql.NullString
	reportFingerprint              []byte
}

func submissionAutoApproval(keys []submissionKey) (bool, error) {
	if len(keys) == 0 {
		return false, ErrInvalidRequest
	}
	mainstream := keys[0].channelID.Valid
	channelID := keys[0].channelID.String
	for _, key := range keys {
		completeSnapshot := key.channelID.Valid && key.channelRevision.Valid && key.channelName.Valid && key.channelCategory.Valid
		customSnapshot := !key.channelID.Valid && !key.channelRevision.Valid && !key.channelName.Valid && !key.channelCategory.Valid
		if !completeSnapshot && !customSnapshot {
			return false, ErrInvariant
		}
		if key.channelID.Valid != mainstream {
			return false, ErrInvalidRequest
		}
		if mainstream && key.channelID.String != channelID {
			return false, ErrInvalidRequest
		}
	}
	return mainstream, nil
}

func validateSubmissionKeys(ctx context.Context, tx *sql.Tx, userID int64, inputs []CreateKeyInput) ([]submissionKey, error) {
	ids := make([]int64, len(inputs))
	expiries := make(map[int64]*int64, len(inputs))
	for index, input := range inputs {
		ids[index] = input.EndpointKeyID
		expiries[input.EndpointKeyID] = input.ExpiresAt
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, userID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, `SELECT k.id,k.display_head,k.display_tail,e.base_url,e.connector_type,
k.secret_fingerprint,e.mainstream_channel_id,e.mainstream_channel_revision,
e.mainstream_channel_name,e.mainstream_channel_category
FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id
WHERE e.user_id=? AND k.id IN (`+placeholders+`)
AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)
AND NOT EXISTS(SELECT 1 FROM donation_key_memberships m WHERE m.endpoint_key_id=k.id)
ORDER BY k.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("donation: validate submission keys: %w", err)
	}
	defer rows.Close()
	keys := make([]submissionKey, 0, len(ids))
	for rows.Next() {
		var key submissionKey
		if err := rows.Scan(&key.id, &key.head, &key.tail, &key.baseURL, &key.connector,
			&key.reportFingerprint, &key.channelID, &key.channelRevision, &key.channelName, &key.channelCategory); err != nil {
			return nil, fmt.Errorf("donation: scan submission key: %w", err)
		}
		key.expiresAt = expiries[key.id]
		key.reportFingerprint = append([]byte(nil), key.reportFingerprint...)
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("donation: iterate submission keys: %w", err)
	}
	if len(keys) != len(ids) {
		return nil, ErrNotFound
	}
	return keys, nil
}

func beginMutation(ctx context.Context, tx *sql.Tx, actorKind string, actorID int64, scope idempotency.Scope, mutation resources.ControlMutation, now int64) (idempotency.Decision, error) {
	actorHash, err := idempotency.ActorScopeHash(actorKind, strconv.FormatInt(actorID, 10))
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actorHash, Method: mutation.Method, Route: mutation.Route,
		PathResourceIDs: mutation.PathIDs, Query: mutation.Query, Body: mutation.CanonicalBody,
	})
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: scope, ActorHash: actorHash, Key: mutation.IdempotencyKey, RequestHash: digest, DecisionNow: now,
	})
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		return idempotency.Decision{}, ErrConflict
	}
	if err != nil {
		return idempotency.Decision{}, fmt.Errorf("donation: accept idempotency record: %w", err)
	}
	return decision, nil
}

func replay[T any](decision idempotency.Decision) (resources.MutationResult[T], error) {
	var value T
	if len(decision.ResponseBody) != 0 {
		if err := json.Unmarshal(decision.ResponseBody, &value); err != nil {
			return resources.MutationResult[T]{}, ErrInvariant
		}
	}
	return resources.MutationResult[T]{Value: value, Status: decision.HTTPStatus,
		Body: append([]byte(nil), decision.ResponseBody...), Replayed: true}, nil
}

func finishJSON[T any](ctx context.Context, tx *sql.Tx, decision idempotency.Decision, status int, value T) (resources.MutationResult[T], error) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > idempotency.MaxResponseBytes {
		return resources.MutationResult[T]{}, ErrResourceLimit
	}
	if err := idempotency.Complete(ctx, tx, decision, status, body); err != nil {
		return resources.MutationResult[T]{}, fmt.Errorf("donation: complete idempotency record: %w", err)
	}
	return resources.MutationResult[T]{Value: value, Status: status, Body: body}, nil
}

func validMutation(input resources.ControlMutation, method, route string, ids ...int64) bool {
	if input.Method != method || input.Route != route || input.Query != "" || len(input.PathIDs) != len(ids) {
		return false
	}
	for index, id := range ids {
		if id <= 0 || input.PathIDs[index] != strconv.FormatInt(id, 10) {
			return false
		}
	}
	return true
}

func validReviewInput(input ReviewInput) bool {
	if input.ExpectedRevision <= 0 || !validDonationText(input.Reason) || input.Decision != "approve" && input.Decision != "reject" {
		return false
	}
	if input.Decision == "reject" {
		return len(input.KeySettings) == 0
	}
	return len(input.KeySettings) >= 1 && len(input.KeySettings) <= maxDonationKeys
}

func validKeyManagement(input KeyManagementInput) bool {
	if input.ExpectedRevision <= 0 || input.Enabled == nil && input.PriceLimit == nil && input.CallsLimit == nil &&
		input.TokensLimit == nil && input.TokenReserve == nil && input.SafeNote == nil && input.ExpiresAt == nil && !input.ResetFailureStreak {
		return false
	}
	if input.TokenReserve != nil && (*input.TokenReserve < 0 || *input.TokenReserve > 2147483647) ||
		input.SafeNote != nil && !validSafeNote(*input.SafeNote) ||
		input.ExpiresAt != nil && !validExpiryValue(*input.ExpiresAt) {
		return false
	}
	return true
}

func validExpiryValue(value *int64) bool {
	return value == nil || *value >= 0 && *value <= maxUnixSecond
}

func effectiveExpiryWithinAuthorization(value *int64, authorized sql.NullInt64) bool {
	if !validExpiryValue(value) {
		return false
	}
	if !authorized.Valid {
		return true
	}
	return value != nil && *value <= authorized.Int64
}

func nullableInt64Argument(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func validUniqueIDs(ids []int64, minimum, maximum int) bool {
	if len(ids) < minimum || len(ids) > maximum {
		return false
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return false
		}
		if _, exists := seen[id]; exists {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func validCreateKeys(keys []CreateKeyInput) bool {
	if len(keys) < 1 || len(keys) > maxDonationKeys {
		return false
	}
	seen := make(map[int64]struct{}, len(keys))
	for _, key := range keys {
		if key.EndpointKeyID <= 0 {
			return false
		}
		if _, exists := seen[key.EndpointKeyID]; exists {
			return false
		}
		seen[key.EndpointKeyID] = struct{}{}
	}
	return true
}

func validDonationText(value string) bool {
	return validBoundedText(value, maxDonationTextRunes)
}

func validSafeNote(value string) bool {
	return validBoundedText(value, maxSafeNoteRunes)
}

func validBoundedText(value string, maximum int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maximum {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r >= 0x7f && r <= 0x9f {
			return false
		}
	}
	return true
}

func parseNullableAmountLimit(value *string) (any, error) {
	if value == nil {
		return nil, nil
	}
	milli, err := parseWireAmount(*value)
	if err != nil {
		return nil, err
	}
	wide, err := db.U128FromBig(newBigInt(milli))
	if err != nil {
		return nil, err
	}
	return db.EncodeU128(wide), nil
}

func parseNullableU128(value *string) (any, error) {
	if value == nil {
		return nil, nil
	}
	wide, err := db.ParseU128Decimal(*value)
	if err != nil || wide.Big().Cmp(big.NewInt(db.MaxMoneyMilli)) > 0 {
		return nil, ErrInvalidRequest
	}
	return db.EncodeU128(wide), nil
}

func parseWireAmount(value string) (int64, error) {
	if value == "" || len(value) > 32 || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return 0, ErrInvalidRequest
	}
	whole, fraction := value, ""
	if index := strings.IndexByte(value, '.'); index >= 0 {
		if strings.Contains(value[index+1:], ".") {
			return 0, ErrInvalidRequest
		}
		whole, fraction = value[:index], value[index+1:]
		if fraction == "" || len(fraction) > 3 {
			return 0, ErrInvalidRequest
		}
	}
	if whole == "" || len(whole) > 1 && whole[0] == '0' || !asciiDigits(whole) || !asciiDigits(fraction) {
		return 0, ErrInvalidRequest
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, ErrInvalidRequest
	}
	for len(fraction) < 3 {
		fraction += "0"
	}
	f := int64(0)
	if fraction != "" {
		f, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, ErrInvalidRequest
		}
	}
	if w > (db.MaxMoneyMilli-f)/1000 {
		return 0, ErrInvalidRequest
	}
	return w*1000 + f, nil
}

func asciiDigits(value string) bool {
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func configBool(ctx context.Context, tx *sql.Tx, key string) (bool, error) {
	var value string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&value); err != nil {
		return false, fmt.Errorf("donation: read feature gate: %w", err)
	}
	if value != "0" && value != "1" {
		return false, ErrInvariant
	}
	return value == "1", nil
}

func requireOne(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("donation: inspect state transition: %w", err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func finishTx(tx *sql.Tx, committed *bool) {
	if tx != nil && committed != nil && !*committed {
		_ = tx.Rollback()
	}
}

func commitTx(tx *sql.Tx, committed *bool) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("donation: commit transaction: %w", err)
	}
	*committed = true
	return nil
}

func classifyWrite(operation string, err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "unique constraint") || strings.Contains(lower, "constraint failed") {
		return ErrConflict
	}
	return fmt.Errorf("donation: %s: %w", operation, err)
}

func mapAuthorization(err error) error {
	if err == nil {
		return nil
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "unauthorized") || strings.Contains(text, "authentication") {
		return ErrUnauthorized
	}
	return ErrForbidden
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func newBigInt(value int64) *big.Int { return big.NewInt(value) }
