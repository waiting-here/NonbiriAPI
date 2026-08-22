package db

// Charity reservation repository: the atomic pre-reserve, the guarded state
// machine (frozen implementation contract §5.3) and crash-safe recovery for
// one logical charity call.
//
// Frozen semantics implemented here:
//
//   - creation is ONE transaction: feature/user/model/candidate/claim/expiry
//     validation → donation-key cap conditional reserve → user balance
//     conditional debit → INSERT reservation + ledger. Any failure rolls back
//     everything; an uncovered balance refuses BEFORE dispatch;
//   - the user is debited exactly once per logical call: retrying another
//     donated key swaps ONLY the key reserve atomically (release old + take
//     new + move the row pointer in one transaction);
//   - every state move is a compare-and-set against the stored state, so a
//     repeated callback or recovery sweep observes zero affected rows and
//     becomes an atomic no-op. reserved can never jump to committed and a
//     dispatched reservation can never be released;
//   - settlement commits the ACTUAL charge even when it exceeds the reserve:
//     the user balance may be driven negative and the donation key's used
//     counter may cross its cap (that key simply stops being admitted);
//   - recovery converges stalled rows using ONLY the persisted price/reserve
//     snapshots, never current configuration, and is idempotent under crash
//     at every step because each effect is guarded by its own CAS or its own
//     idempotent operation id;
//   - account-deletion convergence runs the same effects inside the caller's
//     deletion transaction so a late callback linearizes against the delete.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

// Reservation repository sentinels. None carries request or secret material.
var (
	// ErrCharityDisabled reports that the site-wide charity switch is off.
	ErrCharityDisabled = errors.New("db: charity routing is disabled")
	// ErrCharitySuspended reports that the caller's charity eligibility is
	// currently suspended (users.charity_suspended_until in the future).
	ErrCharitySuspended = errors.New("db: caller charity eligibility is suspended")
	// ErrCharityTokenReserveUnconfigured reports that a per-token reservation
	// was attempted while charity_token_reserve_milli is unset or non-positive
	// (token mode fails closed, frozen §J.2).
	ErrCharityTokenReserveUnconfigured = errors.New("db: charity token reserve price is not configured")
	// ErrDonationKeyCapReached reports that the donation key's milli-credit
	// usage cap has no room left for the requested reserve (or the key lost a
	// concurrent race for the last room). The key stops being a candidate.
	ErrDonationKeyCapReached = errors.New("db: donation key usage cap reached")
)

// charityEnabledKeys lists the site_config representations accepted for the
// boolean charity switch. The administrator surface stores canonical "1"/"0";
// the tolerant read only absorbs manual tampering toward DISABLED, never the
// reverse (an unknown value fails closed).
func charityEnabledValue(raw string, present bool) bool {
	if !present {
		return false
	}
	return raw == "1" || raw == "true"
}

// readCharityEnabledTx reads the authoritative charity switch inside tx.
func readCharityEnabledTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	raw, err := readSiteConfigRowTx(ctx, tx, "charity_enabled")
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return charityEnabledValue(raw, true), nil
}

// readTokenReserveMilliTx reads charity_token_reserve_milli inside tx. found
// is false when the key is absent (token mode fails closed).
func readTokenReserveMilliTx(ctx context.Context, tx *sql.Tx) (value int64, found bool, err error) {
	raw, err := readSiteConfigRowTx(ctx, tx, "charity_token_reserve_milli")
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	v, perr := parseCheckinAmount(raw, -1)
	if perr != nil || v <= 0 {
		return 0, false, nil
	}
	return v, true, nil
}

// CharityPricingSnapshot is the frozen pricing projection taken at reservation
// creation (implementation contract §2.8): mode, discount, the per-request
// pair or the eight per-million unit prices, and the global token reserve
// actually used. Later configuration changes never affect an in-flight call.
type CharityPricingSnapshot struct {
	PricingMode           string
	DiscountPercent       int
	RequestUserPrice      int64
	RequestDonorReward    int64
	UncachedUserPrice     int64
	CacheWriteUserPrice   int64
	CacheReadUserPrice    int64
	OutputUserPrice       int64
	UncachedDonorReward   int64
	CacheWriteDonorReward int64
	CacheReadDonorReward  int64
	OutputDonorReward     int64
	TokenReserveMilli     int64
}

// ComputeCharityReserves applies the frozen §5.2 formulas to one snapshot:
//
//	per-token: user_reserve = ceil(token_reserve × discount / 100);
//	           key_reserve  = token_reserve (undiscounted)
//	per-request: user_reserve = discounted fixed price;
//	             key_reserve  = original fixed price
//
// All arithmetic goes through the shared checked primitives and fails closed.
func ComputeCharityReserves(s CharityPricingSnapshot) (userReserve, keyReserve int64, err error) {
	switch s.PricingMode {
	case CharityPricingPerRequest:
		keyReserve = s.RequestUserPrice
	case CharityPricingPerToken:
		keyReserve = s.TokenReserveMilli
	default:
		return 0, 0, fmt.Errorf("%w: pricing mode", ErrInvalidValue)
	}
	userReserve, err = credits.ApplyDiscountPercent(keyReserve, s.DiscountPercent)
	if err != nil {
		return 0, 0, fmt.Errorf("compute charity reserves: %w", err)
	}
	return userReserve, keyReserve, nil
}

// pricingSnapshotOf projects one charity model row plus the effective discount
// onto the frozen snapshot shape.
func pricingSnapshotOf(m CharityModel, discountPercent int) CharityPricingSnapshot {
	return CharityPricingSnapshot{
		PricingMode:           m.PricingMode,
		DiscountPercent:       discountPercent,
		RequestUserPrice:      m.RequestUserPrice,
		RequestDonorReward:    m.RequestDonorReward,
		UncachedUserPrice:     m.UncachedUserPrice,
		CacheWriteUserPrice:   m.CacheWriteUserPrice,
		CacheReadUserPrice:    m.CacheReadUserPrice,
		OutputUserPrice:       m.OutputUserPrice,
		UncachedDonorReward:   m.UncachedDonorReward,
		CacheWriteDonorReward: m.CacheWriteDonorReward,
		CacheReadDonorReward:  m.CacheReadDonorReward,
		OutputDonorReward:     m.OutputDonorReward,
	}
}

// charityLedgerOpID derives the reserved-namespace operation id of one
// reservation phase. attempt_id is server-generated base32, so the derived id
// is always syntactically valid and can never collide across phases.
func charityLedgerOpID(attemptID, phase string) string {
	return SystemOperationPrefix + "charity." + attemptID + "." + phase
}

