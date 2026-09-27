package continuity

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type EligibilityKind string

const (
	CheckinGeneral EligibilityKind = "checkin_general"
	CheckinGame    EligibilityKind = "checkin_game"
	Welfare        EligibilityKind = "welfare"
	GameOnboarding EligibilityKind = "game_onboarding"
)

func eligibilityKind(kind EligibilityKind) bool {
	return kind == CheckinGeneral || kind == CheckinGame || kind == Welfare || kind == GameOnboarding
}

func validLabel(value string) bool {
	return value != "" && len(value) <= 128 && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

// BoundKeyTx rejects absent accounts and bindings. Authentication binds new
// accounts and startup binds existing ones before any listener is opened.
func BoundKeyTx(ctx context.Context, tx *sql.Tx, userID int64) (Key, error) {
	if ctx == nil || tx == nil || userID <= 0 {
		return Key{}, ErrInvalid
	}
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT c.identity_key FROM users u JOIN user_continuity_identities c ON c.user_id=u.id WHERE u.id=? AND u.is_admin=0`, userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Key{}, ErrNotFound
	}
	if err != nil {
		return Key{}, err
	}
	if len(raw) != 32 {
		return Key{}, ErrInvariant
	}
	var key Key
	copy(key[:], raw)
	return key, nil
}

func HasEligibilityTx(ctx context.Context, tx *sql.Tx, userID int64, kind EligibilityKind, scope, window string, now int64) (bool, error) {
	if !eligibilityKind(kind) || !validLabel(scope) || !validLabel(window) || now < 0 || now > maxUnixSecond {
		return false, ErrInvalid
	}
	key, err := BoundKeyTx(ctx, tx, userID)
	if err != nil {
		return false, err
	}
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_continuity_facts WHERE identity_key=? AND kind=? AND scope=? AND window_key=? AND (expires_at IS NULL OR expires_at>?))`, key[:], string(kind), scope, window, now).Scan(&exists)
	return exists, err
}

// ClaimEligibilityTx must share the transaction with its authoritative award.
// False means the stable identity already consumed this exact qualification.
// A later failure rolls back both the qualification and the ledger mutation.
func ClaimEligibilityTx(ctx context.Context, tx *sql.Tx, userID int64, kind EligibilityKind, scope, window string, now int64, expiresAt *int64) (bool, error) {
	if !eligibilityKind(kind) || !validLabel(scope) || !validLabel(window) || now < 0 || now > maxUnixSecond ||
		(kind == GameOnboarding) != (expiresAt == nil) || expiresAt != nil && (*expiresAt <= now || *expiresAt > maxUnixSecond) {
		return false, ErrInvalid
	}
	key, err := BoundKeyTx(ctx, tx, userID)
	if err != nil {
		return false, err
	}
	return claimKeyTx(ctx, tx, key, kind, scope, window, now, expiresAt)
}

func claimKeyTx(ctx context.Context, tx *sql.Tx, key Key, kind EligibilityKind, scope, window string, now int64, expiresAt *int64) (bool, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO identity_continuity_facts(identity_key,kind,scope,window_key,fact_json,occurred_at,expires_at)
VALUES(?,?,?,?, '{"completed":true}',?,?) ON CONFLICT(identity_key,kind,scope,window_key) DO NOTHING`, key[:], string(kind), scope, window, now, expiresAt)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func DailyPeriodTx(ctx context.Context, tx *sql.Tx, now int64) (string, int64, error) {
	if ctx == nil || tx == nil || now < 0 || now > maxUnixSecond-86400 {
		return "", 0, ErrInvalid
	}
	var raw sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, db.SiteTimezoneKey).Scan(&raw); err != nil {
		return "", 0, err
	}
	offset, err := strconv.Atoi(raw.String)
	if !raw.Valid || err != nil || strconv.Itoa(offset) != raw.String || !db.ValidSiteTimezoneOffset(offset) {
		return "", 0, ErrInvariant
	}
	start := db.SiteDayKey(now, int64(offset))
	date := time.Unix(start+int64(offset)*60, 0).UTC().Format("2006-01-02")
	return date, start + 86400, nil
}

// OnboardingScope is tied to the existing compiled game/task rule identity.
// It intentionally excludes adjustable award amounts and an account's ID.
func OnboardingScope(game, task string) (string, error) {
	if !validLabel(game) || !validLabel(task) || strings.ContainsAny(game, ":") || strings.ContainsAny(task, ":") {
		return "", ErrInvalid
	}
	scope := game + ":" + task
	if !validLabel(scope) {
		return "", ErrInvalid
	}
	return scope, nil
}

// PreserveEligibilityTx copies only verifiable, completed facts belonging to
// the extant account. It does not infer the state of previously deleted users.
// It is called before any domain removes check-ins or onboarding receipts.
func (service *Service) PreserveEligibilityTx(ctx context.Context, tx *sql.Tx, userID, now int64) error {
	if now < 0 || now > maxUnixSecond {
		return ErrInvalid
	}
	key, err := service.BindUserTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	var hasDaily bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM checkins WHERE user_id=? UNION ALL SELECT 1 FROM game_checkins WHERE user_id=? UNION ALL SELECT 1 FROM welfare_claims WHERE user_id=?)`, userID, userID, userID).Scan(&hasDaily); err != nil {
		return err
	}
	var day string
	var expiry int64
	if hasDaily {
		day, expiry, err = DailyPeriodTx(ctx, tx, now)
		if err != nil {
			return err
		}
	}
	for _, source := range []struct {
		table string
		kind  EligibilityKind
	}{{"checkins", CheckinGeneral}, {"game_checkins", CheckinGame}, {"welfare_claims", Welfare}} {
		if !hasDaily {
			break
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO identity_continuity_facts(identity_key,kind,scope,window_key,fact_json,occurred_at,expires_at)
SELECT ?,?,'v1',site_day,'{"completed":true}',created_at,? FROM `+source.table+`
WHERE user_id=? AND site_day=? AND created_at<?
ON CONFLICT(identity_key,kind,scope,window_key) DO NOTHING`, key[:], string(source.kind), expiry, userID, day, expiry)
		if err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT game_key,task_key,completed_at FROM game_onboarding_completions WHERE user_id=? ORDER BY game_key,task_key`, userID)
	if err != nil {
		return err
	}
	type completion struct {
		game, task string
		at         int64
	}
	completed := []completion{}
	for rows.Next() {
		var fact completion
		if err := rows.Scan(&fact.game, &fact.task, &fact.at); err != nil {
			rows.Close()
			return err
		}
		completed = append(completed, fact)
		if len(completed) > 1000 {
			rows.Close()
			return ErrInvariant
		}
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	for _, fact := range completed {
		scope, err := OnboardingScope(fact.game, fact.task)
		if err != nil {
			return err
		}
		if _, err = claimKeyTx(ctx, tx, key, GameOnboarding, scope, "v1", fact.at, nil); err != nil {
			return err
		}
	}
	return nil
}
