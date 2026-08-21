package db

// Level system: the manual override column (users.level), the persistent
// automatic-level high-water mark (users.auto_level), the donation_credit
// threshold site_config keys, and the single authoritative effective-level
// resolver (frozen §G, accepted clarifications 6–8, implementation contract
// §2.1/§4).
//
// Frozen semantics:
//
//   - the administrator row is EXCLUDED from the level system: it has no
//     level, manual or automatic, and the resolver refuses it with a sentinel
//     error instead of mapping it onto any level;
//   - a non-NULL manual level (1..5) wins over everything; level 5 is
//     reachable only through a manual override;
//   - the automatic level is the persisted high-water mark (1..4): a lazy
//     conditional UPDATE raises it when donation_credit reaches an enabled
//     threshold, and there is NO downgrade path — raising a threshold later or
//     a negative donation_credit correction never lowers it;
//   - a threshold of 0 disables that level's automatic promotion; the enabled
//     subset must be strictly increasing with the level order (a disabled
//     middle level is allowed and produces a "skip");
//   - the threshold cross-validation and the write share ONE transaction, so a
//     rejected combination can never leave a half-written chain.
//
// Effective level is resolved ONLY through ResolveEffectiveLevel; capability
// checks use the helpers below against that result. A client-side level or
// capability value is never authoritative.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

// Capability levels served to later rails. The named constants are the only
// sanctioned way to express a capability threshold; every check must run
// server-side against the authoritative resolver (front-end gating is display
// polish, never authorization).
const (
	// LevelBanner is the minimum effective level for the user-station banner.
	LevelBanner = 2
	// LevelCheckinBypass is the minimum effective level for check-in to ignore
	// the credits cap (wired by the check-in rail).
	LevelCheckinBypass = 3
	// LevelSteward is the minimum effective level for the /api/steward/
	// co-management prefix. In alpha.2 it is reachable only via a manual
	// override (auto levels stop at 4).
	LevelSteward = 5
)

// Level bounds. MinLevel/MaxLevel bound the manual override; the automatic
// high-water mark stops one below the manual-only top level.
const (
	MinLevel     = 1
	MaxLevel     = 5
	MinAutoLevel = 1
	MaxAutoLevel = 4
)

// ErrLevelAdminExcluded reports that the target identity is the administrator
// row, which is excluded from the level system. Callers must treat it as a
// refusal, never as any specific level.
var ErrLevelAdminExcluded = errors.New("db: administrator is excluded from levels")

// Level threshold site_config keys (implementation contract §4.1). Values are
// canonical non-negative decimal milli-credit strings; "0" (or an unset key)
// disables that level's automatic promotion.
const (
	LevelThreshold2Key = "level_threshold_2_milli"
	LevelThreshold3Key = "level_threshold_3_milli"
	LevelThreshold4Key = "level_threshold_4_milli"
)

// levelThresholdKeys in level order.
var levelThresholdKeys = [3]string{LevelThreshold2Key, LevelThreshold3Key, LevelThreshold4Key}

// LevelThresholds is the authoritative snapshot of the three promotion
// thresholds in milli-credits. A zero (or negative after corruption) entry
// means that level's automatic promotion is disabled.
type LevelThresholds struct {
	Level2 int64
	Level3 int64
	Level4 int64
}

// EffectiveLevelAtLeast reports whether an authoritative effective level
// satisfies a capability minimum. This is the server-side capability helper;
// the front end may render hints from projected levels but its judgment is
// never authoritative.
func EffectiveLevelAtLeast(effective, minimum int) bool {
	return effective >= minimum
}

// LevelThresholdsSnapshot reads the three promotion thresholds as one
// authoritative snapshot. An unset or non-canonical stored value reads as 0
// (disabled): the write path validates strictly, so this fallback only absorbs
// a manually corrupted row and can never fabricate a promotion.
func (s *Store) LevelThresholdsSnapshot() (LevelThresholds, error) {
	var out LevelThresholds
	values, err := readLevelThresholdRows(context.Background(), s.db)
	if err != nil {
		return out, err
	}
	out.Level2, out.Level3, out.Level4 = values[0], values[1], values[2]
	return out, nil
}

