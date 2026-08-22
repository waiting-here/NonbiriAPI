package db

// Donation repository: user-submitted charity donations, their per-key
// charity attributes, the append-only review audit, and the atomic physical
// endpoint-key claims (frozen §J/§L, implementation contract §2.6).
//
// Frozen semantics implemented here:
//
//   - one donation = one owned endpoint + at least one key; the review
//     decision covers the WHOLE donation; an approved donation is enabled by
//     default; deletion is a soft delete that keeps the bounded audit trail;
//   - every key of a pending donation, and every enabled key of an
//     approved+enabled donation, MUST hold the physical claim. The claim set
//     is re-synchronized inside EVERY transaction that touches status,
//     enabled flags or expiry — never as a follow-up write;
//   - acquisition is a constrained INSERT decided by SQLite's primary key on
//     donation_key_claims.endpoint_key_id: a conflicting physical key yields
//     ErrDonationKeyClaimConflict. Nothing is ever merged and no per-key
//     limit is stacked; there is no read-then-claim anywhere;
//   - terminal records keep only safe snapshots (base URL, head/tail display
//     fragments): no note, no secret, no ciphertext ever enters this layer;
//   - normal endpoint/key deletion refuses while a pending or enabled
//     donation references the resource; account deletion removes claims
//     first, then lets the cascade remove everything (no routable secret is
//     kept alive by a claim).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Donation statuses (closed enum; CHECK-enforced in schema and re-validated
// here so an unknown value can never be interpreted as a default).
const (
	DonationPending  = "pending"
	DonationApproved = "approved"
	DonationRejected = "rejected"
	DonationDeleted  = "deleted"
)

// Review roles. The empty role marks a system-driven transition (the lazy
// expiry disable); it can never be forged through an API path because every
// API entry point passes "admin" or "level5" explicitly.
const (
	ReviewRoleAdmin  = "admin"
	ReviewRoleLevel5 = "level5"
	ReviewRoleSystem = ""
)

// Review actions (closed set frozen in the implementation contract).
const (
	ReviewActionApprove = "approve"
	ReviewActionReject  = "reject"
	ReviewActionEnable  = "enable"
	ReviewActionDisable = "disable"
	ReviewActionUpdate  = "update"
	ReviewActionDelete  = "delete"
)

// Text bounds (implementation contract §1.3: statements/review notes at most
// 1,024 runes). Key notes reuse the short-note bound.
const (
	MaxDonationDescriptionRunes = 1024
	MaxReviewNoteRunes          = 1024
)

// Numeric bounds for the donor/reviewer-settable per-key limits. They mirror
// the platform-wide concurrency/RPM ceilings; 0 keeps the "unlimited" meaning.
const (
	MaxDonationKeyConcurrency = 100000
	MaxDonationKeyRPM         = 4096
)

// Donation is one submitted charity offer. It carries no secret material:
// EndpointBaseURL is a bounded canonical snapshot the donor supplied anyway.
type Donation struct {
	ID               int64
	UserID           int64
	EndpointID       *int64
	EndpointBaseURL  string
	Status           string
	Enabled          bool
	Description      string
	ReviewNote       string
	ReviewedByUserID *int64
	ReviewedByRole   string
	ExpiresAt        *int64
	ReviewedAt       *int64
	CreatedAt        int64
	UpdatedAt        int64
}

// DonationKey is the per-key charity attribute row of one donation. Only
// persisted display fragments identify the underlying physical key.
type DonationKey struct {
	ID              int64
	DonationID      int64
	EndpointKeyID   *int64
	DisplayHead     string
	DisplayTail     string
	MaxConcurrency  int64
	RPMLimit        int64
	CreditsUsageCap int64
	CreditsUsed     int64
	CreditsReserved int64
	Enabled         bool
	CreatedAt       int64
	UpdatedAt       int64
}

// DonationReview is one append-only audit entry.
type DonationReview struct {
	ID             int64
	DonationID     int64
	ReviewerUserID *int64
	ReviewerRole   string
	Action         string
	Note           string
	CreatedAt      int64
}

func validDonationStatus(s string) bool {
	switch s {
	case DonationPending, DonationApproved, DonationRejected, DonationDeleted:
		return true
	}
	return false
}

// donationActive reports whether the donation currently holds claims over its
// keys: pending always; approved only while enabled.
func donationActive(d *Donation) bool {
	if d.Status == DonationPending {
		return true
	}
	return d.Status == DonationApproved && d.Enabled
}

// validateBoundedStatement enforces the shared statement bound with control-
// character rejection (same policy as ledger reasons).
func validateBoundedStatement(value string, maxRunes int) error {
	if len(value) > maxRunes*4 || !utf8.ValidString(value) {
		return errors.New("donation statement is invalid")
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return errors.New("donation statement is too long")
	}
	for _, r := range value {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return errors.New("donation statement contains control characters")
		}
	}
	return nil
}

func validateDonationLimits(maxConcurrency, rpmLimit int64) error {
	if maxConcurrency < 0 || maxConcurrency > MaxDonationKeyConcurrency {
		return errors.New("max_concurrency out of range")
	}
	if rpmLimit < 0 || rpmLimit > MaxDonationKeyRPM {
		return errors.New("rpm_limit out of range")
	}
	return nil
}

// ExistingEndpointKeys selects already-saved keys of one owned endpoint.
type ExistingEndpointKeys struct {
	EndpointID int64
	KeyIDs     []int64 // at least one; all must belong to EndpointID and the caller
}

// NewKeySpec describes one freshly entered key of a nested creation. Secret is
// consumed and cleared by the repository; Note/fragments are bounded text.
type NewKeySpec struct {
	Secret         []byte
	Note           string
	Enabled        bool
	DisplayHead    string
	DisplayTail    string
	MaxConcurrency int64
	RPMLimit       int64
}

// NewEndpointSpec describes the personal endpoint created atomically together
// with a nested donation. BaseURL must already be canonicalized and the
// connector type validated by the service layer; the repository persists them
// verbatim.
type NewEndpointSpec struct {
	ConnectorType string
	BaseURL       string
	Note          string
	Enabled       bool
}

// KeyLimitSpec carries the per-key charity limits for one selected existing
// key (limits are donation-scoped attributes even for donated own keys).
type KeyLimitSpec struct {
	EndpointKeyID  int64
	MaxConcurrency int64
	RPMLimit       int64
}

// CreateDonationInput is the full submission payload for both forms. Exactly
// one mode must be present: Existing (endpoint + selected saved keys) or New
// (nested endpoint + fresh secrets). Description is required. ExpiresAt nil
// means "never expires". Now is the caller-supplied unix timestamp.
type CreateDonationInput struct {
	UserID      int64
	Description string
	ExpiresAt   *int64
	Now         int64

	Existing *ExistingEndpointKeys
	New      *NewEndpointSpec

	Limits []KeyLimitSpec // mode A: limits per selected key id
	Keys   []NewKeySpec   // mode B: fresh keys
}