// CharityReservation is one persisted logical charity call. Secret material
// never exists on this row: base_url/upstream_model are the safe bounded
// snapshots the log contract already exposes to their owner.
type CharityReservation struct {
	ID             int64
	UserID         int64
	DonorUserID    *int64
	CharityModelID *int64
	DonationKeyID  *int64
	AttemptID      string
	ModelSnapshot  string
	BaseURL        string
	UpstreamModel  string
	State          string

	Snapshot CharityPricingSnapshot

	TokenReserveMilli int64
	UserReserved      int64
	KeyReserved       int64
	OriginalCharge    int64
	UserCharge        int64
	DonorReward       int64

	UsageUncachedInputTokens   int64
	UsageCacheWriteInputTokens int64
	UsageCacheReadInputTokens  int64
	UsageOutputTokens          int64
	UsageUnknown               bool

	CreatedAt    int64
	DispatchedAt *int64
	FinalizedAt  *int64
}

const charityReservationSelectSQL = `
SELECT id, user_id, donor_user_id, charity_model_id, donation_key_id, attempt_id,
       model_snapshot, base_url, upstream_model, state, pricing_mode, discount_percent,
       request_user_price, request_donor_reward,
       uncached_user_price, cache_write_user_price, cache_read_user_price, output_user_price,
       uncached_donor_reward, cache_write_donor_reward, cache_read_donor_reward, output_donor_reward,
       token_reserve_milli, user_reserved, key_reserved, original_charge, user_charge, donor_reward,
       usage_uncached_input_tokens, usage_cache_write_input_tokens, usage_cache_read_input_tokens,
       usage_output_tokens, usage_unknown,
       created_at, dispatched_at, finalized_at
FROM charity_reservations`

func scanCharityReservationRow(row *sql.Row) (CharityReservation, error) {
	var (
		r                         CharityReservation
		donor, modelID, keyID     sql.NullInt64
		dispatchedAt, finalizedAt sql.NullInt64
		usageUnknown              int
		s                         = &r.Snapshot
	)
	err := row.Scan(&r.ID, &r.UserID, &donor, &modelID, &keyID, &r.AttemptID,
		&r.ModelSnapshot, &r.BaseURL, &r.UpstreamModel, &r.State, &s.PricingMode, &s.DiscountPercent,
		&s.RequestUserPrice, &s.RequestDonorReward,
		&s.UncachedUserPrice, &s.CacheWriteUserPrice, &s.CacheReadUserPrice, &s.OutputUserPrice,
		&s.UncachedDonorReward, &s.CacheWriteDonorReward, &s.CacheReadDonorReward, &s.OutputDonorReward,
		&r.TokenReserveMilli, &r.UserReserved, &r.KeyReserved, &r.OriginalCharge, &r.UserCharge, &r.DonorReward,
		&r.UsageUncachedInputTokens, &r.UsageCacheWriteInputTokens, &r.UsageCacheReadInputTokens,
		&r.UsageOutputTokens, &usageUnknown,
		&r.CreatedAt, &dispatchedAt, &finalizedAt)
	if err != nil {
		return CharityReservation{}, err
	}
	r.DonorUserID = nullInt64Ptr(donor)
	r.CharityModelID = nullInt64Ptr(modelID)
	r.DonationKeyID = nullInt64Ptr(keyID)
	r.DispatchedAt = nullInt64Ptr(dispatchedAt)
	r.FinalizedAt = nullInt64Ptr(finalizedAt)
	r.UsageUnknown = usageUnknown == 1
	return r, nil
}

// GetCharityReservation loads one reservation row by id.
func (s *Store) GetCharityReservation(ctx context.Context, id int64) (CharityReservation, error) {
	if id <= 0 {
		return CharityReservation{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, charityReservationSelectSQL+` WHERE id=?`, id)
	res, err := scanCharityReservationRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CharityReservation{}, ErrNotFound
	}
	if err != nil {
		return CharityReservation{}, fmt.Errorf("get charity reservation: %w", err)
	}
	return res, nil
}

// GetCharityReservationByAttempt loads one reservation by its unique attempt
// correlation id.
func (s *Store) GetCharityReservationByAttempt(ctx context.Context, attemptID string) (CharityReservation, error) {
	if attemptID == "" {
		return CharityReservation{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, charityReservationSelectSQL+` WHERE attempt_id=?`, attemptID)
	res, err := scanCharityReservationRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CharityReservation{}, ErrNotFound
	}
	if err != nil {
		return CharityReservation{}, fmt.Errorf("get charity reservation by attempt: %w", err)
	}
	return res, nil
}

// reserveDonationKeyCapTx conditionally reserves amount milli-credits against
// one donation key's usage cap inside tx (frozen §5.2): cap=0 is unlimited;
// otherwise used + reserved + amount <= cap with checked addition. The UPDATE
// guard re-checks enabled and the exact previous reserved value so the write
// lands only on the state the admission decision saw. A lost race is an
// admission failure for this key, never a partial write.
func reserveDonationKeyCapTx(ctx context.Context, tx *sql.Tx, donationKeyID, amount, now int64) error {
	var enabled int
	var capValue, used, reserved int64
	err := tx.QueryRowContext(ctx, `
SELECT enabled, credits_usage_cap, credits_used, credits_reserved
FROM donation_keys WHERE id=?`, donationKeyID).
		Scan(&enabled, &capValue, &used, &reserved)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("charity key cap read: %w", err)
	}
	if enabled != 1 || amount < 0 {
		return ErrDonationKeyCapReached
	}
	newReserved, aerr := credits.Add(reserved, amount)
	if aerr != nil {
		return fmt.Errorf("charity key cap reserve: %w", aerr)
	}
	if capValue > 0 {
		consumed, cerr := credits.Add(used, newReserved)
		if cerr != nil {
			return fmt.Errorf("charity key cap reserve: %w", cerr)
		}
		if consumed > capValue {
			return ErrDonationKeyCapReached
		}
	}
	res, uerr := tx.ExecContext(ctx, `
UPDATE donation_keys SET credits_reserved=?, updated_at=?
WHERE id=? AND enabled=1 AND credits_reserved=?`, newReserved, now, donationKeyID, reserved)
	if uerr != nil {
		return fmt.Errorf("charity key cap reserve: %w", uerr)
	}
	affected, uerr := res.RowsAffected()
	if uerr != nil {
		return fmt.Errorf("charity key cap reserve rows: %w", uerr)
	}
	if affected == 0 {
		return ErrDonationKeyCapReached // lost the guarded update; nothing written
	}
	return nil
}