// rowQuerier is satisfied by both *sql.DB and *sql.Tx, so the threshold read
// can share one transaction with the cross-validation write.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// readLevelThresholdRows reads the three thresholds by key in level order. A
// missing key is an empty string and reads as 0.
func readLevelThresholdRows(ctx context.Context, q rowQuerier) ([3]int64, error) {
	var out [3]int64
	for i, key := range levelThresholdKeys {
		var raw sql.NullString
		err := q.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue // unset key: 0 (disabled)
		}
		if err != nil {
			return out, fmt.Errorf("read level thresholds: %w", err)
		}
		if !raw.Valid {
			continue
		}
		value, perr := credits.ParseAmount(raw.String)
		if perr != nil || value < 0 {
			continue // unset-or-corrupt reads as disabled, never as a promotion
		}
		out[i] = value
	}
	return out, nil
}

// thresholdChainIncreasing reports whether the enabled (>0) thresholds are
// strictly increasing in level order. A disabled middle level is allowed: the
// enabled subset just has to keep increasing (a "skip").
func thresholdChainIncreasing(t LevelThresholds) bool {
	previous := int64(0)
	for _, value := range [3]int64{t.Level2, t.Level3, t.Level4} {
		if value <= 0 {
			continue
		}
		if value <= previous {
			return false
		}
		previous = value
	}
	return true
}

// targetAutoLevel computes the automatic level a donation_credit balance
// qualifies for under the given thresholds: the HIGHEST enabled level whose
// threshold is met, or 1 when none is. Disabled (0) thresholds never
// participate. Pure arithmetic; no storage access.
func targetAutoLevel(donationCredit int64, t LevelThresholds) int {
	target := MinAutoLevel
	enabled := [3]int64{t.Level2, t.Level3, t.Level4}
	for i, threshold := range enabled {
		if threshold <= 0 {
			continue
		}
		if donationCredit >= threshold {
			target = i + 2 // keys are ordered 2,3,4
		}
	}
	return target
}

