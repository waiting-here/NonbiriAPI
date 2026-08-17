package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrAdminProtected prevents ordinary-user operations from changing the
	// environment-owned administrator identity.
	ErrAdminProtected = errors.New("administrator identity is protected")
	// ErrBanned is returned when an operation requires an active user.
	ErrBanned = errors.New("user is banned")
)

const (
	maxDiscordIDBytes   = 128
	maxUsernameBytes    = 256
	maxAvatarBytes      = 1024
	maxBanReasonBytes   = 1024
	maxConfigValueBytes = 4096
)

// User is the server-authoritative identity used by later authorization and
// resource repositories. The administrator row is environment-owned and has
// no Discord identity; it is never treated as a normal tenant user.
type User struct {
	ID                        int64
	DiscordID                 string
	Username                  string
	Avatar                    string
	IsAdmin                   bool
	IsBanned                  bool
	BannedReason              string
	EndpointLimit             *int
	RPMLimit                  *int
	TotalRequests             int64
	TotalPromptTokens         int64
	TotalCompletionTokens     int64
	TotalUnknownUsageRequests int64
	Lang                      string
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

// IsActive reports whether this identity can authenticate as a normal user.
func (u *User) IsActive() bool { return u != nil && !u.IsAdmin && !u.IsBanned }

func validateIdentityText(value string, maxBytes int, allowEmpty bool) error {
	if !allowEmpty && value == "" {
		return fmt.Errorf("identity value is required")
	}
	if len(value) > maxBytes || !utf8.ValidString(value) {
		return fmt.Errorf("identity value is invalid")
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == 0x7f {
			return fmt.Errorf("identity value is invalid")
		}
	}
	return nil
}

func validateDiscordIdentity(discordID, username, avatar string) error {
	if err := validateIdentityText(discordID, maxDiscordIDBytes, false); err != nil {
		return err
	}
	if err := validateIdentityText(username, maxUsernameBytes, false); err != nil {
		return err
	}
	if err := validateIdentityText(avatar, maxAvatarBytes, true); err != nil {
		return err
	}
	return nil
}

func validateBanReason(reason string) error {
	if err := validateIdentityText(reason, maxBanReasonBytes, true); err != nil {
		return err
	}
	return nil
}

const userSelectColumns = `
	u.id,
	COALESCE(u.discord_id, ''),
	u.username,
	u.avatar,
	u.is_admin,
	u.is_banned,
	u.banned_reason,
	u.endpoint_limit,
	u.rpm_limit,
	u.total_requests,
	u.total_prompt_tokens,
	u.total_completion_tokens,
	u.total_unknown_usage_requests,
	u.lang,
	u.created_at,
	u.updated_at`

// scanUser scans the stable user projection. It intentionally does not expose
// any credential material or session/caller-key columns.
func scanUser(row interface{ Scan(...any) error }) (*User, error) {
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
	); err != nil {
		return nil, err
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
	return &u, nil
}

// GetUserByID returns a user or (nil, nil) when the id is unknown.
func (s *Store) GetUserByID(id int64) (*User, error) {
	if id <= 0 {
		return nil, ErrNotFound
	}
	u, err := scanUser(s.db.QueryRow(`SELECT `+userSelectColumns+` FROM users u WHERE u.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// GetUserByDiscordID returns a normal Discord user or (nil, nil). The query
// excludes the environment-owned administrator even if a legacy database has
// an accidentally populated Discord value on that row.
func (s *Store) GetUserByDiscordID(discordID string) (*User, error) {
	if err := validateIdentityText(discordID, maxDiscordIDBytes, false); err != nil {
		return nil, ErrNotFound
	}
	u, err := scanUser(s.db.QueryRow(`SELECT `+userSelectColumns+` FROM users u WHERE u.discord_id=? AND u.is_admin=0`, discordID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// CreateDiscordUser inserts a normal user. For an idempotent OAuth callback,
// prefer FindOrCreateDiscordUser.
func (s *Store) CreateDiscordUser(discordID, username, avatar string) (*User, error) {
	if err := validateDiscordIdentity(discordID, username, avatar); err != nil {
		return nil, ErrConflict
	}
	now := time.Now().Unix()
	result, err := s.db.Exec(`INSERT INTO users
		(discord_id, username, avatar, is_admin, is_banned, banned_reason, created_at, updated_at)
		VALUES (?, ?, ?, 0, 0, '', ?, ?)`, discordID, username, avatar, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return s.GetUserByID(id)
}

// FindOrCreateDiscordUser atomically refreshes an existing identity or
// creates the first row for a Discord id. It returns created=true only for a
// newly inserted normal user. The caller performs the registration gate before
// invoking this method for a missing identity.
func (s *Store) FindOrCreateDiscordUser(discordID, username, avatar string) (user *User, created bool, err error) {
	if err := validateDiscordIdentity(discordID, username, avatar); err != nil {
		return nil, false, ErrConflict
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, fmt.Errorf("find or create user: %w", err)
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRow(`SELECT id FROM users WHERE discord_id=? AND is_admin=0`, discordID).Scan(&id)
	switch {
	case err == nil:
		if _, err := tx.Exec(`UPDATE users SET username=?, avatar=?, updated_at=? WHERE id=? AND is_admin=0`, username, avatar, time.Now().Unix(), id); err != nil {
			return nil, false, fmt.Errorf("refresh user: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("refresh user: %w", err)
		}
		user, err := s.GetUserByID(id)
		return user, false, err
	case !errors.Is(err, sql.ErrNoRows):
		return nil, false, fmt.Errorf("find user: %w", err)
	}

	now := time.Now().Unix()
	result, err := tx.Exec(`INSERT INTO users
		(discord_id, username, avatar, is_admin, is_banned, banned_reason, created_at, updated_at)
		VALUES (?, ?, ?, 0, 0, '', ?, ?)`, discordID, username, avatar, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, false, ErrConflict
		}
		return nil, false, fmt.Errorf("create user: %w", err)
	}
	id, err = result.LastInsertId()
	if err != nil {
		return nil, false, fmt.Errorf("create user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("create user: %w", err)
	}
	user, err = s.GetUserByID(id)
	return user, true, err
}

// CreateUser is retained as a concise repository entry point for the identity
// rail; it creates only a normal Discord user.
func (s *Store) CreateUser(discordID, username, avatar string) (*User, error) {
	user, created, err := s.FindOrCreateDiscordUser(discordID, username, avatar)
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, ErrConflict
	}
	return user, nil
}

// EnsureAdminUser creates or refreshes the single environment-owned admin row.
// The password is never passed to this method and is never persisted.
func (s *Store) EnsureAdminUser(username string) (*User, error) {
	if err := validateIdentityText(username, maxUsernameBytes, false); err != nil {
		return nil, ErrConflict
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("ensure admin: %w", err)
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRow(`SELECT id FROM users WHERE is_admin=1 ORDER BY id LIMIT 1`).Scan(&id)
	now := time.Now().Unix()
	switch {
	case errors.Is(err, sql.ErrNoRows):
		result, insertErr := tx.Exec(`INSERT INTO users
			(discord_id, username, avatar, is_admin, is_banned, banned_reason, created_at, updated_at)
			VALUES (NULL, ?, '', 1, 0, '', ?, ?)`, username, now, now)
		if insertErr != nil {
			return nil, fmt.Errorf("ensure admin: %w", insertErr)
		}
		id, err = result.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("ensure admin: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("ensure admin: %w", err)
	default:
		if _, err := tx.Exec(`UPDATE users SET username=?, is_banned=0, banned_reason='', updated_at=? WHERE id=? AND is_admin=1`, username, now, id); err != nil {
			return nil, fmt.Errorf("ensure admin: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("ensure admin: %w", err)
	}
	return s.GetUserByID(id)
}

// UpdateUserProfile refreshes only bounded, non-secret provider metadata.
func (s *Store) UpdateUserProfile(userID int64, username, avatar string) error {
	if userID <= 0 || validateIdentityText(username, maxUsernameBytes, false) != nil || validateIdentityText(avatar, maxAvatarBytes, true) != nil {
		return ErrConflict
	}
	result, err := s.db.Exec(`UPDATE users SET username=?, avatar=?, updated_at=? WHERE id=? AND is_admin=0`, username, avatar, time.Now().Unix(), userID)
	if err != nil {
		return fmt.Errorf("update user profile: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update user profile: %w", err)
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

// BanUser atomically marks a normal user banned and removes all existing
// sessions and caller-key material. Authentication also checks is_banned at
// request time, so a lookup cannot revive a banned identity.
func (s *Store) BanUser(userID int64, reason string) error {
	if userID <= 0 || validateBanReason(reason) != nil {
		return ErrConflict
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("ban user: %w", err)
	}
	defer tx.Rollback()
	var isAdmin int
	if err := tx.QueryRow(`SELECT is_admin FROM users WHERE id=?`, userID).Scan(&isAdmin); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("ban user: %w", err)
	} else if isAdmin != 0 {
		return ErrAdminProtected
	}
	now := time.Now().Unix()
	if _, err := tx.Exec(`UPDATE users SET is_banned=1, banned_reason=?, updated_at=? WHERE id=? AND is_admin=0`, reason, now, userID); err != nil {
		return fmt.Errorf("ban user: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("ban user: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM caller_keys WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("ban user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ban user: %w", err)
	}
	return nil
}

// UnbanUser clears the normal-user ban. A caller key is not recreated: the
// user must explicitly generate a new one after a ban.
func (s *Store) UnbanUser(userID int64) error {
	if userID <= 0 {
		return ErrNotFound
	}
	result, err := s.db.Exec(`UPDATE users SET is_banned=0, banned_reason='', updated_at=? WHERE id=? AND is_admin=0`, time.Now().Unix(), userID)
	if err != nil {
		return fmt.Errorf("unban user: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("unban user: %w", err)
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

// DeleteUserSecurityState is the identity rail's deletion hook. Later account
// deletion code can call it in its own larger transaction; no token or key
// plaintext is accepted or returned here.
func (s *Store) DeleteUserSecurityState(userID int64) error {
	if userID <= 0 {
		return ErrNotFound
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("delete user security state: %w", err)
	}
	defer tx.Rollback()
	var isAdmin int
	if err := tx.QueryRow(`SELECT is_admin FROM users WHERE id=?`, userID).Scan(&isAdmin); errors.Is(err, sql.ErrNoRows) {
		// Deletion hooks are intentionally idempotent: the account may have
		// already been removed by the larger lifecycle transaction.
		return nil
	} else if err != nil {
		return fmt.Errorf("delete user security state: %w", err)
	} else if isAdmin != 0 {
		return ErrAdminProtected
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("delete user security state: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM caller_keys WHERE user_id=?`, userID); err != nil {
		return fmt.Errorf("delete user security state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete user security state: %w", err)
	}
	return nil
}

// GetSiteConfigValue returns a runtime site_config value. A missing key is an
// empty value; identity policy fails closed when required gate values are
// absent.
func (s *Store) GetSiteConfigValue(key string) (string, error) {
	if err := validateIdentityText(key, 128, false); err != nil {
		return "", ErrNotFound
	}
	var value string
	err := s.db.QueryRow(`SELECT value FROM site_config WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read site configuration: %w", err)
	}
	if validateIdentityText(value, maxConfigValueBytes, true) != nil {
		return "", fmt.Errorf("site configuration is invalid")
	}
	return value, nil
}

// DiscordRegistrationGate reads the two runtime gate values together. Both
// values are required for new registrations; existing identities are not
// evaluated against this gate during login.
func (s *Store) DiscordRegistrationGate() (guildID, roleID string, err error) {
	guildID, err = s.GetSiteConfigValue("discord_guild_id")
	if err != nil {
		return "", "", err
	}
	roleID, err = s.GetSiteConfigValue("discord_role_id")
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(guildID), strings.TrimSpace(roleID), nil
}
