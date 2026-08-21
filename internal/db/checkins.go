package db

// Check-in repository: one atomic user check-in per site-local day, its random
// milli-credit award, and the read-only status projection (frozen §H/§I,
// implementation contract §2.4/§4).
//
// Frozen semantics:
//
//   - the site-local day key comes from the explicitly configured fixed
//     offset; while the offset is unset the feature behaves exactly like a
//     disabled feature (ErrCheckinDisabled) and no day key is ever generated
//     under an implicit default;
//   - one check-in per user per day is enforced by UNIQUE(user_id, day): the
//     write path never pre-checks "already checked in today". A constraint
//     violation rolls the whole transaction back — the day is not consumed,
//     no award is granted, no ledger row and no activity contribution exist;
//   - the effective mode is a three-way switch: enabled / level_gated (only
//     effective level >= 3 may check in) / disabled (the default). The
//     timezone-unset, disabled, level-gated-denied and corrupt-configuration
//     refusals are all the SAME sentinel (ErrCheckinDisabled): the repository
//     never distinguishes them for the user;
//   - credits_cap_milli is an admission threshold, not a truncation: cap=0
//     means no threshold; otherwise a user whose credits balance has reached
//     the cap is refused (ErrCheckinCapReached) WITHOUT consuming the day,
//     unless the effective level is >= LevelCheckinBypass (3). An admitted
//     award may push the balance above the cap (never truncated);
//   - the award is a uniform random integer in the inclusive [min, max]
//     milli-credit range, drawn with crypto/rand rejection sampling (no
//     modulo bias); a corrupt range (min > max or a negative bound) fails
//     closed as ErrCheckinDisabled instead of fabricating a value;
//   - the check-in row, the balance update, its checkin_award ledger row and
//     the product-activity contribution commit in ONE transaction; the first
//     temporal row ever also freezes the site timezone offset in that same
//     transaction (freezeTimezoneTx);
//   - checkins live for the account lifecycle: export projects the site-local
//     date and award, account deletion cascades, and no time-based retention
//     sweep ever removes them.

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

// Check-in repository sentinels. The API boundary maps them to the stable
// envelope; none of them carries request or secret material.
var (
	// ErrCheckinDisabled reports that check-in is not available. Every cause —
	// timezone unset, mode disabled, a level-gated denial, or a corrupt
	// configuration — yields this ONE sentinel so the user cannot distinguish
	// them (the stable feature_disabled error and message are identical).
	ErrCheckinDisabled = errors.New("db: check-in is disabled")
	// ErrAlreadyCheckedIn reports that the user already checked in on this
	// site-local day. The day was consumed by the earlier successful check-in,
	// not by this attempt: nothing was written.
	ErrAlreadyCheckedIn = errors.New("db: already checked in today")
	// ErrCheckinCapReached reports that the user's credits balance has reached
	// the configured check-in threshold (credits >= cap) at an effective level
	// below the bypass. The day is NOT consumed by this refusal.
	ErrCheckinCapReached = errors.New("db: check-in credits cap reached")
)

// Check-in site_config keys (implementation contract §4.1).
const (
	CheckinModeKey          = "checkin_mode"
	CheckinAwardMinMilliKey = "checkin_award_min_milli"
	CheckinAwardMaxMilliKey = "checkin_award_max_milli"
	CreditsCapMilliKey      = "credits_cap_milli"
)

// Effective check-in modes (frozen §I). An unknown stored value is treated as
// disabled: fail closed, never an implicit enabled.
const (
	CheckinModeEnabled    = "enabled"
	CheckinModeLevelGated = "level_gated"
	CheckinModeDisabled   = "disabled"
)

// Documented defaults (frozen §H): the award range is 4–6 USD equivalent in
// milli-credits and the low-level check-in threshold is 25 USD equivalent.
// Unset keys read as these defaults; cap 0 would mean "no threshold".
const (
	DefaultCheckinAwardMinMilli int64 = 40_000_000
	DefaultCheckinAwardMaxMilli int64 = 60_000_000
	DefaultCreditsCapMilli      int64 = 250_000_000
)