// validateCreateDonationInput performs the mode-independent structural checks
// before any transaction opens.
func validateCreateDonationInput(in CreateDonationInput) error {
	if in.UserID <= 0 {
		return ErrNotFound
	}
	if in.Now <= 0 {
		return fmt.Errorf("%w: donation timestamp is required", ErrInvalidValue)
	}
	if in.ExpiresAt != nil && *in.ExpiresAt <= in.Now {
		return fmt.Errorf("%w: expires_at must be in the future", ErrInvalidValue)
	}
	if err := validateBoundedStatement(in.Description, MaxDonationDescriptionRunes); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidValue, err)
	}
	switch {
	case in.Existing != nil && in.New == nil:
		if in.Existing.EndpointID <= 0 || len(in.Existing.KeyIDs) == 0 {
			return fmt.Errorf("%w: a donation requires an endpoint and at least one key", ErrInvalidValue)
		}
		seen := make(map[int64]struct{}, len(in.Existing.KeyIDs))
		for _, id := range in.Existing.KeyIDs {
			if id <= 0 {
				return fmt.Errorf("%w: invalid endpoint key id", ErrInvalidValue)
			}
			if _, dup := seen[id]; dup {
				return fmt.Errorf("%w: duplicate endpoint key selection", ErrInvalidValue)
			}
			seen[id] = struct{}{}
		}
		for _, l := range in.Limits {
			if err := validateDonationLimits(l.MaxConcurrency, l.RPMLimit); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidValue, err)
			}
		}
	case in.New != nil && in.Existing == nil:
		if len(in.Keys) == 0 {
			return fmt.Errorf("%w: a donation requires at least one key", ErrInvalidValue)
		}
		for _, k := range in.Keys {
			if len(k.Secret) == 0 {
				return fmt.Errorf("%w: key secret is required", ErrInvalidValue)
			}
			if err := validateDonationLimits(k.MaxConcurrency, k.RPMLimit); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidValue, err)
			}
			if err := validateBoundedStatement(k.Note, 256); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidValue, err)
			}
		}
	default:
		return fmt.Errorf("%w: exactly one of existing keys or a new endpoint must be supplied", ErrInvalidValue)
	}
	return nil
}