// ResolveEffectiveLevel is the single authoritative effective-level resolver.
// It re-reads the user row and the thresholds and returns the effective level:
//
//   - a non-NULL manual level (1..5) wins and is returned as-is;
//   - otherwise the automatic high-water mark applies: when the thresholds now
//     qualify the user for a higher level than the persisted mark, a single
//     conditional UPDATE promotes it inside this call (lazy, idempotent,
//     concurrency-safe: the WHERE auto_level<? predicate means concurrent
//     resolutions converge without losing or duplicating a promotion);
//   - there is no downgrade path: a later threshold raise or a negative
//     donation_credit correction leaves the persisted mark untouched.
//
// The administrator row yields ErrLevelAdminExcluded; a missing user is
// ErrNotFound. Read paths MAY trigger the lazy promotion persistence (that is
// the frozen semantics); nothing else may write auto_level.
func (s *Store) ResolveEffectiveLevel(ctx context.Context, userID int64) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("resolve effective level: store is required")
	}
	if userID <= 0 {
		return 0, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("resolve effective level: %w", err)
	}
	defer tx.Rollback()

	var isAdmin int
	var level sql.NullInt64
	var autoLevel int
	var donationCredit int64
	err = tx.QueryRowContext(ctx,
		`SELECT is_admin, level, auto_level, donation_credit FROM users WHERE id=?`, userID,
	).Scan(&isAdmin, &level, &autoLevel, &donationCredit)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("resolve effective level: %w", err)
	}
	if isAdmin != 0 {
		return 0, ErrLevelAdminExcluded
	}
	if level.Valid {
		// Manual override wins. The stored value is bounded to 1..5 by every
		// write path; a corrupt out-of-range row is refused rather than
		// clamped (fail closed).
		value := int(level.Int64)
		if value < MinLevel || value > MaxLevel {
			return 0, fmt.Errorf("resolve effective level: stored level %d is out of range", value)
		}
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("resolve effective level: %w", err)
		}
		return value, nil
	}
	if autoLevel < MinAutoLevel || autoLevel > MaxAutoLevel {
		return 0, fmt.Errorf("resolve effective level: stored auto level %d is out of range", autoLevel)
	}

	rows, err := readLevelThresholdRows(ctx, tx)
	if err != nil {
		return 0, err
	}
	thresholds := LevelThresholds{Level2: rows[0], Level3: rows[1], Level4: rows[2]}
	target := targetAutoLevel(donationCredit, thresholds)
	if target > autoLevel {
		result, err := tx.ExecContext(ctx,
			`UPDATE users SET auto_level=?, updated_at=? WHERE id=? AND is_admin=0 AND auto_level<?`,
			target, time.Now().Unix(), userID, target)
		if err != nil {
			return 0, fmt.Errorf("resolve effective level: promote: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("resolve effective level: promote: %w", err)
		}
		if affected == 1 {
			if err := tx.Commit(); err != nil {
				return 0, fmt.Errorf("resolve effective level: %w", err)
			}
			return target, nil
		}
		// The conditional update missed: a concurrent resolution already
		// promoted this user at least as high. Re-read the committed mark so
		// this call returns the newest value, never a stale snapshot.
		if err := tx.QueryRowContext(ctx, `SELECT auto_level FROM users WHERE id=?`, userID).Scan(&autoLevel); err != nil {
			return 0, fmt.Errorf("resolve effective level: re-read: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("resolve effective level: %w", err)
	}
	return autoLevel, nil
}

// SetUserManualLevel stores or clears the manual level override for one normal
// user and returns the refreshed row. level == nil resets to automatic (the
// persisted auto_level high-water mark becomes effective again); a non-nil
// level must be within [MinLevel, MaxLevel] (level 5 included: it is the only
// path to the steward capability). The manual override never touches the
// auto_level high-water mark itself. The administrator row is ErrAdminProtected;
// a missing user is ErrNotFound; an out-of-range level is ErrConflict.
func (s *Store) SetUserManualLevel(userID int64, level *int) (*User, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if level != nil && (*level < MinLevel || *level > MaxLevel) {
		return nil, ErrConflict
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("set user level: %w", err)
	}
	defer tx.Rollback()
	var isAdmin int
	if err := tx.QueryRow(`SELECT is_admin FROM users WHERE id=?`, userID).Scan(&isAdmin); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("set user level: %w", err)
	} else if isAdmin != 0 {
		return nil, ErrAdminProtected
	}
	var value any
	if level != nil {
		value = *level
	}
	if _, err := tx.Exec(`UPDATE users SET level=?, updated_at=? WHERE id=? AND is_admin=0`,
		value, time.Now().Unix(), userID); err != nil {
		return nil, fmt.Errorf("set user level: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("set user level: %w", err)
	}
	return s.GetUserByID(userID)
}

// SetLevelThresholdMilli atomically stores one promotion threshold after
// cross-validating the resulting three-key combination inside the SAME
// transaction (implementation contract §4.2: the validation and the write
// must not be two separate steps). An unknown key or a negative value is
// ErrConflict; a combination whose enabled chain is not strictly increasing
// is ErrConflict and nothing is written. Like every administrator console
// write, the change is recorded as product activity in that same transaction
// (skipped while the site timezone is unset, exactly like the generic
// site-config path). Threshold updates never trigger any level recomputation:
// promotion is lazy on the next authoritative resolution and demotion does
// not exist.
func (s *Store) SetLevelThresholdMilli(ctx context.Context, key string, value int64, actorUserID int64, at time.Time) error {
	if value < 0 {
		return ErrConflict
	}
	if actorUserID <= 0 || at.IsZero() {
		return fmt.Errorf("set level threshold: actor and time are required")
	}
	index := -1
	for i, candidate := range levelThresholdKeys {
		if key == candidate {
			index = i
			break
		}
	}
	if index < 0 {
		return ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set level threshold: %w", err)
	}
	defer tx.Rollback()

	rows, err := readLevelThresholdRows(ctx, tx)
	if err != nil {
		return err
	}
	rows[index] = value
	thresholds := LevelThresholds{Level2: rows[0], Level3: rows[1], Level4: rows[2]}
	if !thresholdChainIncreasing(thresholds) {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO site_config (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, credits.FormatAmount(value), at.Unix()); err != nil {
		return fmt.Errorf("set level threshold: %w", err)
	}
	dayKey, err := siteDayKeyAtTx(tx, at.Unix())
	if errors.Is(err, ErrTimezoneUnavailable) {
		// Activity disabled: keep the configuration write, skip the rollup.
	} else if err != nil {
		return fmt.Errorf("set level threshold: resolve day key: %w", err)
	} else if _, err := recordActivityTx(ctx, tx, actorUserID, dayKey, ActivityDelta{ConsoleWrites: 1}, at.Unix()); err != nil {
		return fmt.Errorf("set level threshold: record activity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set level threshold: %w", err)
	}
	return nil
}