// checkinConfig is the authoritative in-transaction snapshot of the four
// check-in site_config keys.
type checkinConfig struct {
	mode       string
	awardMin   int64
	awardMax   int64
	creditsCap int64
}

// readCheckinConfigTx reads the four keys inside the caller's transaction.
//
//   - an unset key reads as its documented default;
//   - a stored non-canonical or negative award bound is CORRUPT: the whole
//     read fails closed with ErrCheckinDisabled and the caller never
//     fabricates a draw. A corrupt cap falls back to the documented default
//     exactly like the administrator GET projection (the write path only
//     stores canonical values, so this only absorbs manual tampering and can
//     never fabricate an unlimited cap);
//   - a stored pair with min > max — only manual corruption could produce it,
//     the write path cross-validates the pair in one transaction — fails
//     closed the same way;
//   - an unknown mode is disabled (fail closed).
func readCheckinConfigTx(ctx context.Context, tx *sql.Tx) (checkinConfig, error) {
	var cfg checkinConfig
	modeRaw, err := readSiteConfigRowTx(ctx, tx, CheckinModeKey)
	if err != nil {
		return cfg, err
	}
	cfg.mode = CheckinModeDisabled
	switch modeRaw {
	case CheckinModeEnabled, CheckinModeLevelGated, CheckinModeDisabled:
		cfg.mode = modeRaw
	}

	minRaw, err := readSiteConfigRowTx(ctx, tx, CheckinAwardMinMilliKey)
	if err != nil {
		return cfg, err
	}
	maxRaw, err := readSiteConfigRowTx(ctx, tx, CheckinAwardMaxMilliKey)
	if err != nil {
		return cfg, err
	}
	minValue, minErr := parseCheckinAmount(minRaw, DefaultCheckinAwardMinMilli)
	maxValue, maxErr := parseCheckinAmount(maxRaw, DefaultCheckinAwardMaxMilli)
	if minErr != nil || maxErr != nil || minValue > maxValue {
		return cfg, ErrCheckinDisabled
	}
	cfg.awardMin = minValue
	cfg.awardMax = maxValue

	capRaw, err := readSiteConfigRowTx(ctx, tx, CreditsCapMilliKey)
	if err != nil {
		return cfg, err
	}
	capValue, capErr := parseCheckinAmount(capRaw, DefaultCreditsCapMilli)
	if capErr != nil {
		capValue = DefaultCreditsCapMilli
	}
	cfg.creditsCap = capValue
	return cfg, nil
}

// parseCheckinAmount parses one stored amount key. A missing row (empty raw)
// yields the documented default; a present-but-invalid value (non-canonical
// decimal or negative) is corrupt and errors.
func parseCheckinAmount(raw string, def int64) (int64, error) {
	if raw == "" {
		return def, nil
	}
	v, err := credits.ParseAmount(raw)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("checkin: stored configuration is corrupt")
	}
	return v, nil
}

// readSiteConfigRowTx reads one site_config value inside the caller's
// transaction; a missing row is the empty string.
func readSiteConfigRowTx(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var raw sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("checkin: read configuration: %w", err)
	}
	if !raw.Valid {
		return "", nil
	}
	return raw.String, nil
}

// CheckinResult reports the committed outcome of one successful check-in.
type CheckinResult struct {
	// Day is the site-local day key the check-in landed on.
	Day int64
	// AwardMilli is the granted award in milli-credits (>= 0).
	AwardMilli int64
	// CreditsAfter is the authoritative post-award consumption balance.
	CreditsAfter int64
}