// releaseDonationKeyCapTx refunds amount milli-credits of one donation key's
// reserved counter inside tx. A missing key row is tolerated (the physical key
// was deleted; its SET NULL already detached every reservation). A guard miss
// fails closed: it would mean the reserved counter drifted from what the
// reservation recorded, which nothing in the frozen flow can produce.
func releaseDonationKeyCapTx(ctx context.Context, tx *sql.Tx, donationKeyID, amount, now int64) error {
	if donationKeyID <= 0 || amount < 0 {
		return nil
	}
	var reserved int64
	err := tx.QueryRowContext(ctx,
		`SELECT credits_reserved FROM donation_keys WHERE id=?`, donationKeyID).Scan(&reserved)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // the key row is gone; nothing left to release
	}
	if err != nil {
		return fmt.Errorf("charity key release read: %w", err)
	}
	newReserved, aerr := credits.Sub(reserved, amount)
	if aerr != nil || newReserved < 0 {
		return fmt.Errorf("charity key release: reserved counter drift")
	}
	if newReserved == reserved {
		return nil
	}
	res, uerr := tx.ExecContext(ctx, `
UPDATE donation_keys SET credits_reserved=?, updated_at=?
WHERE id=? AND credits_reserved=?`, newReserved, now, donationKeyID, reserved)
	if uerr != nil {
		return fmt.Errorf("charity key release: %w", uerr)
	}
	affected, uerr := res.RowsAffected()
	if uerr != nil {
		return fmt.Errorf("charity key release rows: %w", uerr)
	}
	if affected == 0 {
		return fmt.Errorf("charity key release: lost the guarded update")
	}
	return nil
}

// settleDonationKeyTx converts one donation key's reserved counter into the
// used counter inside tx (settlement): reserved -= keyReserved, used +=
// original. Crossing the cap is FROZEN behavior (§5.3): the already-admitted
// request settles in full even when original exceeds the cap; the next
// admission then refuses this key. A deleted key row is tolerated.
func settleDonationKeyTx(ctx context.Context, tx *sql.Tx, donationKeyID, keyReserved, original, now int64) error {
	if donationKeyID <= 0 {
		return nil
	}
	var used, reserved int64
	err := tx.QueryRowContext(ctx, `
SELECT credits_used, credits_reserved FROM donation_keys WHERE id=?`, donationKeyID).
		Scan(&used, &reserved)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // key deleted mid-flight; FK already detached the reference
	}
	if err != nil {
		return fmt.Errorf("charity key settle read: %w", err)
	}
	newUsed, aerr := credits.Add(used, original)
	if aerr != nil {
		return fmt.Errorf("charity key settle: %w", aerr)
	}
	newReserved, aerr := credits.Sub(reserved, keyReserved)
	if aerr != nil || newReserved < 0 {
		return fmt.Errorf("charity key settle: reserved counter drift")
	}
	res, uerr := tx.ExecContext(ctx, `
UPDATE donation_keys SET credits_used=?, credits_reserved=?, updated_at=?
WHERE id=? AND credits_used=? AND credits_reserved=?`, newUsed, newReserved, now, donationKeyID, used, reserved)
	if uerr != nil {
		return fmt.Errorf("charity key settle: %w", uerr)
	}
	affected, uerr := res.RowsAffected()
	if uerr != nil {
		return fmt.Errorf("charity key settle rows: %w", uerr)
	}
	if affected == 0 {
		return fmt.Errorf("charity key settle: lost the guarded update")
	}
	return nil
}

// callerAdmissionTx verifies the caller-side admission gates INSIDE the
// caller's transaction and lazily lifts due time-based states first:
// account present, not banned, not charity-suspended, not the administrator.
func callerAdmissionTx(ctx context.Context, tx *sql.Tx, userID, now int64) error {
	if _, err := liftDueUserBanTx(tx, userID, now); err != nil {
		return err
	}
	if _, err := clearDueCharitySuspensionTx(tx, userID, now); err != nil {
		return err
	}
	var (
		isBanned  int
		suspended sql.NullInt64
		isAdmin   int
	)
	err := tx.QueryRowContext(ctx,
		`SELECT is_banned, charity_suspended_until, is_admin FROM users WHERE id=?`, userID).
		Scan(&isBanned, &suspended, &isAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("charity caller admission: %w", err)
	}
	if isAdmin != 0 {
		return ErrAdminProtected
	}
	if isBanned != 0 {
		return ErrNotFound // indistinguishable from absent at the routing exit
	}
	if suspended.Valid && suspended.Int64 > now {
		return ErrCharitySuspended
	}
	return nil
}

// ReserveCharityInput is one charity pre-reserve admission for a caller and a
// chosen donated key. The service layer picks the candidate; this transaction
// re-validates EVERYTHING atomically before any money moves.
type ReserveCharityInput struct {
	UserID        int64
	FullName      string // '[公益]...' routing key
	BindingID     int64
	DonationKeyID int64
	AttemptID     string // server-generated opaque correlation id
	BaseURL       string // bounded canonical base-URL snapshot of the candidate's endpoint
	Now           int64
}

// MaxCharityRouteCandidates mirrors the personal route projection limit so
// both namespaces share one finite retry boundary.
const MaxCharityRouteCandidates = 256

// CreateCharityReservation runs the frozen §5.2 creation transaction:
//
//	validations (feature/user/model/candidate/claim/expiry/suspension)
//	→ donation-key cap conditional reserve
//	→ user balance conditional debit (only user_reserve > 0)
//	→ INSERT reservation + ledger row(s)
//
// The pricing/discount snapshot is taken from the model row INSIDE this
// transaction. Any failure rolls back everything: an uncovered balance never
// leaves a reservation behind, and no dispatch may start.
func (s *Store) CreateCharityReservation(ctx context.Context, in ReserveCharityInput) (CharityReservation, CharityPricingSnapshot, error) {
	if in.UserID <= 0 || in.FullName == "" || in.BindingID <= 0 || in.DonationKeyID <= 0 ||
		in.AttemptID == "" || len(in.AttemptID) > MaxAttemptIDLen || in.Now <= 0 {
		return CharityReservation{}, CharityPricingSnapshot{}, fmt.Errorf("%w: reservation identity", ErrInvalidValue)
	}
	if !validAttemptID(in.AttemptID) {
		return CharityReservation{}, CharityPricingSnapshot{}, fmt.Errorf("%w: attempt id", ErrInvalidValue)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CharityReservation{}, CharityPricingSnapshot{}, fmt.Errorf("create charity reservation: begin: %w", err)
	}
	reservation, snapshot, err := s.createCharityReservationTx(ctx, tx, in)
	if err != nil {
		_ = tx.Rollback()
		return CharityReservation{}, CharityPricingSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return CharityReservation{}, CharityPricingSnapshot{}, fmt.Errorf("create charity reservation: commit: %w", err)
	}
	return reservation, snapshot, nil
}