// CreateDonation inserts a pending donation together with its donation_keys
// rows and acquires every physical claim INSIDE ONE TRANSACTION. The nested
// mode also creates the personal endpoint and seals each fresh secret in that
// same transaction, so any failure — including a physical-key claim conflict —
// rolls back the whole submission leaving no orphan resource and no residual
// ciphertext. A duplicate selection of the same physical key within one
// donation is rejected up front; a key claimed by another active donation is
// ErrDonationKeyClaimConflict.
func (s *Store) CreateDonation(ctx context.Context, in CreateDonationInput) (Donation, error) {
	defer func() {
		for _, k := range in.Keys {
			clear(k.Secret)
		}
	}()
	if err := validateCreateDonationInput(in); err != nil {
		return Donation{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Donation{}, fmt.Errorf("create donation: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var endpointID int64
	var baseURL string
	if in.Existing != nil {
		// Ownership-scoped read: a cross-user or missing endpoint is
		// indistinguishable ErrNotFound.
		err := tx.QueryRowContext(ctx,
			`SELECT id, base_url FROM endpoints WHERE id=? AND user_id=?`,
			in.Existing.EndpointID, in.UserID).Scan(&endpointID, &baseURL)
		if errors.Is(err, sql.ErrNoRows) {
			return Donation{}, ErrNotFound
		}
		if err != nil {
			return Donation{}, fmt.Errorf("create donation: read endpoint: %w", err)
		}
	} else {
		// Nested creation honors the same endpoint-count cap as the direct
		// path, checked atomically inside this transaction.
		capValue, cerr := endpointCapLocked(ctx, tx, in.UserID)
		if cerr != nil {
			return Donation{}, cerr
		}
		var count int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM endpoints WHERE user_id=?`, in.UserID).Scan(&count); err != nil {
			return Donation{}, fmt.Errorf("create donation: count endpoints: %w", err)
		}
		if count >= capValue {
			return Donation{}, newCapError(ErrEndpointCap, ResourceEndpoint, capValue)
		}
		enabledInt := 0
		if in.New.Enabled {
			enabledInt = 1
		}
		res, ierr := tx.ExecContext(ctx, `
INSERT INTO endpoints (user_id, connector_type, base_url, note, enabled, created_at, updated_at)
VALUES (?,?,?,?,?,?,?)`,
			in.UserID, in.New.ConnectorType, in.New.BaseURL, in.New.Note, enabledInt, in.Now, in.Now)
		if ierr != nil {
			return Donation{}, fmt.Errorf("create donation: insert endpoint: %w", ierr)
		}
		endpointID, err = res.LastInsertId()
		if err != nil {
			return Donation{}, fmt.Errorf("create donation: endpoint id: %w", err)
		}
		baseURL = in.New.BaseURL
	}

	// The donation row itself: always created pending and disabled; approval
	// flips both atomically with the claim sync.
	res, err := tx.ExecContext(ctx, `
INSERT INTO donations (user_id, endpoint_id, endpoint_base_url, status, enabled, description, expires_at, created_at, updated_at)
VALUES (?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		in.UserID, nullableInt64(endpointID), baseURL, DonationPending, in.Description, in.ExpiresAt, in.Now, in.Now)
	if err != nil {
		return Donation{}, fmt.Errorf("create donation: insert: %w", err)
	}
	donationID, err := res.LastInsertId()
	if err != nil {
		return Donation{}, fmt.Errorf("create donation: donation id: %w", err)
	}

	limitByPhysicalKey := make(map[int64]KeyLimitSpec, len(in.Limits))
	for _, l := range in.Limits {
		limitByPhysicalKey[l.EndpointKeyID] = l
	}

	if in.Existing != nil {
		for _, physicalKeyID := range in.Existing.KeyIDs {
			// Ownership + membership are part of the SELECT: a key from
			// another endpoint or another user yields zero rows.
			var head, tail string
			err := tx.QueryRowContext(ctx, `
SELECT ek.display_head, ek.display_tail
FROM endpoint_keys ek
JOIN endpoints e ON ek.endpoint_id = e.id
WHERE ek.id=? AND ek.endpoint_id=? AND e.user_id=? AND e.id=?`,
				physicalKeyID, in.Existing.EndpointID, in.UserID, endpointID).Scan(&head, &tail)
			if errors.Is(err, sql.ErrNoRows) {
				return Donation{}, ErrNotFound
			}
			if err != nil {
				return Donation{}, fmt.Errorf("create donation: read key: %w", err)
			}
			limits := limitByPhysicalKey[physicalKeyID]
			if _, err := insertDonationKeyTx(ctx, tx, donationID, &physicalKeyID, head, tail,
				limits.MaxConcurrency, limits.RPMLimit, true, in.Now); err != nil {
				return Donation{}, err
			}
		}
	} else {
		for i := range in.Keys {
			k := in.Keys[i]
			physicalKeyID, kerr := s.createEndpointKeyTx(ctx, tx, in.UserID, endpointID,
				k.Secret, k.DisplayHead, k.DisplayTail, k.Note, k.Enabled, in.Now)
			if kerr != nil {
				return Donation{}, kerr
			}
			if _, err := insertDonationKeyTx(ctx, tx, donationID, &physicalKeyID,
				k.DisplayHead, k.DisplayTail, k.MaxConcurrency, k.RPMLimit, true, in.Now); err != nil {
				return Donation{}, err
			}
		}
	}

	// Acquire every claim. Any conflict rolls back the entire transaction,
	// including a nested endpoint/key creation.
	if err := syncDonationClaimsTx(ctx, tx, donationID, in.Now); err != nil {
		return Donation{}, err
	}

	if err := tx.Commit(); err != nil {
		return Donation{}, fmt.Errorf("create donation: commit: %w", err)
	}
	committed = true
	return s.mustGetDonation(ctx, donationID)
}

// insertDonationKeyTx writes one donation_keys row. endpointKeyID may be nil
// only when the physical key has already vanished (never on the create path).
func insertDonationKeyTx(ctx context.Context, tx *sql.Tx, donationID int64, endpointKeyID *int64,
	head, tail string, maxConcurrency, rpmLimit int64, enabled bool, now int64) (int64, error) {
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	res, err := tx.ExecContext(ctx, `
INSERT INTO donation_keys
	(donation_id, endpoint_key_id, display_head, display_tail,
	 max_concurrency, rpm_limit, credits_usage_cap, credits_used, credits_reserved, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 0, 0, 0, ?, ?, ?)`,
		donationID, endpointKeyID, head, tail, maxConcurrency, rpmLimit, enabledInt, now, now)
	if err != nil {
		if isConstraintError(err) {
			return 0, ErrConflict // UNIQUE(donation_id, endpoint_key_id)
		}
		return 0, fmt.Errorf("insert donation key: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert donation key: id: %w", err)
	}
	return id, nil
}

// syncDonationClaimsTx makes the physical claim set match the invariant EXACTLY:
//
//	pending                          -> all keys with a physical key hold claims
//	approved && enabled              -> enabled keys hold claims
//	anything else                    -> no claims
//
// Acquisition is a constrained INSERT (the claims table's primary key decides);
// a conflict with ANOTHER donation's key aborts with ErrDonationKeyClaimConflict
// after rolling the caller's transaction back. Re-acquiring a claim this
// donation already holds is an idempotent no-op.
func syncDonationClaimsTx(ctx context.Context, tx *sql.Tx, donationID int64, now int64) error {
	var status string
	var enabled int
	if err := tx.QueryRowContext(ctx,
		`SELECT status, enabled FROM donations WHERE id=?`, donationID).Scan(&status, &enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("claim sync: read donation: %w", err)
	}

	wantClaim := false
	allKeys := false
	switch status {
	case DonationPending:
		wantClaim, allKeys = true, true
	case DonationApproved:
		wantClaim = enabled == 1
	default:
		wantClaim = false
	}

	type desiredKey struct{ dkID, physicalID int64 }
	desired := make([]desiredKey, 0, 8)
	if wantClaim {
		// Desired set: the donation_keys rows eligible to hold a claim right now.
		rows, err := tx.QueryContext(ctx, `
SELECT id, endpoint_key_id FROM donation_keys
WHERE donation_id=? AND endpoint_key_id IS NOT NULL`+
			map[bool]string{true: "", false: " AND enabled=1"}[allKeys], donationID)
		if err != nil {
			return fmt.Errorf("claim sync: list keys: %w", err)
		}
		for rows.Next() {
			var d desiredKey
			if err := rows.Scan(&d.dkID, &d.physicalID); err != nil {
				rows.Close()
				return fmt.Errorf("claim sync: scan key: %w", err)
			}
			desired = append(desired, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("claim sync: iterate keys: %w", err)
		}
	}

	// Release every claim this donation holds that is not in the desired set.
	if len(desired) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM donation_key_claims
WHERE donation_key_id IN (SELECT id FROM donation_keys WHERE donation_id=?)`, donationID); err != nil {
			return fmt.Errorf("claim sync: release claims: %w", err)
		}
	} else {
		desiredIDs := make([]int64, len(desired))
		for i, d := range desired {
			desiredIDs[i] = d.dkID
		}
		params := make([]string, len(desiredIDs))
		args := make([]any, 0, len(desiredIDs)+1)
		args = append(args, donationID)
		for i, id := range desiredIDs {
			params[i] = "?"
			args = append(args, id)
		}
		// #nosec G202 -- placeholders are generated "?" markers only
		query := `DELETE FROM donation_key_claims
WHERE donation_key_id IN (
	SELECT id FROM donation_keys WHERE donation_id=? AND id NOT IN (` + strings.Join(params, ",") + `))`
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("claim sync: release stale claims: %w", err)
		}
	}

	for _, d := range desired {
		res, err := tx.ExecContext(ctx, `
INSERT INTO donation_key_claims (endpoint_key_id, donation_key_id, claimed_at)
VALUES (?, ?, ?)
ON CONFLICT(endpoint_key_id) DO NOTHING`, d.physicalID, d.dkID, now)
		if err != nil {
			return fmt.Errorf("claim sync: acquire: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("claim sync: acquire result: %w", err)
		}
		if affected == 1 {
			continue
		}
		// Zero rows: either this donation already holds the claim (fine) or
		// another active donation does (conflict). One ownership-scoped read
		// classifies AFTER the constrained INSERT failed — never before it.
		var holder sql.NullInt64
		err = tx.QueryRowContext(ctx,
			`SELECT donation_key_id FROM donation_key_claims WHERE endpoint_key_id=?`, d.physicalID).Scan(&holder)
		if err != nil {
			return fmt.Errorf("claim sync: classify conflict: %w", err)
		}
		if !holder.Valid || holder.Int64 != d.dkID {
			return ErrDonationKeyClaimConflict
		}
	}
	return nil
}

// sweepExpiredDonationsTx lazily disables every approved+enabled donation whose
// expiry has passed, releasing its claims and appending the system disable
// audit entry — all inside the caller's transaction. The conditional UPDATE is
// the linearization point: concurrent sweeps and reviews cannot double-disable.
func sweepExpiredDonationsTx(ctx context.Context, tx *sql.Tx, now int64) error {
	rows, err := tx.QueryContext(ctx, `
SELECT id FROM donations
WHERE status='approved' AND enabled=1 AND expires_at IS NOT NULL AND expires_at <= ?
LIMIT 64`, now)
	if err != nil {
		return fmt.Errorf("expiry sweep: list: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("expiry sweep: scan: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("expiry sweep: iterate: %w", err)
	}
	for _, id := range ids {
		res, err := tx.ExecContext(ctx, `
UPDATE donations SET enabled=0, updated_at=?
WHERE id=? AND status='approved' AND enabled=1 AND expires_at IS NOT NULL AND expires_at <= ?`,
			now, id, now)
		if err != nil {
			return fmt.Errorf("expiry sweep: disable: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("expiry sweep: disable result: %w", err)
		}
		if affected == 0 {
			continue // another writer won the race; its own sync ran already
		}
		if err := appendDonationReviewTx(ctx, tx, id, ReviewRoleSystem, ReviewActionDisable,
			"expired", nil, now); err != nil {
			return err
		}
		if err := syncDonationClaimsTx(ctx, tx, id, now); err != nil {
			return fmt.Errorf("expiry sweep: claim sync: %w", err)
		}
	}
	return nil
}

// appendDonationReviewTx appends one audit entry inside the caller's
// transaction. reviewerID 0 means NULL (system or deleted actor later).
func appendDonationReviewTx(ctx context.Context, tx *sql.Tx, donationID int64, role, action, note string, reviewerID *int64, now int64) error {
	if note == "" {
		note = "" // normalize
	} else if err := validateBoundedStatement(note, MaxReviewNoteRunes); err != nil {
		return fmt.Errorf("review note: %w", ErrInvalidValue)
	}
	var reviewer any
	if reviewerID != nil && *reviewerID > 0 {
		reviewer = *reviewerID
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO donation_reviews (donation_id, reviewer_user_id, reviewer_role, action, note, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, donationID, reviewer, role, action, note, now)
	if err != nil {
		return fmt.Errorf("append donation review: %w", err)
	}
	return nil
}

func nullableInt64(v int64) any {
	if v > 0 {
		return v
	}
	return nil
}

// queryContexter is satisfied by both *sql.DB and *sql.Tx; list helpers use
// it so the same projection serves transactional reads and bounded exports.
type queryContexter interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// --- reads ------------------------------------------------------------------

const donationSelectSQL = `
SELECT id, user_id, endpoint_id, endpoint_base_url, status, enabled, description,
       review_note, reviewed_by_user_id, reviewed_by_role, expires_at, reviewed_at,
       created_at, updated_at
FROM donations`

func scanDonationRow(row *sql.Row) (Donation, error) {
	var d Donation
	var endpointID, reviewedBy, expiresAt, reviewedAt sql.NullInt64
	var enabledInt int
	err := row.Scan(&d.ID, &d.UserID, &endpointID, &d.EndpointBaseURL, &d.Status, &enabledInt,
		&d.Description, &d.ReviewNote, &reviewedBy, &d.ReviewedByRole, &expiresAt, &reviewedAt,
		&d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return Donation{}, err
	}
	d.Enabled = enabledInt == 1
	d.EndpointID = nullInt64Ptr(endpointID)
	d.ReviewedByUserID = nullInt64Ptr(reviewedBy)
	d.ExpiresAt = nullInt64Ptr(expiresAt)
	d.ReviewedAt = nullInt64Ptr(reviewedAt)
	return d, nil
}

func nullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	out := v.Int64
	return &out
}

func (s *Store) mustGetDonation(ctx context.Context, donationID int64) (Donation, error) {
	row := s.db.QueryRowContext(ctx, donationSelectSQL+` WHERE id=?`, donationID)
	d, err := scanDonationRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Donation{}, ErrNotFound
	}
	if err != nil {
		return Donation{}, fmt.Errorf("read donation: %w", err)
	}
	return d, nil
}

// GetOwnDonation returns the donation with id owned by userID, after applying
// the lazy expiry sweep. A missing or cross-user id is ErrNotFound.
func (s *Store) GetOwnDonation(ctx context.Context, userID, donationID int64, now int64) (Donation, []DonationKey, []DonationReview, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Donation{}, nil, nil, fmt.Errorf("get donation: begin: %w", err)
	}
	defer tx.Rollback()
	if _, _, err := readOwnDonationTx(ctx, tx, userID, donationID, now); err != nil {
		return Donation{}, nil, nil, err
	}
	keys, err := listDonationKeysTx(ctx, tx, donationID)
	if err != nil {
		return Donation{}, nil, nil, err
	}
	reviews, err := listDonationReviewsTx(ctx, tx, donationID)
	if err != nil {
		return Donation{}, nil, nil, err
	}
	commitErr := tx.Commit()
	if commitErr != nil {
		return Donation{}, nil, nil, fmt.Errorf("get donation: commit: %w", commitErr)
	}
	d, err := s.mustGetDonation(ctx, donationID)
	if err != nil {
		return Donation{}, nil, nil, err
	}
	return d, keys, reviews, nil
}

// readOwnDonationTx loads one owned donation inside tx, running the lazy
// expiry sweep first so a read never advertises an expired enabled state. A
// missing or cross-user id is ErrNotFound.
func readOwnDonationTx(ctx context.Context, tx *sql.Tx, userID, donationID, now int64) (Donation, bool, error) {
	if err := sweepExpiredDonationsTx(ctx, tx, now); err != nil {
		return Donation{}, false, err
	}
	row := tx.QueryRowContext(ctx, donationSelectSQL+` WHERE id=? AND user_id=?`, donationID, userID)
	d, err := scanDonationRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Donation{}, false, ErrNotFound
	}
	if err != nil {
		return Donation{}, false, fmt.Errorf("read own donation: %w", err)
	}
	return d, donationActive(&d), nil
}

// ListOwnDonations returns the caller's donations (all statuses), newest
// first, after the lazy expiry sweep.
func (s *Store) ListOwnDonations(ctx context.Context, userID int64, now int64, limit, offset int) ([]Donation, int, error) {
	if userID <= 0 || limit <= 0 {
		return nil, 0, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("list donations: begin: %w", err)
	}
	defer tx.Rollback()
	if err := sweepExpiredDonationsTx(ctx, tx, now); err != nil {
		return nil, 0, err
	}
	var total int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM donations WHERE user_id=?`, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count donations: %w", err)
	}
	rows, err := tx.QueryContext(ctx, donationSelectSQL+`
WHERE user_id=? ORDER BY id DESC LIMIT ? OFFSET ?`, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list donations: %w", err)
	}
	defer rows.Close()
	out := make([]Donation, 0, min(limit, 32))
	for rows.Next() {
		var (
			d                                             Donation
			endpointID, reviewedBy, expiresAt, reviewedAt sql.NullInt64
			enabledInt                                    int
		)
		if err := rows.Scan(&d.ID, &d.UserID, &endpointID, &d.EndpointBaseURL, &d.Status, &enabledInt,
			&d.Description, &d.ReviewNote, &reviewedBy, &d.ReviewedByRole, &expiresAt, &reviewedAt,
			&d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan donation: %w", err)
		}
		d.Enabled = enabledInt == 1
		d.EndpointID = nullInt64Ptr(endpointID)
		d.ReviewedByUserID = nullInt64Ptr(reviewedBy)
		d.ExpiresAt = nullInt64Ptr(expiresAt)
		d.ReviewedAt = nullInt64Ptr(reviewedAt)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate donations: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("list donations: commit: %w", err)
	}
	return out, total, nil
}

// ListReviewableDonations returns donations visible to a reviewer, optionally
// filtered by status, newest first, after the lazy expiry sweep. The query
// projects only donation-table columns; callers build the role-appropriate
// projection (steward sees no other-user sensitive fields because none are
// joined here at all).
func (s *Store) ListReviewableDonations(ctx context.Context, status string, now int64, limit, offset int) ([]Donation, int, error) {
	if limit <= 0 {
		return nil, 0, ErrNotFound
	}
	if status != "" && !validDonationStatus(status) {
		return nil, 0, ErrInvalidValue
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("list reviewable donations: begin: %w", err)
	}
	defer tx.Rollback()
	if err := sweepExpiredDonationsTx(ctx, tx, now); err != nil {
		return nil, 0, err
	}
	where := `WHERE 1=1`
	args := []any{}
	if status != "" {
		where += ` AND status=?`
		args = append(args, status)
	}
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donations `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count reviewable donations: %w", err)
	}
	query := donationSelectSQL + ` ` + where + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list reviewable donations: %w", err)
	}
	defer rows.Close()
	out := make([]Donation, 0, min(limit, 32))
	for rows.Next() {
		var (
			d                                             Donation
			endpointID, reviewedBy, expiresAt, reviewedAt sql.NullInt64
			enabledInt                                    int
		)
		if err := rows.Scan(&d.ID, &d.UserID, &endpointID, &d.EndpointBaseURL, &d.Status, &enabledInt,
			&d.Description, &d.ReviewNote, &reviewedBy, &d.ReviewedByRole, &expiresAt, &reviewedAt,
			&d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan donation: %w", err)
		}
		d.Enabled = enabledInt == 1
		d.EndpointID = nullInt64Ptr(endpointID)
		d.ReviewedByUserID = nullInt64Ptr(reviewedBy)
		d.ExpiresAt = nullInt64Ptr(expiresAt)
		d.ReviewedAt = nullInt64Ptr(reviewedAt)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate reviewable donations: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("list reviewable donations: commit: %w", err)
	}
	return out, total, nil
}

func listDonationKeysTx(ctx context.Context, q queryContexter, donationID int64) ([]DonationKey, error) {
	rows, err := q.QueryContext(ctx, `
SELECT id, donation_id, endpoint_key_id, display_head, display_tail,
       max_concurrency, rpm_limit, credits_usage_cap, credits_used, credits_reserved,
       enabled, created_at, updated_at
FROM donation_keys WHERE donation_id=? ORDER BY id`, donationID)
	if err != nil {
		return nil, fmt.Errorf("list donation keys: %w", err)
	}
	defer rows.Close()
	out := make([]DonationKey, 0, 8)
	for rows.Next() {
		var (
			k          DonationKey
			physicalID sql.NullInt64
			enabledInt int
		)
		if err := rows.Scan(&k.ID, &k.DonationID, &physicalID, &k.DisplayHead, &k.DisplayTail,
			&k.MaxConcurrency, &k.RPMLimit, &k.CreditsUsageCap, &k.CreditsUsed, &k.CreditsReserved,
			&enabledInt, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan donation key: %w", err)
		}
		k.EndpointKeyID = nullInt64Ptr(physicalID)
		k.Enabled = enabledInt == 1
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate donation keys: %w", err)
	}
	return out, nil
}

// ListOwnDonationKeys returns the donation_keys rows of one owned donation.
func (s *Store) ListOwnDonationKeys(ctx context.Context, userID, donationID int64) ([]DonationKey, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list donation keys: begin: %w", err)
	}
	defer tx.Rollback()
	if _, _, err := readOwnDonationTx(ctx, tx, userID, donationID, time.Now().Unix()); err != nil {
		return nil, err
	}
	keys, err := listDonationKeysTx(ctx, tx, donationID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("list donation keys: commit: %w", err)
	}
	return keys, nil
}

func listDonationReviewsTx(ctx context.Context, tx *sql.Tx, donationID int64) ([]DonationReview, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT id, donation_id, reviewer_user_id, reviewer_role, action, note, created_at
FROM donation_reviews WHERE donation_id=? ORDER BY id`, donationID)
	if err != nil {
		return nil, fmt.Errorf("list donation reviews: %w", err)
	}
	defer rows.Close()
	out := make([]DonationReview, 0, 8)
	for rows.Next() {
		var (
			r        DonationReview
			reviewer sql.NullInt64
		)
		if err := rows.Scan(&r.ID, &r.DonationID, &reviewer, &r.ReviewerRole, &r.Action, &r.Note, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan donation review: %w", err)
		}
		r.ReviewerUserID = nullInt64Ptr(reviewer)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate donation reviews: %w", err)
	}
	return out, nil
}

// ListDonationReviews returns the append-only audit entries of one owned
// donation (the donor sees the review history of their own submission).
func (s *Store) ListDonationReviews(ctx context.Context, userID, donationID int64) ([]DonationReview, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list donation reviews: begin: %w", err)
	}
	defer tx.Rollback()
	if _, _, err := readOwnDonationTx(ctx, tx, userID, donationID, time.Now().Unix()); err != nil {
		return nil, err
	}
	reviews, err := listDonationReviewsTx(ctx, tx, donationID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("list donation reviews: commit: %w", err)
	}
	return reviews, nil
}

// --- owner mutations --------------------------------------------------------

// UpdateDonationKeysInput replaces the selected physical key set and/or the
// per-key limits of a PENDING donation owned by userID. Replacement keys must
// belong to the SAME endpoint as the donation (the endpoint itself is
// immutable after submission). Every change, the review audit entry and the
// claim re-sync share one transaction.
type UpdateDonationKeysInput struct {
	UserID      int64
	DonationID  int64
	Now         int64
	Description *string  // nil = unchanged
	ExpiresAt   **int64  // nil = unchanged; *ExpiresAt != nil sets/clears via pointer target
	KeyIDs      *[]int64 // nil = unchanged; non-nil replaces the full selection
	Limits      []KeyLimitSpec
	ActorRole   string // "" = owner self-edit; admin/level5 record reviewer entries
	ActorUserID int64  // >0 for reviewers
	Note        string
}

// UpdateOwnPendingDonation edits a pending donation owned by the caller: the
// required description, the expiry, and/or the selected key set (same
// endpoint). An already-reviewed or soft-deleted donation is immutable here.
// All changes plus the claim re-sync commit atomically; a replacement key
// claimed by another active donation aborts the whole edit.
func (s *Store) UpdateOwnPendingDonation(ctx context.Context, in UpdateDonationKeysInput) (Donation, error) {
	if in.ActorRole != "" && in.ActorRole != ReviewRoleAdmin && in.ActorRole != ReviewRoleLevel5 {
		return Donation{}, ErrConflict
	}
	if in.Now <= 0 {
		return Donation{}, fmt.Errorf("%w: timestamp is required", ErrInvalidValue)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Donation{}, fmt.Errorf("update donation: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	d, active, err := readOwnDonationTx(ctx, tx, in.UserID, in.DonationID, in.Now)
	if err != nil {
		return Donation{}, err
	}
	_ = active
	if d.Status != DonationPending {
		return Donation{}, ErrConflict
	}

	sets := make([]string, 0, 3)
	args := make([]any, 0, 4)
	if in.Description != nil {
		if err := validateBoundedStatement(*in.Description, MaxDonationDescriptionRunes); err != nil {
			return Donation{}, fmt.Errorf("%w: %v", ErrInvalidValue, err)
		}
		sets = append(sets, "description=?")
		args = append(args, *in.Description)
	}
	if in.ExpiresAt != nil {
		if *in.ExpiresAt != nil && **in.ExpiresAt <= in.Now {
			return Donation{}, fmt.Errorf("%w: expires_at must be in the future", ErrInvalidValue)
		}
		sets = append(sets, "expires_at=?")
		args = append(args, *in.ExpiresAt)
	}
	if len(sets) > 0 || in.KeyIDs != nil {
		sets = append(sets, "updated_at=?")
		args = append(args, in.Now)
	}
	if len(sets) > 0 {
		// #nosec G202 -- sets contains only fixed column fragments selected above
		query := `UPDATE donations SET `
		for i, s := range sets {
			if i > 0 {
				query += ", "
			}
			query += s
		}
		query += ` WHERE id=? AND user_id=? AND status='pending'`
		args = append(args, in.DonationID, in.UserID)
		res, uerr := tx.ExecContext(ctx, query, args...)
		if uerr != nil {
			return Donation{}, fmt.Errorf("update donation: %w", uerr)
		}
		affected, uerr := res.RowsAffected()
		if uerr != nil {
			return Donation{}, fmt.Errorf("update donation: rows: %w", uerr)
		}
		if affected == 0 {
			return Donation{}, ErrConflict // lost a race against a review decision
		}
	}

	// Replace the selected key set when requested: release the old donation
	// keys (their claims go with them via CASCADE) and insert the new ones.
	if in.KeyIDs != nil {
		newIDs := *in.KeyIDs
		if len(newIDs) == 0 {
			return Donation{}, fmt.Errorf("%w: a donation requires at least one key", ErrInvalidValue)
		}
		seen := make(map[int64]struct{}, len(newIDs))
		for _, id := range newIDs {
			if id <= 0 {
				return Donation{}, fmt.Errorf("%w: invalid endpoint key id", ErrInvalidValue)
			}
			if _, dup := seen[id]; dup {
				return Donation{}, fmt.Errorf("%w: duplicate endpoint key selection", ErrInvalidValue)
			}
			seen[id] = struct{}{}
		}
		if d.EndpointID == nil {
			return Donation{}, ErrConflict // underlying endpoint already gone
		}
		limitByKey := make(map[int64]KeyLimitSpec, len(in.Limits))
		for _, l := range in.Limits {
			limitByKey[l.EndpointKeyID] = l
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM donation_keys WHERE donation_id=?`, in.DonationID); err != nil {
			return Donation{}, fmt.Errorf("update donation: clear keys: %w", err)
		}
		for _, physicalID := range newIDs {
			var head, tail string
			err := tx.QueryRowContext(ctx, `
SELECT ek.display_head, ek.display_tail
FROM endpoint_keys ek
JOIN endpoints e ON ek.endpoint_id = e.id
WHERE ek.id=? AND ek.endpoint_id=? AND e.user_id=?`,
				physicalID, *d.EndpointID, in.UserID).Scan(&head, &tail)
			if errors.Is(err, sql.ErrNoRows) {
				return Donation{}, ErrNotFound
			}
			if err != nil {
				return Donation{}, fmt.Errorf("update donation: read key: %w", err)
			}
			limits := limitByKey[physicalID]
			if err := validateDonationLimits(limits.MaxConcurrency, limits.RPMLimit); err != nil {
				return Donation{}, fmt.Errorf("%w: %v", ErrInvalidValue, err)
			}
			if _, err := insertDonationKeyTx(ctx, tx, in.DonationID, &physicalID, head, tail,
				limits.MaxConcurrency, limits.RPMLimit, true, in.Now); err != nil {
				return Donation{}, err
			}
		}
	} else if len(in.Limits) > 0 {
		// Limits-only adjustment on the existing key set.
		for _, l := range in.Limits {
			if err := validateDonationLimits(l.MaxConcurrency, l.RPMLimit); err != nil {
				return Donation{}, fmt.Errorf("%w: %v", ErrInvalidValue, err)
			}
			res, uerr := tx.ExecContext(ctx, `
UPDATE donation_keys SET max_concurrency=?, rpm_limit=?, updated_at=?
WHERE donation_id=? AND endpoint_key_id=?`, l.MaxConcurrency, l.RPMLimit, in.Now, in.DonationID, l.EndpointKeyID)
			if uerr != nil {
				return Donation{}, fmt.Errorf("update donation key limits: %w", uerr)
			}
			affected, uerr := res.RowsAffected()
			if uerr != nil {
				return Donation{}, fmt.Errorf("update donation key limits: rows: %w", uerr)
			}
			if affected == 0 {
				return Donation{}, ErrNotFound
			}
		}
	}

	// Claim invariant re-established inside the same transaction.
	if err := syncDonationClaimsTx(ctx, tx, in.DonationID, in.Now); err != nil {
		return Donation{}, err
	}

	role := in.ActorRole
	action := ReviewActionUpdate
	if role == "" {
		role = ReviewRoleSystem // owner self-edit while pending
	}
	var reviewer *int64
	if in.ActorUserID > 0 {
		id := in.ActorUserID
		reviewer = &id
	}
	if err := appendDonationReviewTx(ctx, tx, in.DonationID, role, action, in.Note, reviewer, in.Now); err != nil {
		return Donation{}, err
	}

	// Reviewer-driven updates are console writes (product activity); owner
	// self-edits are ordinary user actions without console activity.
	if role != ReviewRoleSystem {
		if err := recordConsoleActivityTxBestEffort(ctx, tx, in.ActorUserID, in.Now); err != nil {
			return Donation{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return Donation{}, fmt.Errorf("update donation: commit: %w", err)
	}
	committed = true
	return s.mustGetDonation(ctx, in.DonationID)
}

// recordConsoleActivityTxBestEffort records one console-write activity bucket
// for the acting administrator/steward inside the caller's transaction. While
// the site timezone is unset the activity system is disabled and this is a
// no-op, exactly like the generic site-config write path.
func recordConsoleActivityTxBestEffort(ctx context.Context, tx *sql.Tx, actorUserID, now int64) error {
	day, err := siteDayKeyAtTx(tx, now)
	if errors.Is(err, ErrTimezoneUnavailable) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("console activity day key: %w", err)
	}
	_, err = recordActivityTx(ctx, tx, actorUserID, day, ActivityDelta{ConsoleWrites: 1}, now)
	if err != nil {
		return fmt.Errorf("console activity: %w", err)
	}
	return nil
}

// DeleteOwnDonation soft-deletes the caller's donation: releases every claim,
// sets status=deleted/enabled=0 and appends the delete audit entry in ONE
// transaction. Works for any non-deleted status; a missing or cross-user id is
// ErrNotFound; deleting twice is ErrNotFound (the row stays but no longer
// matches the owner-visible guard below) — actually it stays owner-visible as
// history, so a second delete is refused with ErrConflict.
func (s *Store) DeleteOwnDonation(ctx context.Context, userID, donationID int64, now int64) error {
	return s.deleteDonationTx(ctx, userID, donationID, "", 0, now)
}

// DeleteDonationByReviewer soft-deletes any donation on behalf of a reviewer.
func (s *Store) DeleteDonationByReviewer(ctx context.Context, donationID int64, role string, reviewerUserID int64, note string, now int64) error {
	if role != ReviewRoleAdmin && role != ReviewRoleLevel5 {
		return ErrConflict
	}
	if reviewerUserID <= 0 {
		return ErrConflict
	}
	return s.deleteDonationTx(ctx, 0, donationID, role, reviewerUserID, now, note)
}

func (s *Store) deleteDonationTx(ctx context.Context, ownerScopeUserID, donationID int64, role string, reviewerUserID int64, now int64, note ...string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete donation: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Scope + state gate in one conditional UPDATE: the single-writer handle
	// makes this the linearization point against a concurrent review.
	var res sql.Result
	if ownerScopeUserID > 0 {
		res, err = tx.ExecContext(ctx, `
UPDATE donations SET status='deleted', enabled=0, updated_at=?
WHERE id=? AND user_id=? AND status<>'deleted'`, now, donationID, ownerScopeUserID)
	} else {
		res, err = tx.ExecContext(ctx, `
UPDATE donations SET status='deleted', enabled=0, updated_at=?
WHERE id=? AND status<>'deleted'`, now, donationID)
	}
	if err != nil {
		return fmt.Errorf("delete donation: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete donation: rows: %w", err)
	}
	if affected == 0 {
		// Distinguish "not found / not yours" from "already deleted" without
		// leaking existence across users.
		var (
			userID sql.NullInt64
			status string
		)
		qerr := tx.QueryRowContext(ctx,
			`SELECT user_id, status FROM donations WHERE id=?`, donationID).Scan(&userID, &status)
		if errors.Is(qerr, sql.ErrNoRows) {
			return ErrNotFound
		}
		if qerr != nil {
			return fmt.Errorf("delete donation: classify: %w", qerr)
		}
		if ownerScopeUserID > 0 && (!userID.Valid || userID.Int64 != ownerScopeUserID) {
			return ErrNotFound
		}
		return ErrConflict
	}

	if err := syncDonationClaimsTx(ctx, tx, donationID, now); err != nil {
		return fmt.Errorf("delete donation: claim sync: %w", err)
	}

	appendRole := role
	var reviewer *int64
	if appendRole == "" {
		appendRole = ReviewRoleSystem
	} else {
		id := reviewerUserID
		reviewer = &id
	}
	reviewNote := ""
	if len(note) > 0 {
		reviewNote = note[0]
	}
	if err := appendDonationReviewTx(ctx, tx, donationID, appendRole, ReviewActionDelete, reviewNote, reviewer, now); err != nil {
		return err
	}
	if appendRole != ReviewRoleSystem {
		if err := recordConsoleActivityTxBestEffort(ctx, tx, reviewerUserID, now); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete donation: commit: %w", err)
	}
	committed = true
	return nil
}

// --- reviewer mutations -----------------------------------------------------

// ReviewDecision is one reviewer operation on a WHOLE donation. Action is one
// of approve/reject/enable/disable/update. KeyUpdates adjust the frozen
// per-key fields (limits, usage cap, enabled flag) in the same transaction.
type ReviewDecision struct {
	DonationID int64
	Role       string // admin | level5
	ReviewerID int64
	Action     string
	Note       string
	Now        int64

	// ExpiresAt: nil = unchanged; non-nil outer pointer sets the expiry (a
	// nil inner pointer clears it to never-expires).
	ExpiresAt **int64

	KeyUpdates []DonationKeyUpdate
}

// DonationKeyUpdate adjusts one donation key of the reviewed donation.
// Pointer semantics: nil field = unchanged. Enabled=false releases that key's
// claim; Enabled=true re-acquires it (ErrDonationKeyClaimConflict when the
// physical key is held elsewhere). credits_usage_cap is milli-credits
// (string wire happens at the handler boundary); 0 = unlimited.
type DonationKeyUpdate struct {
	DonationKeyID   int64
	MaxConcurrency  *int64
	RPMLimit        *int64
	CreditsUsageCap *int64
	Enabled         *bool
}

// ApplyDonationReview executes one reviewer decision atomically: the status /
// enabled change, optional expiry and per-key adjustments, the claim
// re-sync, the append-only audit entry and the console-write activity all
// share one transaction. Approve defaults the donation to enabled (frozen
// §J.4); reject and delete disable it. A claim conflict during enable/approve
// aborts the whole decision.
func (s *Store) ApplyDonationReview(ctx context.Context, dec ReviewDecision) (Donation, error) {
	if dec.Role != ReviewRoleAdmin && dec.Role != ReviewRoleLevel5 {
		return Donation{}, ErrConflict
	}
	if dec.ReviewerID <= 0 || dec.Now <= 0 {
		return Donation{}, ErrConflict
	}
	switch dec.Action {
	case ReviewActionApprove, ReviewActionReject, ReviewActionEnable,
		ReviewActionDisable, ReviewActionUpdate:
	default:
		return Donation{}, ErrConflict
	}
	if err := validateBoundedStatement(dec.Note, MaxReviewNoteRunes); err != nil {
		return Donation{}, fmt.Errorf("%w: %v", ErrInvalidValue, err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Donation{}, fmt.Errorf("apply review: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Load with the lazy expiry sweep so a decision lands on current state.
	if err := sweepExpiredDonationsTx(ctx, tx, dec.Now); err != nil {
		return Donation{}, err
	}
	row := tx.QueryRowContext(ctx, donationSelectSQL+` WHERE id=?`, dec.DonationID)
	current, err := scanDonationRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Donation{}, ErrNotFound
	}
	if err != nil {
		return Donation{}, fmt.Errorf("apply review: read donation: %w", err)
	}
	if current.Status == DonationDeleted {
		return Donation{}, ErrConflict
	}

	sets := make([]string, 0, 6)
	args := make([]any, 0, 8)
	switch dec.Action {
	case ReviewActionApprove:
		if current.Status == DonationApproved {
			return Donation{}, ErrConflict
		}
		if current.Status != DonationPending {
			return Donation{}, ErrConflict
		}
		sets = append(sets, "status='approved'", "enabled=1",
			"review_note=?", "reviewed_by_user_id=?", "reviewed_by_role=?", "reviewed_at=?")
		args = append(args, dec.Note, dec.ReviewerID, dec.Role, dec.Now)
	case ReviewActionReject:
		if current.Status != DonationPending {
			return Donation{}, ErrConflict
		}
		sets = append(sets, "status='rejected'", "enabled=0",
			"review_note=?", "reviewed_by_user_id=?", "reviewed_by_role=?", "reviewed_at=?")
		args = append(args, dec.Note, dec.ReviewerID, dec.Role, dec.Now)
	case ReviewActionEnable:
		if current.Status != DonationApproved || current.Enabled {
			return Donation{}, ErrConflict
		}
		sets = append(sets, "enabled=1")
	case ReviewActionDisable:
		if current.Status != DonationApproved || !current.Enabled {
			return Donation{}, ErrConflict
		}
		sets = append(sets, "enabled=0")
	case ReviewActionUpdate:
		// Field-only adjustment; allowed on pending and approved donations
		// alike (reviewers may tune limits/expiry before deciding).
	}

	if dec.ExpiresAt != nil {
		if *dec.ExpiresAt != nil && **dec.ExpiresAt <= dec.Now {
			return Donation{}, fmt.Errorf("%w: expires_at must be in the future", ErrInvalidValue)
		}
		sets = append(sets, "expires_at=?")
		args = append(args, *dec.ExpiresAt)
	}

	// Per-key adjustments run BEFORE the status flip so approve validates the
	// adjusted values and the final claim sync sees final enabled flags.
	for _, ku := range dec.KeyUpdates {
		if err := applyDonationKeyUpdateTx(ctx, tx, current, ku, dec.Now); err != nil {
			return Donation{}, err
		}
	}

	if len(sets) > 0 {
		sets = append(sets, "updated_at=?")
		args = append(args, dec.Now, dec.DonationID)
		query := `UPDATE donations SET `
		for i, s := range sets {
			if i > 0 {
				query += ", "
			}
			query += s
		}
		query += ` WHERE id=?`
		// #nosec G202 -- sets contains only constant fragments selected above
		res, uerr := tx.ExecContext(ctx, query, args...)
		if uerr != nil {
			return Donation{}, fmt.Errorf("apply review: update: %w", uerr)
		}
		affected, uerr := res.RowsAffected()
		if uerr != nil {
			return Donation{}, fmt.Errorf("apply review: update rows: %w", uerr)
		}
		if affected == 0 {
			return Donation{}, ErrConflict
		}
	}

	// The claim invariant, re-established inside the same transaction.
	if err := syncDonationClaimsTx(ctx, tx, dec.DonationID, dec.Now); err != nil {
		return Donation{}, err
	}

	if err := appendDonationReviewTx(ctx, tx, dec.DonationID, dec.Role, dec.Action, dec.Note, &dec.ReviewerID, dec.Now); err != nil {
		return Donation{}, err
	}
	if err := recordConsoleActivityTxBestEffort(ctx, tx, dec.ReviewerID, dec.Now); err != nil {
		return Donation{}, err
	}

	if err := tx.Commit(); err != nil {
		return Donation{}, fmt.Errorf("apply review: commit: %w", err)
	}
	committed = true
	return s.mustGetDonation(ctx, dec.DonationID)
}

// applyDonationKeyUpdateTx applies one per-key adjustment inside the review
// transaction. The key must belong to the donation under review.
func applyDonationKeyUpdateTx(ctx context.Context, tx *sql.Tx, d Donation, ku DonationKeyUpdate, now int64) error {
	if ku.DonationKeyID <= 0 {
		return ErrNotFound
	}
	// Load current values scoped to this donation.
	var (
		maxConcurrency, rpmLimit, usageCap int64
		enabled                            int
	)
	err := tx.QueryRowContext(ctx, `
SELECT max_concurrency, rpm_limit, credits_usage_cap, enabled
FROM donation_keys WHERE id=? AND donation_id=?`,
		ku.DonationKeyID, d.ID).Scan(&maxConcurrency, &rpmLimit, &usageCap, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("donation key update: read: %w", err)
	}
	if ku.MaxConcurrency != nil {
		maxConcurrency = *ku.MaxConcurrency
	}
	if ku.RPMLimit != nil {
		rpmLimit = *ku.RPMLimit
	}
	if ku.CreditsUsageCap != nil {
		usageCap = *ku.CreditsUsageCap
	}
	if ku.Enabled != nil {
		if *ku.Enabled {
			enabled = 1
		} else {
			enabled = 0
		}
	}
	if err := validateDonationLimits(maxConcurrency, rpmLimit); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidValue, err)
	}
	if usageCap < 0 {
		return fmt.Errorf("%w: negative credits_usage_cap", ErrConflict)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE donation_keys
SET max_concurrency=?, rpm_limit=?, credits_usage_cap=?, enabled=?, updated_at=?
WHERE id=? AND donation_id=?`,
		maxConcurrency, rpmLimit, usageCap, enabled, now, ku.DonationKeyID, d.ID)
	if err != nil {
		return fmt.Errorf("donation key update: %w", err)
	}
	return nil
}

// GetReviewDonation loads one donation with keys and reviews for a reviewer
// surface (admin or steward frame). No user join happens here: callers build
// the role-appropriate projection from these rows alone.
func (s *Store) GetReviewDonation(ctx context.Context, donationID int64, now int64) (Donation, []DonationKey, []DonationReview, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Donation{}, nil, nil, fmt.Errorf("get review donation: begin: %w", err)
	}
	defer tx.Rollback()
	if err := sweepExpiredDonationsTx(ctx, tx, now); err != nil {
		return Donation{}, nil, nil, err
	}
	row := tx.QueryRowContext(ctx, donationSelectSQL+` WHERE id=?`, donationID)
	d, err := scanDonationRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Donation{}, nil, nil, ErrNotFound
	}
	if err != nil {
		return Donation{}, nil, nil, fmt.Errorf("get review donation: %w", err)
	}
	keys, err := listDonationKeysTx(ctx, tx, donationID)
	if err != nil {
		return Donation{}, nil, nil, err
	}
	reviews, err := listDonationReviewsTx(ctx, tx, donationID)
	if err != nil {
		return Donation{}, nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Donation{}, nil, nil, fmt.Errorf("get review donation: commit: %w", err)
	}
	return d, keys, reviews, nil
}