// Checkin performs one user's daily check-in as a single transaction:
// day-key resolution, configuration snapshot, mode gate, effective-level
// resolution (same transaction, lazy promotion included), cap gate, random
// award, the UNIQUE-constrained check-in insert, the checkin_award ledger
// application, the product-activity contribution and — for the first temporal
// row ever — the durable timezone freeze. now is the caller-supplied instant
// that defines the site-local day.
//
// Refusals (ErrCheckinDisabled / ErrAlreadyCheckedIn / ErrCheckinCapReached)
// write nothing; in particular a cap or mode refusal never consumes the day.
// The administrator row is excluded (ErrLevelAdminExcluded propagates) and a
// missing user is ErrNotFound.
func (s *Store) Checkin(ctx context.Context, userID int64, now time.Time) (*CheckinResult, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if now.IsZero() {
		return nil, fmt.Errorf("checkin: time is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("checkin: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// 1. Site-local day key. An unset offset makes the feature indistinguish-
	//    able from disabled; no key is generated under an implicit default.
	day, err := siteDayKeyAtTx(tx, now.Unix())
	if errors.Is(err, ErrTimezoneUnavailable) {
		return nil, ErrCheckinDisabled
	}
	if err != nil {
		return nil, fmt.Errorf("checkin: resolve day key: %w", err)
	}

	// 2. Configuration snapshot in the same transaction.
	cfg, err := readCheckinConfigTx(ctx, tx)
	if errors.Is(err, ErrCheckinDisabled) {
		return nil, ErrCheckinDisabled // corrupt award range
	}
	if err != nil {
		return nil, err
	}

	// 3. Authoritative effective level inside this transaction. The
	//    administrator row is excluded by the resolver itself; its sentinel
	//    propagates untouched.
	level, err := s.resolveEffectiveLevelTx(ctx, tx, userID)
	if err != nil {
		return nil, err
	}

	// 4. Mode gate. level_gated is a denial below the bypass level, with the
	//    SAME user-facing sentinel as every other disabled cause.
	if cfg.mode == CheckinModeLevelGated && !EffectiveLevelAtLeast(level, LevelCheckinBypass) {
		return nil, ErrCheckinDisabled
	}
	if cfg.mode != CheckinModeEnabled && cfg.mode != CheckinModeLevelGated {
		return nil, ErrCheckinDisabled // disabled or unknown stored mode
	}

	// 5. Cap gate: an admission threshold, never a truncation. The balance is
	//    read in this transaction; a refusal writes nothing and leaves the
	//    day unconsumed. Level >= 3 bypasses the threshold entirely.
	balances, err := readUserBalancesForUpdate(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if cfg.creditsCap > 0 && balances.credits >= cfg.creditsCap &&
		!EffectiveLevelAtLeast(level, LevelCheckinBypass) {
		return nil, ErrCheckinCapReached
	}

	// 6. Random award in the inclusive [min, max] range (crypto/rand,
	//    rejection sampling, no modulo bias).
	award, err := randomAwardMilli(cfg.awardMin, cfg.awardMax)
	if err != nil {
		return nil, err
	}

	// 7. The UNIQUE-constrained insert IS the one-per-day rule. The operation
	//    id derives from the unforgeable (user, day) identity: "sys.checkin."
	//    plus two int64 decimal forms is at most 12+19+1+19 = 52 bytes, well
	//    inside MaxOperationIDLen, and its characters are all in the
	//    operation-id alphabet.
	operationID := "sys.checkin." + strconv.FormatInt(userID, 10) + "." + strconv.FormatInt(day, 10)
	if err := validateSystemOperationID(operationID); err != nil {
		return nil, err // unreachable by construction; kept fail-closed
	}
	firstCheckinRow, err := timezoneTableEmptyTx(ctx, tx, "checkins")
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO checkins (user_id, day, award, operation_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, day, award, operationID, now.Unix())
	if err != nil {
		if isConstraintError(err) {
			// Diagnostic inside the still-open transaction (the write lock is
			// held, so the read is consistent): a surviving row for this
			// (user, day) is the ordinary same-day race. The operation_id
			// unique index derives from the same identity, so its collision
			// resolves to the same row and the same user-facing outcome.
			if derr := classifyConflict(ctx, tx,
				`SELECT COUNT(*) FROM checkins WHERE user_id=? AND day=?`, userID, day); derr == ErrConflict {
				return nil, ErrAlreadyCheckedIn
			}
		}
		return nil, fmt.Errorf("checkin: insert: %w", err)
	}

	// 8. Apply the award through the shared ledger primitive. The operation_id
	//    replay check inside makes a hypothetical duplicate application a
	//    no-op returning the first result (the check-in insert above normally
	//    refuses duplicates first, so this is the idempotency backstop).
	ledgerRes, err := s.applyCreditOperationTx(ctx, tx, CreditOperation{
		Kind:         LedgerCheckinAward,
		UserID:       userID,
		OperationID:  operationID,
		CreditsDelta: award,
		CreatedAt:    now,
	})
	if err != nil {
		return nil, err
	}

	// 9. Product-activity contribution: a successful check-in is product-
	//    active. The offset is guaranteed set (step 1), so the helper cannot
	//    hit its disabled path here.
	if _, err := recordActivityTx(ctx, tx, userID, day, ActivityDelta{Checkins: 1}, now.Unix()); err != nil {
		return nil, fmt.Errorf("checkin: record activity: %w", err)
	}

	// 10. The first temporal row ever durably freezes the site timezone
	//     offset in this same transaction (recordActivityTx above performs the
	//     same freeze for the activity table; this covers the checkins table
	//     itself and is idempotent).
	if firstCheckinRow {
		if err := freezeTimezoneTx(ctx, tx, now.Unix()); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("checkin: commit: %w", err)
	}
	committed = true
	return &CheckinResult{
		Day:          day,
		AwardMilli:   award,
		CreditsAfter: ledgerRes.CreditsAfter,
	}, nil
}

// CheckinStatus is the read-only projection for the status endpoint: the
// effective availability, whether the user already checked in on the current
// site-local day, the raw configured award range and cap (no randomness), and
// the current consumption balance.
type CheckinStatus struct {
	Enabled         bool
	CheckedInToday  bool
	AwardMinMilli   int64
	AwardMaxMilli   int64
	CreditsCapMilli int64
	Credits         int64
}

// CheckinStatus resolves the status projection WITHOUT any write: it never
// triggers the lazy level promotion (the level columns are read directly and
// the effective value is computed without persisting, mirroring the
// administrator-list rule) and never inserts anything. Availability requires
// a configured timezone, a known non-disabled mode and a valid award range —
// identical to the write path's admission, so the status never advertises a
// check-in that would be refused, and never reveals WHY a feature is disabled.
func (s *Store) CheckinStatus(ctx context.Context, userID int64, now time.Time) (*CheckinStatus, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if now.IsZero() {
		return nil, fmt.Errorf("checkin status: time is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("checkin status: begin: %w", err)
	}
	defer tx.Rollback() // read-only: nothing to commit

	day, err := siteDayKeyAtTx(tx, now.Unix())
	if errors.Is(err, ErrTimezoneUnavailable) {
		return &CheckinStatus{Enabled: false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checkin status: resolve day key: %w", err)
	}
	cfg, err := readCheckinConfigTx(ctx, tx)
	if errors.Is(err, ErrCheckinDisabled) {
		return &CheckinStatus{Enabled: false}, nil // corrupt award range
	}
	if err != nil {
		return nil, err
	}
	if cfg.mode != CheckinModeEnabled && cfg.mode != CheckinModeLevelGated {
		return &CheckinStatus{Enabled: false}, nil // disabled or unknown mode
	}
	if cfg.mode == CheckinModeLevelGated {
		level, err := peekEffectiveLevelTx(ctx, tx, userID)
		if err != nil {
			return nil, err
		}
		if !EffectiveLevelAtLeast(level, LevelCheckinBypass) {
			return &CheckinStatus{Enabled: false}, nil
		}
	}

	status := &CheckinStatus{
		Enabled:         true,
		AwardMinMilli:   cfg.awardMin,
		AwardMaxMilli:   cfg.awardMax,
		CreditsCapMilli: cfg.creditsCap,
	}
	var checkedIn int
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM checkins WHERE user_id=? AND day=?)`, userID, day).Scan(&checkedIn); err != nil {
		return nil, fmt.Errorf("checkin status: read today's row: %w", err)
	}
	status.CheckedInToday = checkedIn == 1
	balances, err := readUserBalancesForUpdate(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	status.Credits = balances.credits
	return status, nil
}

// peekEffectiveLevelTx computes the effective level WITHOUT the lazy
// auto-level promotion write: the manual override wins, otherwise the result
// is the higher of the persisted high-water mark and the level the current
// thresholds qualify for (exactly what the authoritative resolver would
// return, minus the persistence). Corrupt stored levels are refused fail-closed
// like in the resolver; the administrator row is ErrLevelAdminExcluded.
func peekEffectiveLevelTx(ctx context.Context, tx *sql.Tx, userID int64) (int, error) {
	var isAdmin int
	var level sql.NullInt64
	var autoLevel int
	var donationCredit int64
	err := tx.QueryRowContext(ctx,
		`SELECT is_admin, level, auto_level, donation_credit FROM users WHERE id=?`, userID,
	).Scan(&isAdmin, &level, &autoLevel, &donationCredit)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("checkin status: read level: %w", err)
	}
	if isAdmin != 0 {
		return 0, ErrLevelAdminExcluded
	}
	if level.Valid {
		value := int(level.Int64)
		if value < MinLevel || value > MaxLevel {
			return 0, fmt.Errorf("checkin status: stored level %d is out of range", value)
		}
		return value, nil
	}
	if autoLevel < MinAutoLevel || autoLevel > MaxAutoLevel {
		return 0, fmt.Errorf("checkin status: stored auto level %d is out of range", autoLevel)
	}
	rows, err := readLevelThresholdRows(ctx, tx)
	if err != nil {
		return 0, err
	}
	target := targetAutoLevel(donationCredit, LevelThresholds{Level2: rows[0], Level3: rows[1], Level4: rows[2]})
	if target > autoLevel {
		return target, nil
	}
	return autoLevel, nil
}

// randomAwardMilli draws one uniform integer in the inclusive [minMilli,
// maxMilli] range. It derives the minimum sufficient bit width from the span,
// draws full bytes from crypto/rand and rejects out-of-range draws, so every
// value in the range is exactly equally likely (no modulo bias). min == max
// returns the constant without consuming entropy. A negative bound or an
// inverted range is a caller bug / corrupt configuration and errors out;
// nothing is ever fabricated.
func randomAwardMilli(minMilli, maxMilli int64) (int64, error) {
	if minMilli < 0 || minMilli > maxMilli {
		return 0, fmt.Errorf("checkin: award range [%d, %d] is invalid", minMilli, maxMilli)
	}
	if minMilli == maxMilli {
		return minMilli, nil
	}
	span := uint64(maxMilli - minMilli) // both bounds non-negative, so no overflow
	width := (bits.Len64(span) + 7) / 8
	var mask uint64
	if bits.Len64(span) < 64 {
		mask = (uint64(1) << bits.Len64(span)) - 1
	} else {
		mask = ^uint64(0)
	}
	buf := make([]byte, width)
	for {
		if _, err := crand.Read(buf); err != nil {
			return 0, fmt.Errorf("checkin: draw award: %w", err)
		}
		draw := uint64(0)
		for _, b := range buf {
			draw = draw<<8 | uint64(b)
		}
		draw &= mask
		if draw <= span {
			return minMilli + int64(draw), nil // draw <= span keeps the sum <= maxMilli
		}
		// Out of range: reject the whole draw and try again (rejection
		// sampling; expected iterations < 2 for any span).
	}
}

// SetCheckinAwardBoundMilli atomically stores one bound of the check-in award
// range after cross-validating the resulting pair (min <= max) against the
// other key's current value inside the SAME transaction — never a separate
// read-then-write. An unknown key or a negative value is ErrConflict, and a
// rejected combination writes nothing. Like every administrator console
// write, the change records the actor's console-write activity in that same
// transaction (skipped while the site timezone is unset, exactly like the
// generic site-config path and the level-threshold pair).
func (s *Store) SetCheckinAwardBoundMilli(ctx context.Context, key string, value int64, actorUserID int64, at time.Time) error {
	if value < 0 {
		return ErrConflict
	}
	if actorUserID <= 0 || at.IsZero() {
		return fmt.Errorf("set checkin award bound: actor and time are required")
	}
	var minKey, maxKey bool
	switch key {
	case CheckinAwardMinMilliKey:
		minKey = true
	case CheckinAwardMaxMilliKey:
		maxKey = true
	default:
		return ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set checkin award bound: %w", err)
	}
	defer tx.Rollback()

	minRaw, err := readSiteConfigRowTx(ctx, tx, CheckinAwardMinMilliKey)
	if err != nil {
		return err
	}
	maxRaw, err := readSiteConfigRowTx(ctx, tx, CheckinAwardMaxMilliKey)
	if err != nil {
		return err
	}
	// Unset (or, mirroring the read projection, manually corrupted) bounds
	// cross-validate against their documented defaults; the write path itself
	// only ever stores canonical values.
	minValue, minErr := parseCheckinAmount(minRaw, DefaultCheckinAwardMinMilli)
	if minErr != nil {
		minValue = DefaultCheckinAwardMinMilli
	}
	maxValue, maxErr := parseCheckinAmount(maxRaw, DefaultCheckinAwardMaxMilli)
	if maxErr != nil {
		maxValue = DefaultCheckinAwardMaxMilli
	}
	if minKey {
		minValue = value
	}
	if maxKey {
		maxValue = value
	}
	if minValue > maxValue {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO site_config (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, credits.FormatAmount(value), at.Unix()); err != nil {
		return fmt.Errorf("set checkin award bound: %w", err)
	}
	dayKey, err := siteDayKeyAtTx(tx, at.Unix())
	if errors.Is(err, ErrTimezoneUnavailable) {
		// Activity disabled: keep the configuration write, skip the rollup.
	} else if err != nil {
		return fmt.Errorf("set checkin award bound: resolve day key: %w", err)
	} else if _, err := recordActivityTx(ctx, tx, actorUserID, dayKey, ActivityDelta{ConsoleWrites: 1}, at.Unix()); err != nil {
		return fmt.Errorf("set checkin award bound: record activity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set checkin award bound: %w", err)
	}
	return nil
}

// CheckinExportRow is one check-in of the self-service export package: the
// site-local calendar date and the granted award only — no streak and no
// location data exists anywhere in the table (frozen §7 privacy).
type CheckinExportRow struct {
	// Day is the site-local calendar date rendered as YYYY-MM-DD under the
	// frozen site offset (rows can only exist while the offset is set).
	Day string `json:"day"`
	// AwardMilli is the granted award as a canonical decimal string.
	AwardMilli string `json:"award_milli"`
}

// ListExportCheckins returns up to limit check-in rows owned by userID in day
// order for the self-service export (bounded, fail closed). The day key is
// rendered as the site-local calendar date. A user with no check-ins exports
// an empty list regardless of the timezone state; surviving rows without a
// configured offset are a corrupted state and fail closed instead of
// fabricating dates.
func (s *Store) ListExportCheckins(ctx context.Context, userID int64, limit int) ([]CheckinExportRow, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > ExportCollectionLimit {
		return nil, ErrExportLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT day, award FROM checkins WHERE user_id=? ORDER BY day LIMIT ?`, userID, limit+1)
	if err != nil {
		return nil, fmt.Errorf("export checkins: %w", err)
	}
	defer rows.Close()

	type rawCheckin struct {
		day   int64
		award int64
	}
	raw := make([]rawCheckin, 0, min(limit, 64))
	for rows.Next() {
		var r rawCheckin
		if err := rows.Scan(&r.day, &r.award); err != nil {
			return nil, fmt.Errorf("export checkins: %w", err)
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export checkins: %w", err)
	}
	if len(raw) > limit {
		return nil, ErrExportLimit
	}
	if len(raw) == 0 {
		return []CheckinExportRow{}, nil
	}
	// Rows can only have been written under a configured, since-frozen
	// offset; a missing offset here is a corrupted database.
	offset, err := s.SiteTimezoneOffsetMinutes()
	if err != nil {
		return nil, fmt.Errorf("export checkins: %w", err)
	}
	out := make([]CheckinExportRow, 0, len(raw))
	for _, r := range raw {
		// The day key is the unix seconds of the site-local midnight; adding
		// the offset back yields the instant whose UTC wall clock equals the
		// local one, so the UTC date components ARE the local date.
		local := time.Unix(r.day+int64(offset)*60, 0).UTC()
		out = append(out, CheckinExportRow{
			Day:        local.Format("2006-01-02"),
			AwardMilli: credits.FormatAmount(r.award),
		})
	}
	return out, nil
}
