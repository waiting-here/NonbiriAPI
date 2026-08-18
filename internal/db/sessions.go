package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// UserSessionIdleTTL is the sliding lifetime of a session when it is used.
	UserSessionIdleTTL = 7 * 24 * time.Hour
	// UserSessionAbsoluteTTL is immutable and caps sliding renewal.
	UserSessionAbsoluteTTL = 30 * 24 * time.Hour
	// SessionRenewalThreshold prevents a write on every request while still
	// keeping active sessions sliding.
	SessionRenewalThreshold = 1 * time.Minute
	maxSessionTokenBytes    = 256
)

// Compatibility aliases make the TTL policy explicit to callers without
// exposing any persistence detail.
const (
	SessionTTL                     = UserSessionIdleTTL
	SessionAbsoluteTTL             = UserSessionAbsoluteTTL
	DefaultSessionIdleTTL          = UserSessionIdleTTL
	DefaultSessionAbsoluteTTL      = UserSessionAbsoluteTTL
	DefaultSessionRenewalThreshold = SessionRenewalThreshold
)

// SessionExpiry reports both server-enforced expiry boundaries. The opaque
// token itself is deliberately not part of this value.
type SessionExpiry struct {
	Idle     time.Time
	Absolute time.Time
}

func hashOpaqueToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// SessionHash returns the irreversible lookup hash of an opaque session token.
// It is the single session-binding value other rails may hold in memory (for
// example to bind an elevated capability to the session that requested it).
// The raw token is never derivable from the hash, and the hash itself is never
// a session identifier.
func SessionHash(token string) string { return hashOpaqueToken(token) }

func validateSessionToken(token string) bool {
	if token == "" || len(token) > maxSessionTokenBytes || !utf8.ValidString(token) {
		return false
	}
	for _, r := range token {
		if unicode.IsControl(r) || r == 0x7f {
			return false
		}
	}
	return true
}

func generateSessionToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		clear(raw)
		return "", fmt.Errorf("generate session token")
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	clear(raw)
	return token, nil
}

func (s *Store) createSessionAtCredGen(userID int64, admin bool, credGen string, now time.Time) (string, SessionExpiry, error) {
	if userID <= 0 {
		return "", SessionExpiry{}, ErrNotFound
	}
	token, err := generateSessionToken()
	if err != nil {
		return "", SessionExpiry{}, err
	}
	idle := now.Add(UserSessionIdleTTL)
	absolute := now.Add(UserSessionAbsoluteTTL)
	if !idle.Before(absolute) {
		idle = absolute
	}

	tx, err := s.db.Begin()
	if err != nil {
		return "", SessionExpiry{}, fmt.Errorf("create session")
	}
	defer tx.Rollback()
	var isAdmin, isBanned int
	if err := tx.QueryRow(`SELECT is_admin, is_banned FROM users WHERE id=?`, userID).Scan(&isAdmin, &isBanned); errors.Is(err, sql.ErrNoRows) {
		return "", SessionExpiry{}, ErrNotFound
	} else if err != nil {
		return "", SessionExpiry{}, fmt.Errorf("create session")
	}
	if (isAdmin != 0) != admin || (!admin && isBanned != 0) {
		if !admin && isBanned != 0 {
			return "", SessionExpiry{}, ErrBanned
		}
		return "", SessionExpiry{}, ErrNotFound
	}
	_, err = tx.Exec(`INSERT INTO sessions
		(token_hash, user_id, oauth_state, last_seen_at, expires_at, absolute_expires_at, created_at, cred_gen)
		VALUES (?, ?, '', ?, ?, ?, ?, ?)`, hashOpaqueToken(token), userID, now.Unix(), idle.Unix(), absolute.Unix(), now.Unix(), credGen)
	if err != nil {
		return "", SessionExpiry{}, fmt.Errorf("create session")
	}
	if err := tx.Commit(); err != nil {
		return "", SessionExpiry{}, fmt.Errorf("create session")
	}
	return token, SessionExpiry{Idle: idle, Absolute: absolute}, nil
}

// createSessionAt is the compatibility issuer with no credential-generation
// binding. The stored cred_gen is the empty string, so the row authenticates
// only through the matching compatibility entry points.
func (s *Store) createSessionAt(userID int64, admin bool, now time.Time) (string, SessionExpiry, error) {
	return s.createSessionAtCredGen(userID, admin, "", now)
}

// CreateUserSession issues a high-entropy opaque user session. Only its hash
// is written to SQLite.
func (s *Store) CreateUserSession(userID int64) (string, SessionExpiry, error) {
	return s.createSessionAt(userID, false, time.Now())
}

// CreateAdminSession issues a separate admin-station session for the
// environment-owned admin row. It is a compatibility entry point: the session
// is not bound to a credential generation and authenticates only through the
// matching compatibility entry points. Production callers must use
// CreateAdminSessionWithCredGen so the row is stamped with the current
// administrator credential-generation fingerprint.
func (s *Store) CreateAdminSession(userID int64) (string, SessionExpiry, error) {
	return s.createSessionAt(userID, true, time.Now())
}

