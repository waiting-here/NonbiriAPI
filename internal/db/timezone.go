package db

// Authoritative site-timezone resolver. The offset is a site_config value
// that must distinguish "not configured" from an explicit UTC (0): an unset
// offset never falls back to an implicit default, because site day keys
// (local midnights) would silently change meaning. Consumers that need day
// keys fail closed with ErrTimezoneUnavailable when the offset is unset or a
// stored value is corrupt; that sentinel maps to the stable
// feature_disabled error at the API boundary and its reason is never exposed
// to normal users.
//
// Immutability guard: the offset may be set or changed only while no checkin
// or activity row exists. Once any such row has ever been produced, the
// offset is frozen — even if those rows are later removed by retention — so
// historical day keys can never be re-bucketed under a different offset. The
// guard is enforced inside the same write transaction as the upsert (never
// by a separate read-then-write), and a permanent marker row records the
// freeze so retention cannot unfreeze the key.

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrTimezoneUnavailable reports that the site timezone offset is not
// configured (or a stored value is invalid). It is an internal sentinel:
// API boundaries map it to the stable feature_disabled error without
// revealing why the feature is unavailable.
var ErrTimezoneUnavailable = errors.New("site timezone is not configured")

const (
	// SiteTimezoneKey is the site_config key holding the fixed offset in
	// minutes east of UTC.
	SiteTimezoneKey = "site_timezone_offset_minutes"
	// MinSiteTimezoneOffsetMinutes / MaxSiteTimezoneOffsetMinutes bound the
	// offset to whole zones from UTC-12:00 through UTC+14:00.
	MinSiteTimezoneOffsetMinutes = -720
	MaxSiteTimezoneOffsetMinutes = 840

	// timezoneLockKey marks that temporal data once existed; it is written in
	// the same transaction that first observes such data and is never cleared.
	timezoneLockKey = "site_timezone_offset_locked"
)

// timezoneFreezeTables are the tables whose first row freezes the offset.
// They may not exist yet when this guard runs (their schema lands with their
// own features), so existence is checked per table inside the transaction.
var timezoneFreezeTables = []string{"checkins", "user_activity_daily", "site_activity_daily"}

// ValidSiteTimezoneOffset reports whether minutes is an allowed explicit
// offset: a multiple of 30 within [-720, +840]. Zero is valid and means UTC.
func ValidSiteTimezoneOffset(minutes int) bool {
	if minutes < MinSiteTimezoneOffsetMinutes || minutes > MaxSiteTimezoneOffsetMinutes {
		return false
	}
	return minutes%30 == 0
}

// SiteTimezoneOffsetMinutes returns the configured fixed offset in minutes.
// An unset or invalid stored value yields ErrTimezoneUnavailable; an explicit
// 0 (UTC) is returned as (0, nil).
func (s *Store) SiteTimezoneOffsetMinutes() (int, error) {
	raw, err := s.GetSiteConfigValue(SiteTimezoneKey)
	if err != nil {
		return 0, err
	}
	offset, perr := parseSiteTimezoneOffset(raw)
	if perr != nil {
		return 0, ErrTimezoneUnavailable
	}
	return int(offset), nil
}

// parseSiteTimezoneOffset parses a stored site_config value into a validated
// offset. A missing row is an empty string and fails closed like any other
// invalid value: an unset offset never falls back to an implicit default.
func parseSiteTimezoneOffset(raw string) (int64, error) {
	n, perr := strconv.Atoi(strings.TrimSpace(raw))
	// Require the canonical decimal form: a manually corrupted or
	// non-canonical row ("+30", "030", ...) fails closed instead of being
	// silently accepted.
	if perr != nil || strconv.FormatInt(int64(n), 10) != strings.TrimSpace(raw) || !ValidSiteTimezoneOffset(n) {
		return 0, ErrTimezoneUnavailable
	}
	return int64(n), nil
}