func (s *Store) createCharityReservationTx(ctx context.Context, tx *sql.Tx, in ReserveCharityInput) (CharityReservation, CharityPricingSnapshot, error) {
	if err := sweepExpiredDonationsTx(ctx, tx, in.Now); err != nil {
		return CharityReservation{}, CharityPricingSnapshot{}, err
	}
	enabled, err := readCharityEnabledTx(ctx, tx)
	if err != nil {
		return CharityReservation{}, CharityPricingSnapshot{}, err
	}
	if !enabled {
		return CharityReservation{}, CharityPricingSnapshot{}, ErrCharityDisabled
	}
	if err := callerAdmissionTx(ctx, tx, in.UserID, in.Now); err != nil {
		return CharityReservation{}, CharityPricingSnapshot{}, err
	}
	route, err := resolveCharityRouteTx(ctx, tx, in.FullName, in.Now, MaxCharityRouteCandidates)
	if err != nil {
		return CharityReservation{}, CharityPricingSnapshot{}, err
	}
	var chosen *CharityCandidate
	for i := range route.Candidates {
		if route.Candidates[i].BindingID == in.BindingID && route.Candidates[i].DonationKeyID == in.DonationKeyID {
			chosen = &route.Candidates[i]
			break
		}
	}
	if chosen == nil {
		// The selected candidate died between selection and this transaction,
		// or was never part of the projection: fail closed without charging.
		return CharityReservation{}, CharityPricingSnapshot{}, ErrNotFound
	}
	snapshot := pricingSnapshotOf(route.Model, route.Model.EffectiveDiscountPercent(in.Now))
	userReserve, keyReserve, err := ComputeCharityReserves(snapshot)
	if err != nil {
		return CharityReservation{}, CharityPricingSnapshot{}, err
	}
	if snapshot.PricingMode == CharityPricingPerToken {
		tokenReserve, found, terr := readTokenReserveMilliTx(ctx, tx)
		if terr != nil {
			return CharityReservation{}, CharityPricingSnapshot{}, terr
		}
		if !found {
			// Token mode fails closed while the global per-token reserve is
			// unconfigured — even if the model row was enabled earlier.
			return CharityReservation{}, CharityPricingSnapshot{}, ErrCharityTokenReserveUnconfigured
		}
		snapshot.TokenReserveMilli = tokenReserve
		userReserve, keyReserve, err = ComputeCharityReserves(snapshot)
		if err != nil {
			return CharityReservation{}, CharityPricingSnapshot{}, err
		}
	}

	// Donation-key cap conditional reserve (authoritative check).
	if err := reserveDonationKeyCapTx(ctx, tx, chosen.DonationKeyID, keyReserve, in.Now); err != nil {
		return CharityReservation{}, CharityPricingSnapshot{}, err
	}
	rollbackKey := true
	defer func() {
		if rollbackKey {
			_ = releaseDonationKeyCapTx(ctx, tx, chosen.DonationKeyID, keyReserve, in.Now)
		}
	}()

	// User balance conditional pre-debit. A zero-amount reservation still
	// writes its zero-delta ledger row so every admitted call shares one
	// idempotent accounting path regardless of price.
	reserveOp := CreditOperation{
		Kind:           LedgerCharityReserve,
		UserID:         in.UserID,
		OperationID:    charityLedgerOpID(in.AttemptID, "reserve"),
		CreditsDelta:   -userReserve,
		Reason:         "charity reserve",
		CreatedAt:      time.Unix(in.Now, 0),
		hasCreditFloor: userReserve > 0,
		creditFloor:    userReserve,
	}
	if _, err := s.applyCreditOperationTx(ctx, tx, reserveOp); err != nil {
		return CharityReservation{}, CharityPricingSnapshot{}, err
	}

	res, ierr := tx.ExecContext(ctx, `
INSERT INTO charity_reservations
	(user_id, donor_user_id, charity_model_id, donation_key_id, attempt_id,
	 model_snapshot, base_url, upstream_model, state, pricing_mode, discount_percent,
	 request_user_price, request_donor_reward,
	 uncached_user_price, cache_write_user_price, cache_read_user_price, output_user_price,
	 uncached_donor_reward, cache_write_donor_reward, cache_read_donor_reward, output_donor_reward,
	 token_reserve_milli, user_reserved, key_reserved,
	 created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'reserved', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.UserID, chosen.DonorUserID, route.Model.ID, chosen.DonationKeyID, in.AttemptID,
		route.Model.FullName, in.BaseURL, chosen.UpstreamModelID, snapshot.PricingMode, snapshot.DiscountPercent,
		snapshot.RequestUserPrice, snapshot.RequestDonorReward,
		snapshot.UncachedUserPrice, snapshot.CacheWriteUserPrice, snapshot.CacheReadUserPrice, snapshot.OutputUserPrice,
		snapshot.UncachedDonorReward, snapshot.CacheWriteDonorReward, snapshot.CacheReadDonorReward, snapshot.OutputDonorReward,
		snapshot.TokenReserveMilli, userReserve, keyReserve,
		in.Now, in.Now)
	if ierr != nil {
		if isConstraintError(ierr) {
			return CharityReservation{}, CharityPricingSnapshot{}, ErrConflict // duplicate attempt id
		}
		return CharityReservation{}, CharityPricingSnapshot{}, fmt.Errorf("insert charity reservation: %w", ierr)
	}
	id, ierr := res.LastInsertId()
	if ierr != nil {
		return CharityReservation{}, CharityPricingSnapshot{}, fmt.Errorf("charity reservation id: %w", ierr)
	}
	rollbackKey = false
	stored, rerr := scanCharityReservationRow(
		tx.QueryRowContext(ctx, charityReservationSelectSQL+` WHERE id=?`, id))
	if rerr != nil {
		return CharityReservation{}, CharityPricingSnapshot{}, fmt.Errorf("read charity reservation: %w", rerr)
	}
	return stored, snapshot, nil
}

// SwapCharityReservationKey atomically releases the previous donated key's
// reserve and takes the new key's reserve, moving the reservation pointer in
// the SAME transaction (frozen §2.8). The user balance is never touched: the
// retry reserves from the user exactly once. The CAS on state='reserved'
// guarantees the swap can never race with a settlement or a release.
func (s *Store) SwapCharityReservationKey(ctx context.Context, reservationID int64,
	newKey CharityCandidate, newKeyReserve int64, now int64) error {
	if reservationID <= 0 || newKey.BindingID <= 0 || newKey.DonationKeyID <= 0 || newKeyReserve < 0 || now <= 0 {
		return fmt.Errorf("%w: swap identity", ErrInvalidValue)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("swap charity key: begin: %w", err)
	}
	err = func() error {
		var current CharityReservation
		row := tx.QueryRowContext(ctx, charityReservationSelectSQL+` WHERE id=?`, reservationID)
		current, err = scanCharityReservationRow(row)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("swap charity key: read: %w", err)
		}
		if current.State != string(credits.StateReserved) {
			return credits.ErrIllegalTransition
		}
		oldKeyID := int64(0)
		if current.DonationKeyID != nil {
			oldKeyID = *current.DonationKeyID
		}
		if oldKeyID == newKey.DonationKeyID {
			return nil // same key, nothing to move
		}
		if err := releaseDonationKeyCapTx(ctx, tx, oldKeyID, current.KeyReserved, now); err != nil {
			return err
		}
		if err := reserveDonationKeyCapTx(ctx, tx, newKey.DonationKeyID, newKeyReserve, now); err != nil {
			return err
		}
		// CAS the pointer + amounts onto the still-reserved row. The guard on
		// donation_key_id/key_reserved pins the exact prior swap state.
		var oldKeyAny any
		if current.DonationKeyID != nil {
			oldKeyAny = *current.DonationKeyID
		}
		res, uerr := tx.ExecContext(ctx, `
UPDATE charity_reservations SET donation_key_id=?, key_reserved=?, updated_at=?
WHERE id=? AND state='reserved' AND donation_key_id IS ? AND key_reserved=?`,
			newKey.DonationKeyID, newKeyReserve, now, reservationID,
			oldKeyAny, current.KeyReserved)
		if uerr != nil {
			return fmt.Errorf("swap charity key: update: %w", uerr)
		}
		affected, uerr := res.RowsAffected()
		if uerr != nil {
			return fmt.Errorf("swap charity key rows: %w", uerr)
		}
		if affected == 0 {
			return credits.ErrIllegalTransition
		}
		return nil
	}()
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("swap charity key: commit: %w", err)
	}
	return nil
}

// DispatchCharityReservation performs the reserved→dispatched CAS (frozen
// §5.3): it changes ONLY the state and timestamp, releasing nothing. applied
// is false when the row already left `reserved` (a concurrent terminal move
// won); the caller then treats the attempt as failed and settles nothing.
func (s *Store) DispatchCharityReservation(ctx context.Context, reservationID, now int64) (bool, error) {
	if reservationID <= 0 || now <= 0 {
		return false, fmt.Errorf("%w: dispatch identity", ErrInvalidValue)
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE charity_reservations SET state='dispatched', dispatched_at=?, updated_at=?
WHERE id=? AND state='reserved'`, now, now, reservationID)
	if err != nil {
		return false, fmt.Errorf("dispatch charity reservation: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("dispatch charity reservation rows: %w", err)
	}
	return affected == 1, nil
}

// UndispatchCharityReservation performs the dispatched→reserved CAS used ONLY
// by the dispatch writer's provable zero-byte compensation (frozen §5.3 /
// clarification §C1.5): when the first non-empty body Write delegates and the
// underlying writer returns ZERO bytes, the "first body byte successfully
// submitted" boundary was never crossed, so the service must be able to take
// the row back to `reserved` and release / retry it. applied is false when the
// row already left `dispatched` (a concurrent terminal move — recovery or
// account-delete convergence — won); the caller then treats the attempt as a
// pre-dispatch failure whose release is an idempotent no-op (the terminal
// winner has already settled the row conservatively). This transition never
// releases the user reserve and never touches the donation-key cap; both stay
// exactly as the dispatch left them, so a successful revert is a pure state
// unwind that the normal reserved→released or reserved→swap paths complete.
func (s *Store) UndispatchCharityReservation(ctx context.Context, reservationID, now int64) (bool, error) {
	if reservationID <= 0 || now <= 0 {
		return false, fmt.Errorf("%w: undispatch identity", ErrInvalidValue)
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE charity_reservations SET state='reserved', dispatched_at=NULL, updated_at=?
WHERE id=? AND state='dispatched'`, now, reservationID)
	if err != nil {
		return false, fmt.Errorf("undispatch charity reservation: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("undispatch charity reservation rows: %w", err)
	}
	return affected == 1, nil
}

// CommitPlan carries the settlement values computed by the service layer from
// the connector's usage report and the PERSISTED snapshot (never current
// configuration). All amounts are non-negative milli-credits.
type CommitPlan struct {
	OriginalCharge int64
	UserCharge     int64
	DonorReward    int64

	UncachedInputTokens   int64
	CacheWriteInputTokens int64
	CacheReadInputTokens  int64
	OutputTokens          int64
	UsageUnknown          bool
}

// CommitCharityReservation executes the dispatched→committed transition and
// its complete accounting inside ONE transaction (frozen §5.3):
//
//   - CAS dispatched→committed (a repeated callback lands on zero rows and
//     becomes a no-op that returns the stored first result);
//   - the donation key converts reserved → used at the ORIGINAL charge (the
//     cap may be crossed; the next admission refuses this key);
//   - the user is settled against their reserve: refund when actual < reserve,
//     additional charge (possibly driving the balance negative) when actual >
//     reserve — always through one idempotent ledger operation;
//   - the donor reward adds to BOTH credits and donation_credit of the donor;
//     a deleted (or administrator) donor abandons the reward but NEVER blocks
//     the consumer settlement.
func (s *Store) CommitCharityReservation(ctx context.Context, reservationID int64, plan CommitPlan, now int64) (CharityReservation, error) {
	if reservationID <= 0 || now <= 0 {
		return CharityReservation{}, fmt.Errorf("%w: commit identity", ErrInvalidValue)
	}
	if plan.OriginalCharge < 0 || plan.UserCharge < 0 || plan.DonorReward < 0 ||
		plan.UncachedInputTokens < 0 || plan.CacheWriteInputTokens < 0 ||
		plan.CacheReadInputTokens < 0 || plan.OutputTokens < 0 {
		return CharityReservation{}, fmt.Errorf("%w: negative settlement value", ErrInvalidValue)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CharityReservation{}, fmt.Errorf("commit charity reservation: begin: %w", err)
	}
	result, err := s.commitCharityReservationTx(ctx, tx, reservationID, plan, now)
	if err != nil {
		_ = tx.Rollback()
		return CharityReservation{}, err
	}
	if err := tx.Commit(); err != nil {
		return CharityReservation{}, fmt.Errorf("commit charity reservation: commit: %w", err)
	}
	return result, nil
}

// commitCharityReservationTx is the shared settlement body; the account-delete
// convergence path calls it inside its own deletion transaction.
func (s *Store) commitCharityReservationTx(ctx context.Context, tx *sql.Tx, reservationID int64, plan CommitPlan, now int64) (CharityReservation, error) {
	var current CharityReservation
	row := tx.QueryRowContext(ctx, charityReservationSelectSQL+` WHERE id=?`, reservationID)
	current, err := scanCharityReservationRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CharityReservation{}, ErrNotFound
	}
	if err != nil {
		return CharityReservation{}, fmt.Errorf("settle charity reservation: read: %w", err)
	}
	switch current.State {
	case "committed":
		return current, nil // replayed callback: first settlement wins, nothing re-applies
	case "dispatched":
		// proceed below
	default:
		// reserved can never jump to committed; released is terminal.
		return CharityReservation{}, credits.ErrIllegalTransition
	}
	unknownInt := 0
	if plan.UsageUnknown {
		unknownInt = 1
	}
	res, uerr := tx.ExecContext(ctx, `
UPDATE charity_reservations SET state='committed',
	original_charge=?, user_charge=?, donor_reward=?,
	usage_uncached_input_tokens=?, usage_cache_write_input_tokens=?,
	usage_cache_read_input_tokens=?, usage_output_tokens=?, usage_unknown=?,
	finalized_at=?, updated_at=?
WHERE id=? AND state='dispatched'`,
		plan.OriginalCharge, plan.UserCharge, plan.DonorReward,
		plan.UncachedInputTokens, plan.CacheWriteInputTokens,
		plan.CacheReadInputTokens, plan.OutputTokens, unknownInt,
		now, now, reservationID)
	if uerr != nil {
		return CharityReservation{}, fmt.Errorf("settle charity reservation: %w", uerr)
	}
	affected, uerr := res.RowsAffected()
	if uerr != nil {
		return CharityReservation{}, fmt.Errorf("settle charity reservation rows: %w", uerr)
	}
	if affected == 0 {
		// A concurrent winner moved the row between our read and our write;
		// re-read and surface the stored result without re-applying effects.
		reread, rerr := scanCharityReservationRow(
			tx.QueryRowContext(ctx, charityReservationSelectSQL+` WHERE id=?`, reservationID))
		if rerr != nil {
			return CharityReservation{}, fmt.Errorf("settle charity reservation reread: %w", rerr)
		}
		if reread.State == "committed" {
			return reread, nil
		}
		return CharityReservation{}, credits.ErrIllegalTransition
	}

	keyID := int64(0)
	if current.DonationKeyID != nil {
		keyID = *current.DonationKeyID
	}
	if err := settleDonationKeyTx(ctx, tx, keyID, current.KeyReserved, plan.OriginalCharge, now); err != nil {
		return CharityReservation{}, err
	}

	// User settlement: net delta relative to the taken reserve. The delta may
	// be negative (over-reserved actual): users.credits is allowed to go
	// negative by frozen design and the checked arithmetic fails closed on
	// overflow instead of wrapping.
	settleDelta, derr := credits.Sub(current.UserReserved, plan.UserCharge)
	if derr != nil {
		return CharityReservation{}, fmt.Errorf("settle charity reservation: %w", derr)
	}
	settleOp := CreditOperation{
		Kind:         LedgerCharitySettlement,
		UserID:       current.UserID,
		OperationID:  charityLedgerOpID(current.AttemptID, "settle"),
		CreditsDelta: settleDelta,
		Reason:       "charity settlement",
		CreatedAt:    time.Unix(now, 0),
	}
	if _, serr := s.applyCreditOperationTx(ctx, tx, settleOp); serr != nil {
		return CharityReservation{}, fmt.Errorf("charity settlement ledger: %w", serr)
	}

	// Donor reward: both balances rise together. A donor who was deleted
	// (FK SET NULL) or is the environment-owned administrator abandons the
	// reward; the consumption settlement above is unaffected.
	if plan.DonorReward > 0 && current.DonorUserID != nil && *current.DonorUserID > 0 {
		rewardOp := CreditOperation{
			Kind:                LedgerDonorReward,
			UserID:              *current.DonorUserID,
			OperationID:         charityLedgerOpID(current.AttemptID, "reward"),
			CreditsDelta:        plan.DonorReward,
			DonationCreditDelta: plan.DonorReward,
			Reason:              "donor reward",
			CreatedAt:           time.Unix(now, 0),
		}
		if _, rerr := s.applyCreditOperationTx(ctx, tx, rewardOp); rerr != nil {
			switch {
			case errors.Is(rerr, ErrNotFound), errors.Is(rerr, ErrAdminProtected):
				// Reward abandoned (donor deleted or administrator); the
				// consumer settlement above stays final.
			case errors.Is(rerr, ErrDonationCreditNegative):
				return CharityReservation{}, fmt.Errorf("donor reward: %w", rerr)
			default:
				return CharityReservation{}, fmt.Errorf("donor reward ledger: %w", rerr)
			}
		}
	}
	return scanCharityReservationRow(
		tx.QueryRowContext(ctx, charityReservationSelectSQL+` WHERE id=?`, reservationID))
}

// ReleaseCharityReservation performs the reserved→released transition and its
// full refund inside ONE transaction (frozen §5.3). Replayed releases and
// calls against terminal rows are atomic no-ops. A dispatched reservation can
// never be released here: the CAS matches zero rows and the sentinel is
// returned so a caller bug surfaces loudly.
func (s *Store) ReleaseCharityReservation(ctx context.Context, reservationID, now int64) (bool, error) {
	if reservationID <= 0 || now <= 0 {
		return false, fmt.Errorf("%w: release identity", ErrInvalidValue)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("release charity reservation: begin: %w", err)
	}
	applied, err := s.releaseCharityReservationTx(ctx, tx, reservationID, now)
	if err != nil {
		_ = tx.Rollback()
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("release charity reservation: commit: %w", err)
	}
	return applied, nil
}

// releaseCharityReservationTx is the shared release body; the account-delete
// convergence path calls it inside its own deletion transaction.
func (s *Store) releaseCharityReservationTx(ctx context.Context, tx *sql.Tx, reservationID, now int64) (bool, error) {
	var current CharityReservation
	row := tx.QueryRowContext(ctx, charityReservationSelectSQL+` WHERE id=?`, reservationID)
	current, err := scanCharityReservationRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("release charity reservation: read: %w", err)
	}
	switch current.State {
	case "released":
		return false, nil // already terminal: idempotent no-op
	case "reserved":
		// proceed below
	default:
		// dispatched can never be released (frozen §5.3).
		return false, credits.ErrIllegalTransition
	}
	res, uerr := tx.ExecContext(ctx, `
UPDATE charity_reservations SET state='released', finalized_at=?, updated_at=?
WHERE id=? AND state='reserved'`, now, now, reservationID)
	if uerr != nil {
		return false, fmt.Errorf("release charity reservation: %w", uerr)
	}
	affected, uerr := res.RowsAffected()
	if uerr != nil {
		return false, fmt.Errorf("release charity reservation rows: %w", uerr)
	}
	if affected == 0 {
		return false, credits.ErrIllegalTransition
	}
	keyID := int64(0)
	if current.DonationKeyID != nil {
		keyID = *current.DonationKeyID
	}
	if err := releaseDonationKeyCapTx(ctx, tx, keyID, current.KeyReserved, now); err != nil {
		return false, err
	}
	releaseOp := CreditOperation{
		Kind:         LedgerCharityRelease,
		UserID:       current.UserID,
		OperationID:  charityLedgerOpID(current.AttemptID, "release"),
		CreditsDelta: current.UserReserved,
		Reason:       "charity release",
		CreatedAt:    time.Unix(now, 0),
	}
	if _, rerr := s.applyCreditOperationTx(ctx, tx, releaseOp); rerr != nil {
		return false, fmt.Errorf("charity release ledger: %w", rerr)
	}
	return true, nil
}

// StaleCharityReservation is one stalled reservation surfaced by the recovery
// sweep, carrying only the fields the convergence decision needs.
type StaleCharityReservation struct {
	ID    int64
	State string
}

// ListStalledCharityReservations returns reserved/dispatched reservations
// whose last activity (created_at for reserved, dispatched_at for dispatched)
// is strictly older than beforeUnix, oldest first, bounded. Terminal rows are
// never listed and in-flight rows younger than the cutoff are never touched,
// so a periodic sweep can never release a live request.
func (s *Store) ListStalledCharityReservations(ctx context.Context, beforeUnix int64, limit int) ([]StaleCharityReservation, error) {
	if limit <= 0 {
		return nil, ErrInvalidValue
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, state FROM charity_reservations
WHERE state IN ('reserved','dispatched')
  AND COALESCE(dispatched_at, created_at) < ?
ORDER BY id LIMIT ?`, beforeUnix, limit)
	if err != nil {
		return nil, fmt.Errorf("list stalled charity reservations: %w", err)
	}
	defer rows.Close()
	out := make([]StaleCharityReservation, 0, min(limit, 16))
	for rows.Next() {
		var row StaleCharityReservation
		if err := rows.Scan(&row.ID, &row.State); err != nil {
			return nil, fmt.Errorf("scan stalled charity reservation: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stalled charity reservations: %w", err)
	}
	return out, nil
}

// RecoverCharityReservation converges ONE stalled reservation to its frozen
// terminal target using ONLY the persisted snapshot (frozen §5.4):
//
//	reserved   → released (refund user reserve; release key reserve)
//	dispatched → committed unknown (user keeps the discounted reserve as the
//	             charge; the key consumes the undiscounted reserve; reward 0;
//	             usage_unknown=1)
//
// The CAS guards make it idempotent and safe to re-run after a crash at any
// point; a row that reached a terminal state between listing and recovery is
// reported as already converged (false), never double-applied.
func (s *Store) RecoverCharityReservation(ctx context.Context, reservationID, now int64) (bool, error) {
	row := s.db.QueryRowContext(ctx, charityReservationSelectSQL+` WHERE id=?`, reservationID)
	current, err := scanCharityReservationRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("recover charity reservation: read: %w", err)
	}
	target, needed := credits.RecoveryTarget(credits.ReservationState(current.State))
	if !needed {
		return false, nil
	}
	switch target {
	case credits.StateReleased:
		applied, rerr := s.ReleaseCharityReservation(ctx, reservationID, now)
		if errors.Is(rerr, credits.ErrIllegalTransition) {
			return false, nil // concurrent terminal move won; nothing to do
		}
		return applied, rerr
	case credits.StateCommitted:
		plan := CommitPlan{
			OriginalCharge: current.KeyReserved,
			UserCharge:     current.UserReserved,
			DonorReward:    0,
			UsageUnknown:   true,
		}
		result, cerr := s.CommitCharityReservation(ctx, reservationID, plan, now)
		if errors.Is(cerr, credits.ErrIllegalTransition) {
			return false, nil // concurrent terminal move won; nothing to do
		}
		if cerr != nil {
			return false, cerr
		}
		return result.State == string(credits.StateCommitted), nil
	default:
		return false, credits.ErrIllegalTransition
	}
}

// CleanupTerminalCharityReservations removes terminal reservations whose
// finalized_at is older than cutoffUnix inside bounded batches (frozen §7:
// terminal 400 days; in-flight rows are never removed by age). It reports
// whether more work remains and stops early when ctx is canceled.
func (s *Store) CleanupTerminalCharityReservations(ctx context.Context, cutoffUnix int64) (removed int64, remaining bool, err error) {
	const batchSize = 200
	for i := 0; i < 50; i++ { // hard batch bound per invocation
		if ctx != nil && ctx.Err() != nil {
			return removed, true, nil
		}
		res, eerr := s.db.ExecContext(ctx, `
DELETE FROM charity_reservations
WHERE id IN (
	SELECT id FROM charity_reservations
	WHERE state IN ('committed','released') AND finalized_at IS NOT NULL AND finalized_at < ?
	ORDER BY id LIMIT ?
)`, cutoffUnix, batchSize)
		if eerr != nil {
			return removed, false, fmt.Errorf("cleanup terminal charity reservations: %w", eerr)
		}
		n, eerr := res.RowsAffected()
		if eerr != nil {
			return removed, false, fmt.Errorf("cleanup terminal charity reservations rows: %w", eerr)
		}
		removed += n
		if n < batchSize {
			return removed, false, nil
		}
	}
	return removed, true, nil
}

// convergeCharityReservationsForUserDeleteTx releases every reserved and
// commits every dispatched (unknown-usage) reservation of userID INSIDE the
// caller's account-deletion transaction, so a late settlement callback can
// only observe a terminal state and become a no-op (frozen §5.4). Unknown-usage
// convergence pays NO donor reward (frozen §5.3): the usage was never validly
// observed, so the reserve is consumed without reward.
func (s *Store) convergeCharityReservationsForUserDeleteTx(ctx context.Context, tx *sql.Tx, userID, now int64) error {
	rows, err := tx.QueryContext(ctx, `
SELECT id FROM charity_reservations WHERE user_id=? AND state IN ('reserved','dispatched')
ORDER BY id`, userID)
	if err != nil {
		return fmt.Errorf("converge charity reservations: list: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("converge charity reservations: scan: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("converge charity reservations: iterate: %w", err)
	}
	for _, id := range ids {
		row := tx.QueryRowContext(ctx, charityReservationSelectSQL+` WHERE id=?`, id)
		current, serr := scanCharityReservationRow(row)
		if serr != nil {
			return fmt.Errorf("converge charity reservations: read: %w", serr)
		}
		switch current.State {
		case "reserved":
			if _, rerr := s.releaseCharityReservationTx(ctx, tx, id, now); rerr != nil &&
				!errors.Is(rerr, credits.ErrIllegalTransition) {
				return fmt.Errorf("converge charity reservations: release %d: %w", id, rerr)
			}
		case "dispatched":
			plan := CommitPlan{
				OriginalCharge: current.KeyReserved,
				UserCharge:     current.UserReserved,
				UsageUnknown:   true,
			}
			if _, cerr := s.commitCharityReservationTx(ctx, tx, id, plan, now); cerr != nil &&
				!errors.Is(cerr, credits.ErrIllegalTransition) {
				return fmt.Errorf("converge charity reservations: commit %d: %w", id, cerr)
			}
		}
	}
	return nil
}

// CharityReservationExportRow is the consumer's own consumption summary for
// the self-service export (frozen §7). It deliberately carries NO donation
// key id and NO base URL: those identify donated resources the consumer must
// not learn from an export package.
type CharityReservationExportRow struct {
	ID                    int64  `json:"id"`
	Model                 string `json:"model"`
	State                 string `json:"state"`
	PricingMode           string `json:"pricing_mode"`
	DiscountPercent       int    `json:"discount_percent"`
	UserReservedMilli     string `json:"user_reserved_milli"`
	KeyReservedMilli      string `json:"key_reserved_milli"`
	OriginalChargeMilli   string `json:"original_charge_milli"`
	UserChargeMilli       string `json:"user_charge_milli"`
	UncachedInputTokens   int64  `json:"uncached_input_tokens"`
	CacheWriteInputTokens int64  `json:"cache_write_input_tokens"`
	CacheReadInputTokens  int64  `json:"cache_read_input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	UsageUnknown          bool   `json:"usage_unknown"`
	CreatedAt             int64  `json:"created_at"`
	FinalizedAt           *int64 `json:"finalized_at"`
}

// ListExportCharityReservations returns up to limit of the caller's own
// charity reservations (consumer summary view) in id order, fail closed on
// bound violations.
func (s *Store) ListExportCharityReservations(ctx context.Context, userID int64, limit int) ([]CharityReservationExportRow, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, model_snapshot, state, pricing_mode, discount_percent,
       user_reserved, key_reserved, original_charge, user_charge,
       usage_uncached_input_tokens, usage_cache_write_input_tokens,
       usage_cache_read_input_tokens, usage_output_tokens, usage_unknown,
       created_at, finalized_at
FROM charity_reservations WHERE user_id=? ORDER BY id LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export charity reservations: %w", err)
	}
	defer rows.Close()
	out := make([]CharityReservationExportRow, 0, min(limit, 32))
	for rows.Next() {
		var (
			r                          CharityReservationExportRow
			userReserved, keyReserved  int64
			originalCharge, userCharge int64
			finalizedAt                sql.NullInt64
			usageUnknown               int
		)
		if err := rows.Scan(&r.ID, &r.Model, &r.State, &r.PricingMode, &r.DiscountPercent,
			&userReserved, &keyReserved, &originalCharge, &userCharge,
			&r.UncachedInputTokens, &r.CacheWriteInputTokens,
			&r.CacheReadInputTokens, &r.OutputTokens, &usageUnknown,
			&r.CreatedAt, &finalizedAt); err != nil {
			return nil, fmt.Errorf("export charity reservations scan: %w", err)
		}
		r.UserReservedMilli = credits.FormatAmount(userReserved)
		r.KeyReservedMilli = credits.FormatAmount(keyReserved)
		r.OriginalChargeMilli = credits.FormatAmount(originalCharge)
		r.UserChargeMilli = credits.FormatAmount(userCharge)
		r.UsageUnknown = usageUnknown == 1
		if finalizedAt.Valid {
			v := finalizedAt.Int64
			r.FinalizedAt = &v
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export charity reservations iterate: %w", err)
	}
	if len(out) > limit {
		return nil, ErrExportLimit
	}
	return out, nil
}

// DonorRewardExportRow is the donor-visible reward summary of one consumed
// charity call against a donated key (frozen §7). Only the model snapshot and
// the reward amount are projected — never consumer identity or request data.
type DonorRewardExportRow struct {
	ID               int64  `json:"id"`
	Model            string `json:"model"`
	DonorRewardMilli string `json:"donor_reward_milli"`
	CreatedAt        int64  `json:"created_at"`
	FinalizedAt      *int64 `json:"finalized_at"`
}

// ListExportDonorRewards returns up to limit reward summaries of the caller's
// donations in id order, fail closed on bound violations.
func (s *Store) ListExportDonorRewards(ctx context.Context, userID int64, limit int) ([]DonorRewardExportRow, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, model_snapshot, donor_reward, created_at, finalized_at
FROM charity_reservations WHERE donor_user_id=? ORDER BY id LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export donor rewards: %w", err)
	}
	defer rows.Close()
	out := make([]DonorRewardExportRow, 0, min(limit, 32))
	for rows.Next() {
		var (
			r           DonorRewardExportRow
			reward      int64
			finalizedAt sql.NullInt64
		)
		if err := rows.Scan(&r.ID, &r.Model, &reward, &r.CreatedAt, &finalizedAt); err != nil {
			return nil, fmt.Errorf("export donor rewards scan: %w", err)
		}
		r.DonorRewardMilli = credits.FormatAmount(reward)
		if finalizedAt.Valid {
			v := finalizedAt.Int64
			r.FinalizedAt = &v
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export donor rewards iterate: %w", err)
	}
	if len(out) > limit {
		return nil, ErrExportLimit
	}
	return out, nil
}