// CreateAdminSessionWithCredGen issues an admin-station session bound to the
// supplied opaque credential-generation fingerprint. credGen is an opaque
// HMAC-SHA-256 fingerprint of the current administrator password (never the
// password itself, and never a raw SHA-256 of it); the database stores and
// compares only that opaque string. An empty credGen leaves the row unbound
// (compatibility) and is accepted only for tests.
func (s *Store) CreateAdminSessionWithCredGen(userID int64, credGen string) (string, SessionExpiry, error) {
	return s.createSessionAtCredGen(userID, true, credGen, time.Now())
}

// CreateSession is a compatibility entry point. The user's server-side role
// selects the session domain; callers that need an explicit boundary should
// use CreateUserSession or CreateAdminSession.
func (s *Store) CreateSession(userID int64) (string, SessionExpiry, error) {
	user, err := s.GetUserByID(userID)
	if err != nil || user == nil {
		if err != nil {
			return "", SessionExpiry{}, err
		}
		return "", SessionExpiry{}, ErrNotFound
	}
	return s.createSessionAt(userID, user.IsAdmin, time.Now())
}

// getSessionUserAtCredGen resolves a token in one SQLite transaction. The
// query checks role and ban state at the same serialization point as idle
// renewal. For administrator sessions (admin=true) the stored credential-
// generation fingerprint is compared atomically in the same SELECT: a row
// whose cred_gen no longer matches the supplied fingerprint is treated as
// not-found and deleted, so a rotated administrator password revokes its
// stale long-lived sessions at the next request. credGen is an opaque
// high-entropy HMAC fingerprint (never the password), so the SQL string
// comparison leaks no password material; an empty credGen disables the
// fingerprint check (compatibility entry points only).
func (s *Store) getSessionUserAtCredGen(token string, admin bool, credGen string, now time.Time) (*User, error) {
	if !validateSessionToken(token) {
		return nil, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("authenticate session")
	}
	defer tx.Rollback()

	query := `SELECT ` + userSelectColumns + `, s.expires_at, s.absolute_expires_at
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=? AND u.is_admin=? AND (? OR u.is_banned=0)`
	args := []any{hashOpaqueToken(token), boolInt(admin), boolInt(admin)}
	if admin && credGen != "" {
		query += ` AND s.cred_gen=?`
		args = append(args, credGen)
	}
	row := tx.QueryRow(query, args...)
	// scanUser expects exactly the user projection, so scan the combined row
	// into a small adapter first and keep the token hash out of the result.
	var scanned sessionUserScan
	if err := scanned.scan(row); errors.Is(err, sql.ErrNoRows) {
		if admin && credGen != "" {
			// A row whose stored cred_gen no longer matches the current
			// fingerprint is stale (administrator password was rotated). Drop
			// it so it cannot linger until idle/absolute expiry. Only
			// administrator-stamped rows are candidates: user sessions and
			// compatibility-stamped admin rows carry the empty string, so the
			// cred_gen<>'' guard excludes them and an administrator-station
			// lookup can never delete a normal user's session. Legacy NULL
			// rows (pre-migration) are also excluded and simply fail the check.
			if _, err := tx.Exec(`DELETE FROM sessions WHERE token_hash=? AND cred_gen<>'' AND cred_gen<>?`,
				hashOpaqueToken(token), credGen); err != nil {
				return nil, fmt.Errorf("authenticate session")
			}
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("authenticate session")
			}
		}
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("authenticate session")
	}
	user := scanned.user
	expiresAt := scanned.expiresAt
	absoluteExpiresAt := scanned.absoluteExpiresAt

	if now.Unix() >= expiresAt || now.Unix() >= absoluteExpiresAt {
		if _, err := tx.Exec(`DELETE FROM sessions WHERE token_hash=?`, hashOpaqueToken(token)); err != nil {
			return nil, fmt.Errorf("authenticate session")
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("authenticate session")
		}
		return nil, nil
	}

	renewed := now.Add(UserSessionIdleTTL)
	absolute := time.Unix(absoluteExpiresAt, 0)
	if renewed.After(absolute) {
		renewed = absolute
	}
	newExpiresAt := expiresAt
	if renewed.Unix() > expiresAt && renewed.Sub(time.Unix(expiresAt, 0)) >= SessionRenewalThreshold {
		newExpiresAt = renewed.Unix()
	}
	if _, err := tx.Exec(`UPDATE sessions SET last_seen_at=?, expires_at=?
		WHERE token_hash=? AND expires_at=? AND absolute_expires_at=?`,
		now.Unix(), newExpiresAt, hashOpaqueToken(token), expiresAt, absoluteExpiresAt); err != nil {
		return nil, fmt.Errorf("authenticate session")
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("authenticate session")
	}
	return user, nil
}

