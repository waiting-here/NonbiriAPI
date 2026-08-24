package db

import (
	"context"
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
	maxDiscordIDBytes = 128
	maxUsernameBytes  = 256
	maxAvatarBytes    = 1024
	maxBanReasonBytes = 1024
	maxAvatarURLBytes = 2048
)

// User is the server-authoritative identity used by later authorization and
// resource repositories. The administrator row is environment-owned and has
// no Discord identity; it is never treated as a normal tenant user.
type User struct {
	ID             int64
	DiscordID      string
	Username       string
	Avatar         string
	GuildNick      string
	GuildAvatarURL string
	IsAdmin        bool
	IsBanned       bool
	BannedReason   string
	// BannedUntil is nil for a permanent ban and while the user is not
	// banned. A non-nil deadline at or before the current instant is lifted
	// lazily by an atomic conditional UPDATE on read.
	BannedUntil *time.Time
	// AutoBanned records whether the most recent effective ban was produced
	// by an automatic rule; every manual ban clears it.
	AutoBanned bool
	// CharitySuspendedUntil suspends charity-feature eligibility only. A due
	// deadline is cleared lazily on read exactly like BannedUntil.
	CharitySuspendedUntil *time.Time
	// Credits is the signed consumption balance in milli-credits. It may be
	// negative (settled over-reservation, administrator-configured penalty);
	// every change commits together with its credit_ledger row.
	Credits int64
	// DonationCredit is the cumulative donor-reward balance in milli-credits.
	// The application layer keeps it non-negative.
	DonationCredit int64
	// Level is the nullable manual level override (1..5; nil = automatic).
	// It never applies to the administrator row (administrators are excluded
	// from the level system). See levels.go for the authoritative resolver.
	Level *int
	// AutoLevel is the persistent automatic-level high-water mark (1..4). It
	// is only ever raised (lazy CAS promotion); there is no downgrade path.
	AutoLevel        int
	EndpointLimit    *int
	RPMLimit         *int
	ConcurrencyLimit *int
	// GameProfilePublic controls the account-wide public projection used by
	// game leaderboards. It defaults to false and is never copied into game
	// round or best-record rows.
	GameProfilePublic         bool
	TotalRequests             int64
	TotalPromptTokens         int64
	TotalCompletionTokens     int64
	TotalUnknownUsageRequests int64
	Lang                      string
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

// BanEffectiveAt reports whether a ban is still in force at instant now:
// permanent bans always are, deadline bans only until the deadline passes
// (deadline <= now counts as expired). This pure projection never mutates
// storage; the lazy lift happens through the repository read paths.
func (u *User) BanEffectiveAt(now time.Time) bool {
	if u == nil || !u.IsBanned {
		return false
	}
	return u.BannedUntil == nil || u.BannedUntil.After(now)
}

// CharitySuspensionEffectiveAt reports whether the charity-eligibility
// suspension is still in force at instant now.
func (u *User) CharitySuspensionEffectiveAt(now time.Time) bool {
	if u == nil {
		return false
	}
	return u.CharitySuspendedUntil != nil && u.CharitySuspendedUntil.After(now)
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

// validateGuildProfile bounds the server-nickname and server-avatar snapshot
// captured at OAuth time. guild_nick is optional (a member without a server
// nickname keeps the empty string so the caller falls back to the global
// username); guild_avatar_url is a full CDN URL built by the auth layer.
func validateGuildProfile(guildNick, guildAvatarURL string) error {
	if err := validateIdentityText(guildNick, maxUsernameBytes, true); err != nil {
		return err
	}
	if err := validateIdentityText(guildAvatarURL, maxAvatarURLBytes, true); err != nil {
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
	u.guild_nick,
	u.guild_avatar_url,
	u.is_admin,
	u.is_banned,
	u.banned_reason,
	u.banned_until,
	u.auto_banned,
	u.charity_suspended_until,
	u.endpoint_limit,
	u.rpm_limit,
	u.concurrency_limit,
	u.game_profile_public,
	u.total_requests,
	u.total_prompt_tokens,
	u.total_completion_tokens,
	u.total_unknown_usage_requests,
	u.credits,
	u.donation_credit,
	u.level,
	u.auto_level,
	u.lang,
	u.created_at,
	u.updated_at`

// scanUser scans the stable user projection. It intentionally does not expose
// any credential material or session/caller-key columns.
func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var isAdmin, isBanned int
	var bannedUntil, charitySuspendedUntil sql.NullInt64
	var autoBanned int
	var endpointLimit, rpmLimit, concurrencyLimit, level sql.NullInt64
	var gameProfilePublic int
	var createdAt, updatedAt int64
	if err := row.Scan(
		&u.ID,
		&u.DiscordID,
		&u.Username,
		&u.Avatar,
		&u.GuildNick,
		&u.GuildAvatarURL,
		&isAdmin,
		&isBanned,
		&u.BannedReason,
		&bannedUntil,
		&autoBanned,
		&charitySuspendedUntil,
		&endpointLimit,
		&rpmLimit,
		&concurrencyLimit,
		&gameProfilePublic,
		&u.TotalRequests,
		&u.TotalPromptTokens,
		&u.TotalCompletionTokens,
		&u.TotalUnknownUsageRequests,
		&u.Credits,
		&u.DonationCredit,
		&level,
		&u.AutoLevel,
		&u.Lang,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin != 0
	u.IsBanned = isBanned != 0
	u.GameProfilePublic = gameProfilePublic != 0
	u.AutoBanned = autoBanned != 0
	u.BannedUntil = nullUnixTime(bannedUntil)
	u.CharitySuspendedUntil = nullUnixTime(charitySuspendedUntil)
	if endpointLimit.Valid {
		value := int(endpointLimit.Int64)
		u.EndpointLimit = &value
	}
	if rpmLimit.Valid {
		value := int(rpmLimit.Int64)
		u.RPMLimit = &value
	}
	if concurrencyLimit.Valid {
		value := int(concurrencyLimit.Int64)
		u.ConcurrencyLimit = &value
	}
	if level.Valid {
		value := int(level.Int64)
		u.Level = &value
	}
	u.CreatedAt = time.Unix(createdAt, 0).UTC()
	u.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return &u, nil
}

// nullUnixTime converts a nullable unix-seconds column into a *time.Time.
func nullUnixTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	t := time.Unix(value.Int64, 0).UTC()
	return &t
}

// GetUserByID returns a user or (nil, nil) when the id is unknown. A due
// temporal ban or charity suspension is lifted lazily by an atomic
// conditional UPDATE before the row is returned.
func (s *Store) GetUserByID(id int64) (*User, error) {
	if id <= 0 {
		return nil, ErrNotFound
	}
	u, err := scanUser(s.db.QueryRow(`SELECT `+userSelectColumns+` FROM users u WHERE u.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.reconcileUserTemporalState(u, time.Now())
	return u, nil
}

// GetUserRPMLimit returns the user's explicit per-minute RPM cap and whether
// one is stored. A missing user or a NULL cap yields (0, false, nil); callers
// fall back to the administrator default. Explicit values are independent of
// that default and are bounded only by MaxUserRPMLimit.
func (s *Store) GetUserRPMLimit(userID int64) (int, bool, error) {
	if userID <= 0 {
		return 0, false, ErrNotFound
	}
	var value sql.NullInt64
	err := s.db.QueryRow(`SELECT rpm_limit FROM users WHERE id=?`, userID).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read user rpm limit: %w", err)
	}
	if !value.Valid {
		return 0, false, nil
	}
	return int(value.Int64), true, nil
}

// UserAdmissionLimits is the single request-time snapshot consumed by the
// public chat admission boundary. RPMLimitSet distinguishes an explicit RPM
// override from the site default; ConcurrencyLimit is always effective (NULL
// has already become DefaultUserConcurrencyLimit). The query also rechecks
// that the identity remains a normal, active account immediately before any
// process-local permit or RPM reservation is acquired.
type UserAdmissionLimits struct {
	RPMLimit         int
	RPMLimitSet      bool
	ConcurrencyLimit int
}

// GetUserAdmissionLimits returns one active user's request-time limit
// snapshot. Missing, administrator, and banned rows are indistinguishable
// ErrNotFound so a stale caller-key context cannot enter admission after its
// account has stopped being callable.
func (s *Store) GetUserAdmissionLimits(ctx context.Context, userID int64) (UserAdmissionLimits, error) {
	if ctx == nil || userID <= 0 {
		return UserAdmissionLimits{}, ErrNotFound
	}
	var rpm, concurrency sql.NullInt64
	nowUnix := time.Now().Unix()
	err := s.db.QueryRowContext(ctx, `
SELECT rpm_limit, concurrency_limit
FROM users
WHERE id=? AND is_admin=0
  AND (is_banned=0 OR (banned_until IS NOT NULL AND banned_until<=?))`, userID, nowUnix).Scan(&rpm, &concurrency)
	if errors.Is(err, sql.ErrNoRows) {
		return UserAdmissionLimits{}, ErrNotFound
	}
	if err != nil {
		return UserAdmissionLimits{}, fmt.Errorf("read user admission limits: %w", err)
	}
	out := UserAdmissionLimits{ConcurrencyLimit: DefaultUserConcurrencyLimit}
	if rpm.Valid {
		if rpm.Int64 < 1 || rpm.Int64 > MaxUserRPMLimit {
			return UserAdmissionLimits{}, fmt.Errorf("read user admission limits: invalid rpm limit")
		}
		out.RPMLimit = int(rpm.Int64)
		out.RPMLimitSet = true
	}
	if concurrency.Valid {
		if concurrency.Int64 < 1 || concurrency.Int64 > MaxUserConcurrencyLimit {
			return UserAdmissionLimits{}, fmt.Errorf("read user admission limits: invalid concurrency limit")
		}
		out.ConcurrencyLimit = int(concurrency.Int64)
	}
	return out, nil
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
	if err != nil {
		return nil, err
	}
	s.reconcileUserTemporalState(u, time.Now())
	return u, nil
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
func (s *Store) FindOrCreateDiscordUser(discordID, username, avatar, guildNick, guildAvatarURL string) (user *User, created bool, err error) {
	if err := validateDiscordIdentity(discordID, username, avatar); err != nil {
		return nil, false, ErrConflict
	}
	if err := validateGuildProfile(guildNick, guildAvatarURL); err != nil {
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
		if _, err := tx.Exec(`UPDATE users SET username=?, avatar=?, guild_nick=?, guild_avatar_url=?, updated_at=? WHERE id=? AND is_admin=0`, username, avatar, guildNick, guildAvatarURL, time.Now().Unix(), id); err != nil {
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
		(discord_id, username, avatar, guild_nick, guild_avatar_url, is_admin, is_banned, banned_reason, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, 0, '', ?, ?)`, discordID, username, avatar, guildNick, guildAvatarURL, now, now)
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
	user, created, err := s.FindOrCreateDiscordUser(discordID, username, avatar, "", "")
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
func (s *Store) UpdateUserProfile(userID int64, username, avatar, guildNick, guildAvatarURL string) error {
	if userID <= 0 || validateIdentityText(username, maxUsernameBytes, false) != nil || validateIdentityText(avatar, maxAvatarBytes, true) != nil || validateGuildProfile(guildNick, guildAvatarURL) != nil {
		return ErrConflict
	}
	result, err := s.db.Exec(`UPDATE users SET username=?, avatar=?, guild_nick=?, guild_avatar_url=?, updated_at=? WHERE id=? AND is_admin=0`, username, avatar, guildNick, guildAvatarURL, time.Now().Unix(), userID)
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

// UserBan describes one ban decision. DurationSeconds == 0 means permanent;
// a positive value sets a lazy expiry deadline (banned_until). Auto records
// whether the ban was produced by an automatic rule; every manual ban clears
// the stored flag.
type UserBan struct {
	Reason          string
	DurationSeconds int64
	Auto            bool
}

// MaxBanDurationSeconds bounds a custom temporary ban to ten years, so an
// administrative typo can never create an effectively unbounded deadline.
const MaxBanDurationSeconds = int64(10 * 366 * 24 * 3600)

// BanUser atomically marks a normal user banned and removes all existing
// sessions and caller-key material. Authentication also checks is_banned at
// request time, so a lookup cannot revive a banned identity.
func (s *Store) BanUser(userID int64, reason string) error {
	return s.BanUserWithOptions(userID, UserBan{Reason: reason})
}

// BanUserWithOptions is the full-form ban: a permanent or deadline ban with
// the auto/manual provenance flag. The ban, session deletion, and caller-key
// deletion commit in one transaction, so request-time auth and platform-exit
// auth are invalidated atomically.
func (s *Store) BanUserWithOptions(userID int64, ban UserBan) error {
	if userID <= 0 || validateBanReason(ban.Reason) != nil {
		return ErrConflict
	}
	if ban.DurationSeconds < 0 || ban.DurationSeconds > MaxBanDurationSeconds {
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
	var until any
	if ban.DurationSeconds > 0 {
		until = now + ban.DurationSeconds
	}
	if _, err := tx.Exec(`UPDATE users SET is_banned=1, banned_reason=?, banned_until=?, auto_banned=?, updated_at=? WHERE id=? AND is_admin=0`,
		ban.Reason, until, boolInt(ban.Auto), now, userID); err != nil {
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

// UnbanUser clears the normal-user ban, including any pending expiry
// deadline. A caller key is not recreated: the user must explicitly generate
// a new one after a ban.
func (s *Store) UnbanUser(userID int64) error {
	if userID <= 0 {
		return ErrNotFound
	}
	result, err := s.db.Exec(`UPDATE users SET is_banned=0, banned_reason='', banned_until=NULL, updated_at=? WHERE id=? AND is_admin=0`, time.Now().Unix(), userID)
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
	return s.GetSiteConfigValueContext(context.Background(), key)
}

// GetSiteConfigValueContext is the cancellation-aware runtime read used on
// request paths. A missing key remains an empty value, matching the
// compatibility helper above.
func (s *Store) GetSiteConfigValueContext(ctx context.Context, key string) (string, error) {
	if ctx == nil {
		return "", ErrNotFound
	}
	if err := validateIdentityText(key, 128, false); err != nil {
		return "", ErrNotFound
	}
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read site configuration: %w", err)
	}
	if validateSiteConfigText(value, maxSiteConfigValueBytes, true, true) != nil {
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

// RegistrationOpen reports whether new-user registration is currently
// allowed. The default (unset or any value other than "0") is open, matching
// the pre-existing behavior; an explicit "0" closes registration. Existing
// users always sign in regardless of this toggle, which is evaluated only on
// the new-identity branch of the OAuth callback.
func (s *Store) RegistrationOpen() (bool, error) {
	raw, err := s.GetSiteConfigValue("registration_open")
	if err != nil {
		return true, err
	}
	return raw != "0", nil
}