// siteDayKeyAtTx is the in-transaction variant of SiteDayKeyAt: it resolves
// the configured offset from site_config inside the caller's transaction so
// the day key and the business write it buckets share one consistent read of
// the authoritative value. An unset or corrupt offset yields
// ErrTimezoneUnavailable; callers treat that as "activity disabled" and skip
// the write instead of generating a key under an implicit default.
func siteDayKeyAtTx(tx *sql.Tx, unixSeconds int64) (int64, error) {
	var raw sql.NullString
	err := tx.QueryRow(`SELECT value FROM site_config WHERE key = ?`, SiteTimezoneKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrTimezoneUnavailable
	}
	if err != nil {
		return 0, err
	}
	stored := ""
	if raw.Valid {
		stored = raw.String
	}
	offset, perr := parseSiteTimezoneOffset(stored)
	if perr != nil {
		return 0, ErrTimezoneUnavailable
	}
	return SiteDayKey(unixSeconds, offset), nil
}

// SiteDayKeyAt returns the site-local day key for a UTC unix instant: the
// unix seconds of that local day's midnight under the configured offset.
// ErrTimezoneUnavailable propagates when the offset is not configured, so no
// day key is ever generated under an implicit default.
func (s *Store) SiteDayKeyAt(unixSeconds int64) (int64, error) {
	offset, err := s.SiteTimezoneOffsetMinutes()
	if err != nil {
		return 0, err
	}
	return SiteDayKey(unixSeconds, int64(offset)), nil
}

// SiteDayKey maps a UTC unix instant to the unix seconds of the local
// midnight of the day containing it under a fixed offset in minutes east of
// UTC. It is pure arithmetic: floor-division on the shifted instant, so it
// is correct for negative instants and any valid offset.
func SiteDayKey(unixSeconds, offsetMinutes int64) int64 {
	local := unixSeconds + offsetMinutes*60
	days := floorDiv(local, 86400)
	return days*86400 - offsetMinutes*60
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// SetSiteTimezoneOffsetMinutes atomically stores an explicitly configured
// offset after enforcing the immutability guard. The freeze check (lock
// marker plus per-table row counts for every existing freeze table) and the
// upsert run in one write transaction, so a concurrent first checkin/activity
// insert either commits before this transaction (the guard sees it and
// refuses) or after (the offset was already changed while no data existed);
// there is no interleaving that changes the offset over existing data.
// Any stored value — including one equal to the current setting — is refused
// once frozen. An invalid offset is ErrConflict.
func (s *Store) SetSiteTimezoneOffsetMinutes(minutes int) error {
	if !ValidSiteTimezoneOffset(minutes) {
		return ErrConflict
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("set site timezone: %w", err)
	}
	defer tx.Rollback()

	locked, err := timezoneLockExistsTx(tx)
	if err != nil {
		return fmt.Errorf("set site timezone: %w", err)
	}
	if locked {
		return ErrConflict
	}
	hasData, err := timezoneDataExistsTx(tx)
	if err != nil {
		return fmt.Errorf("set site timezone: %w", err)
	}
	if hasData {
		// Freeze permanently before refusing, so later retention cleanup
		// cannot make the key editable again.
		if _, err := tx.Exec(`INSERT INTO site_config (key, value, updated_at) VALUES (?, '1', ?)
			ON CONFLICT(key) DO UPDATE SET value='1', updated_at=excluded.updated_at`,
			timezoneLockKey, time.Now().Unix()); err != nil {
			return fmt.Errorf("set site timezone: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("set site timezone: %w", err)
		}
		return ErrConflict
	}
	if _, err := tx.Exec(`INSERT INTO site_config (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		SiteTimezoneKey, strconv.Itoa(minutes), time.Now().Unix()); err != nil {
		return fmt.Errorf("set site timezone: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set site timezone: %w", err)
	}
	return nil
}

// timezoneLockExistsTx reports whether the permanent freeze marker exists.
func timezoneLockExistsTx(tx *sql.Tx) (bool, error) {
	var count int
	err := tx.QueryRow(`SELECT COUNT(1) FROM site_config WHERE key=?`, timezoneLockKey).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// timezoneDataExistsTx reports whether any freeze table currently holds at
// least one row. Tables that do not exist yet are skipped: they trivially
// hold no data, and their guards activate automatically once created.
func timezoneDataExistsTx(tx *sql.Tx) (bool, error) {
	for _, table := range timezoneFreezeTables {
		exists, err := tableExistsTx(tx, table)
		if err != nil {
			return false, err
		}
		if !exists {
			continue
		}
		var count int
		// The table name comes only from the constant list above; no request
		// text ever reaches this statement.
		if err := tx.QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&count); err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func tableExistsTx(tx *sql.Tx, name string) (bool, error) {
	var count int
	err := tx.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