// getSessionUserAt is the compatibility resolver with no credential-generation
// check. It delegates with an empty fingerprint so existing callers keep
// their behavior; production administrator authentication must use the
// credGen-aware path through AuthenticateAdminSessionWithCredGen.
func (s *Store) getSessionUserAt(token string, admin bool, now time.Time) (*User, error) {
	return s.getSessionUserAtCredGen(token, admin, "", now)
}

type sessionUserScan struct {
	user              *User
	expiresAt         int64
	absoluteExpiresAt int64
}

func (s *sessionUserScan) scan(row interface{ Scan(...any) error }) error {
	var u User
	var isAdmin, isBanned int
	var endpointLimit, rpmLimit sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(
		&u.ID,
		&u.DiscordID,
		&u.Username,
		&u.Avatar,
		&isAdmin,
		&isBanned,
		&u.BannedReason,
		&endpointLimit,
		&rpmLimit,
		&u.TotalRequests,
		&u.TotalPromptTokens,
		&u.TotalCompletionTokens,
		&u.TotalUnknownUsageRequests,
		&u.Lang,
		&createdAt,
		&updatedAt,
		&s.expiresAt,
		&s.absoluteExpiresAt,
	); err != nil {
		return err
	}
	u.IsAdmin = isAdmin != 0
	u.IsBanned = isBanned != 0
	if endpointLimit.Valid {
		value := int(endpointLimit.Int64)
		u.EndpointLimit = &value
	}
	if rpmLimit.Valid {
		value := int(rpmLimit.Int64)
		u.RPMLimit = &value
	}
	u.CreatedAt = time.Unix(createdAt, 0).UTC()
	u.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	s.user = &u
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// AuthenticateUserSession resolves only a normal-user session and renews its
// idle deadline without crossing the absolute deadline.
func (s *Store) AuthenticateUserSession(token string) (*User, error) {
	return s.getSessionUserAt(token, false, time.Now())
}

// AuthenticateAdminSession resolves only an administrator session. A normal
// user session can never enter this domain because the SQL predicate checks
// is_admin. It is a compatibility entry point: no credential-generation
// fingerprint is checked, so it authenticates only rows stamped with an empty
// cred_gen. Production callers must use AuthenticateAdminSessionWithCredGen.
func (s *Store) AuthenticateAdminSession(token string) (*User, error) {
	return s.getSessionUserAt(token, true, time.Now())
}

// AuthenticateAdminSessionWithCredGen resolves only an administrator session
// and, within the same transaction, requires the stored credential-generation
// fingerprint to equal credGen. A mismatch (administrator password rotated)
// is treated as not-found and the stale row is deleted; the caller surfaces a
// standard unauthorized response. credGen is an opaque HMAC fingerprint, not
// the password. An empty credGen disables the fingerprint check and is
// accepted only for compatibility/tests.
func (s *Store) AuthenticateAdminSessionWithCredGen(token, credGen string) (*User, error) {
	return s.getSessionUserAtCredGen(token, true, credGen, time.Now())
}

// GetSessionUser is the safe compatibility entry point for user-station
// callers. It deliberately resolves only a normal-user session; administrator
// code must opt into AuthenticateAdminSession explicitly so a generic helper
// cannot cross the station authorization boundary.
func (s *Store) GetSessionUser(token string) (*User, error) {
	return s.AuthenticateUserSession(token)
}

// GetAdminSessionUser is an explicit administrator-station compatibility
// entry point.
func (s *Store) GetAdminSessionUser(token string) (*User, error) {
	return s.AuthenticateAdminSession(token)
}

// DeleteSession invalidates exactly one opaque session token.
func (s *Store) DeleteSession(token string) error {
	if !validateSessionToken(token) {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, hashOpaqueToken(token)); err != nil {
		return fmt.Errorf("delete session")
	}
	return nil
}

// ConsumeSession atomically deletes a session and reports whether it existed.
func (s *Store) ConsumeSession(token string) (bool, error) {
	if !validateSessionToken(token) {
		return false, nil
	}
	result, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, hashOpaqueToken(token))
	if err != nil {
		return false, fmt.Errorf("consume session")
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// DeleteUserSessions invalidates all sessions for a user. It is idempotent.
func (s *Store) DeleteUserSessions(userID int64) error {
	if userID <= 0 {
		return ErrNotFound
	}
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("delete user sessions")
	}
	return nil
}

// PurgeExpiredSessions removes both idle-expired and absolute-expired rows.
func (s *Store) PurgeExpiredSessions() (int64, error) {
	return s.purgeExpiredSessionsAt(time.Now())
}

func (s *Store) purgeExpiredSessionsAt(now time.Time) (int64, error) {
	result, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at<=? OR absolute_expires_at<=?`, now.Unix(), now.Unix())
	if err != nil {
		return 0, fmt.Errorf("purge sessions")
	}
	return result.RowsAffected()
}

// SessionRowCount is a bounded inspection hook for maintenance and tests; it
// never returns token material.
func (s *Store) SessionRowCount() (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
