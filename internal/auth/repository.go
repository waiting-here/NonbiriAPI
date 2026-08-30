package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

var (
	errSessionUnauthorized = errors.New("session unauthorized")
	errSessionForbidden    = errors.New("session forbidden")
	errIdentityConflict    = errors.New("identity conflict")
)

const maxUnixSecond = int64(253402300799)

type sessionPrincipal struct {
	actor     authz.Actor
	username  string
	expiresAt int64
}

func randomOpaque(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		clear(raw)
		return "", err
	}
	out := base64.RawURLEncoding.EncodeToString(raw)
	clear(raw)
	return out, nil
}

func sessionLookupHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (r *Runtime) createSessionTx(ctx context.Context, tx *sql.Tx, userID int64, credGen string, now int64) (string, int64, error) {
	if userID <= 0 || credGen == "" || now < 0 {
		return "", 0, errSessionUnauthorized
	}
	token, err := randomOpaque(32)
	if err != nil {
		return "", 0, fmt.Errorf("create session material: %w", err)
	}
	hash := sessionLookupHash(token)
	absolute := now + int64(r.absoluteTTL/time.Second)
	if absolute > maxUnixSecond {
		return "", 0, fmt.Errorf("create session: invalid expiry")
	}
	expires := now + int64(r.idleTTL/time.Second)
	if expires > absolute {
		expires = absolute
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
		return "", 0, fmt.Errorf("replace session: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,oauth_state,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,'',?,?,?,?,?)`, hash, userID, now, expires, absolute, now, credGen); err != nil {
		return "", 0, fmt.Errorf("create session: %w", err)
	}
	return token, expires, nil
}

func (r *Runtime) authenticate(ctx context.Context, rawToken string, kind authz.ActorKind, elevationToken string) (sessionPrincipal, error) {
	if rawToken == "" || len(rawToken) > 4096 {
		return sessionPrincipal{}, errSessionUnauthorized
	}
	hash := sessionLookupHash(rawToken)
	now := r.now().Unix()
	if now < 0 || now > maxUnixSecond {
		return sessionPrincipal{}, fmt.Errorf("authenticate session: invalid time")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return sessionPrincipal{}, fmt.Errorf("authenticate session: begin: %w", err)
	}
	done := false
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()
	var p sessionPrincipal
	var storedKind int
	var banned int
	var bannedUntil sql.NullInt64
	var absolute int64
	var expires int64
	err = tx.QueryRowContext(ctx, `SELECT s.user_id,s.cred_gen,s.expires_at,s.absolute_expires_at,u.username,u.is_admin,u.is_banned,u.banned_until FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=?`, hash).Scan(&p.actor.UserID, &p.actor.SessionGeneration, &expires, &absolute, &p.username, &storedKind, &banned, &bannedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionPrincipal{}, errSessionUnauthorized
	}
	if err != nil {
		return sessionPrincipal{}, fmt.Errorf("authenticate session: read: %w", err)
	}
	expectedAdmin := kind == authz.ActorAdminSession
	if (storedKind == 1) != expectedAdmin {
		return sessionPrincipal{}, errSessionForbidden
	}
	if expires <= now || absolute <= now {
		_, _ = tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, hash)
		if err := tx.Commit(); err == nil {
			done = true
		}
		return sessionPrincipal{}, errSessionUnauthorized
	}
	if expectedAdmin && p.actor.SessionGeneration != r.adminCredentialGeneration {
		_, _ = tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, hash)
		if err := tx.Commit(); err == nil {
			done = true
		}
		return sessionPrincipal{}, errSessionUnauthorized
	}
	if expectedAdmin && p.username != r.adminUsername {
		_, _ = tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, hash)
		if err := tx.Commit(); err == nil {
			done = true
		}
		return sessionPrincipal{}, errSessionUnauthorized
	}
	if !expectedAdmin && banned == 1 && (!bannedUntil.Valid || bannedUntil.Int64 > now) {
		return sessionPrincipal{}, errSessionForbidden
	}
	newExpiry := now + int64(r.idleTTL/time.Second)
	if newExpiry > absolute {
		newExpiry = absolute
	}
	result, err := tx.ExecContext(ctx, `UPDATE sessions SET last_seen_at=?,expires_at=? WHERE token_hash=? AND user_id=? AND cred_gen=? AND expires_at=? AND absolute_expires_at=?`, now, newExpiry, hash, p.actor.UserID, p.actor.SessionGeneration, expires, absolute)
	if err != nil {
		return sessionPrincipal{}, fmt.Errorf("authenticate session: touch: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return sessionPrincipal{}, errSessionUnauthorized
	}
	if err := tx.Commit(); err != nil {
		return sessionPrincipal{}, fmt.Errorf("authenticate session: commit: %w", err)
	}
	done = true
	p.actor.Kind = kind
	p.actor.SessionTokenHash = hash
	p.actor.ElevationToken = elevationToken
	p.expiresAt = newExpiry
	return p, nil
}

func (r *Runtime) deleteSession(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, sessionLookupHash(rawToken))
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

type userRow struct {
	id                                                                   int64
	discordID, username, avatar, guildNick, guildAvatarURL, lang         string
	isBanned, gamePublic                                                 bool
	bannedUntil, charityUntil                                            sql.NullInt64
	endpointLimit, rpmLimit, concurrencyLimit                            sql.NullInt64
	donation                                                             []byte
	manualLevel                                                          sql.NullInt64
	autoLevel                                                            int
	requests, uncached, cacheWrite, cacheRead, output, unknown, revision []byte
	createdAt, updatedAt                                                 int64
}

func readUserRow(ctx context.Context, tx *sql.Tx, userID int64) (userRow, error) {
	var u userRow
	var banned, public int
	err := tx.QueryRowContext(ctx, `SELECT id,discord_id,username,avatar,guild_nick,guild_avatar_url,is_banned,banned_until,charity_suspended_until,endpoint_limit,rpm_limit,concurrency_limit,game_profile_public,donation_credit_mag,level,auto_level,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,lang,created_at,updated_at FROM users WHERE id=? AND is_admin=0`, userID).Scan(&u.id, &u.discordID, &u.username, &u.avatar, &u.guildNick, &u.guildAvatarURL, &banned, &u.bannedUntil, &u.charityUntil, &u.endpointLimit, &u.rpmLimit, &u.concurrencyLimit, &public, &u.donation, &u.manualLevel, &u.autoLevel, &u.requests, &u.uncached, &u.cacheWrite, &u.cacheRead, &u.output, &u.unknown, &u.revision, &u.lang, &u.createdAt, &u.updatedAt)
	if err != nil {
		return userRow{}, err
	}
	u.isBanned = banned == 1
	u.gamePublic = public == 1
	return u, nil
}

func configTx(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var value string
	err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("missing site configuration")
	}
	if err != nil {
		return "", err
	}
	return value, nil
}
func configUintTx(ctx context.Context, tx *sql.Tx, key string, min, max int64) (int64, error) {
	raw, err := configTx(ctx, tx, key)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < min || v > max {
		return 0, fmt.Errorf("invalid site configuration")
	}
	return v, nil
}

func decodeU128(raw []byte) (db.U128, error) { return db.DecodeU128(raw) }
func incrementU128(raw []byte) ([]byte, error) {
	v, err := db.DecodeU128(raw)
	if err != nil {
		return nil, err
	}
	n := v.Big()
	n.Add(n, big.NewInt(1))
	out, err := db.U128FromBig(n)
	if err != nil {
		return nil, err
	}
	return db.EncodeU128(out), nil
}

func (r *Runtime) promoteLevel(ctx context.Context, tx *sql.Tx, u *userRow, now int64) error {
	if u.manualLevel.Valid {
		return nil
	}
	donation, err := decodeU128(u.donation)
	if err != nil {
		return err
	}
	eligible := 1
	for level := 2; level <= 4; level++ {
		threshold, err := configUintTx(ctx, tx, fmt.Sprintf("level_threshold_%d_milli", level), 0, db.MaxMoneyMilli)
		if err != nil {
			return err
		}
		if threshold > 0 && donation.Big().Cmp(big.NewInt(threshold)) >= 0 {
			eligible = level
		}
	}
	if eligible <= u.autoLevel {
		return nil
	}
	nextRevision, err := incrementU128(u.revision)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE users SET auto_level=?,revision=?,updated_at=? WHERE id=? AND revision=? AND is_admin=0`, eligible, nextRevision, now, u.id, u.revision)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errIdentityConflict
	}
	u.autoLevel = eligible
	u.revision = nextRevision
	u.updatedAt = now
	return nil
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	v := value
	return &v
}
func nullableDecimal(v sql.NullInt64) *string {
	if !v.Valid {
		return nil
	}
	s := strconv.FormatInt(v.Int64, 10)
	return &s
}
func nullableFuture(v sql.NullInt64, now int64) *int64 {
	if !v.Valid || v.Int64 <= now {
		return nil
	}
	n := v.Int64
	return &n
}

func formatMilliPoints(value *big.Int) string {
	if value == nil || value.Sign() == 0 {
		return "0"
	}
	negative := value.Sign() < 0
	abs := new(big.Int).Abs(new(big.Int).Set(value))
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(abs, big.NewInt(1000), rem)
	out := q.String()
	if rem.Sign() != 0 {
		fraction := fmt.Sprintf("%03d", rem.Int64())
		for len(fraction) > 0 && fraction[len(fraction)-1] == '0' {
			fraction = fraction[:len(fraction)-1]
		}
		out += "." + fraction
	}
	if negative {
		return "-" + out
	}
	return out
}

func (r *Runtime) userEnvelopeTx(ctx context.Context, tx *sql.Tx, userID int64, now int64) (UserEnvelope, error) {
	u, err := readUserRow(ctx, tx, userID)
	if err != nil {
		return UserEnvelope{}, err
	}
	if err := r.promoteLevel(ctx, tx, &u, now); err != nil {
		return UserEnvelope{}, err
	}
	wallet, err := ledger.UserAccount(ctx, tx, userID)
	if err != nil {
		return UserEnvelope{}, err
	}
	endpointDefault, err := configUintTx(ctx, tx, "default_endpoint_limit", 0, 10000)
	if err != nil {
		return UserEnvelope{}, err
	}
	rpmDefault, err := configUintTx(ctx, tx, "default_rpm_per_user", 1, 4096)
	if err != nil {
		return UserEnvelope{}, err
	}
	concurrencyDefault, err := configUintTx(ctx, tx, "default_per_endpoint_concurrency", 1, 100000)
	if err != nil {
		return UserEnvelope{}, err
	}
	values := make([]db.U128, 6)
	for i, raw := range [][]byte{u.requests, u.uncached, u.cacheWrite, u.cacheRead, u.output, u.unknown} {
		values[i], err = decodeU128(raw)
		if err != nil {
			return UserEnvelope{}, err
		}
	}
	prompt := new(big.Int).Add(values[1].Big(), values[2].Big())
	prompt.Add(prompt, values[3].Big())
	effective := u.autoLevel
	if u.manualLevel.Valid {
		effective = int(u.manualLevel.Int64)
	}
	if effective < 1 || effective > 5 {
		return UserEnvelope{}, fmt.Errorf("invalid effective level")
	}
	display, err := configTx(ctx, tx, fmt.Sprintf("level_display_name_%d", effective))
	if err != nil {
		return UserEnvelope{}, err
	}
	if display == "" {
		display = fmt.Sprintf("Lv. %d", effective)
	}
	donation, err := decodeU128(u.donation)
	if err != nil {
		return UserEnvelope{}, err
	}
	effectiveLimit := func(value sql.NullInt64, fallback int64) string {
		if value.Valid {
			return strconv.FormatInt(value.Int64, 10)
		}
		return strconv.FormatInt(fallback, 10)
	}
	return UserEnvelope{User: User{ID: strconv.FormatInt(u.id, 10), Username: u.username, Avatar: stringPtr(u.avatar), AvatarURL: discordAvatarURL(u.discordID, u.avatar), GuildNick: stringPtr(u.guildNick), GuildAvatarURL: stringPtr(u.guildAvatarURL), Lang: u.lang, IsBanned: u.isBanned && (!u.bannedUntil.Valid || u.bannedUntil.Int64 > now), BannedUntil: nullableFuture(u.bannedUntil, now), CharitySuspendedUntil: nullableFuture(u.charityUntil, now), EndpointLimit: nullableDecimal(u.endpointLimit), EffectiveEndpointLimit: effectiveLimit(u.endpointLimit, endpointDefault), RPMLimit: nullableDecimal(u.rpmLimit), EffectiveRPMLimit: effectiveLimit(u.rpmLimit, rpmDefault), ConcurrencyLimit: nullableDecimal(u.concurrencyLimit), EffectiveConcurrencyLimit: effectiveLimit(u.concurrencyLimit, concurrencyDefault), Balance: formatMilliPoints(wallet.Balance.Big()), DonationCredit: formatMilliPoints(donation.Big()), EffectiveLevel: effective, LevelDisplayName: display, GameProfilePublic: u.gamePublic, CreatedAt: u.createdAt, UpdatedAt: u.updatedAt, Usage: UsageSummary{TotalRequests: values[0].Decimal(), TotalUncachedInputTokens: values[1].Decimal(), TotalCacheWriteInputTokens: values[2].Decimal(), TotalCacheReadInputTokens: values[3].Decimal(), TotalOutputTokens: values[4].Decimal(), TotalPromptTokens: prompt.String(), TotalCompletionTokens: values[4].Decimal(), TotalUnknownUsageRequests: values[5].Decimal()}}}, nil
}

func (r *Runtime) readUserEnvelope(ctx context.Context, userID int64) (UserEnvelope, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return UserEnvelope{}, err
	}
	done := false
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()
	envelope, err := r.userEnvelopeTx(ctx, tx, userID, r.now().Unix())
	if err != nil {
		return UserEnvelope{}, err
	}
	if err := tx.Commit(); err != nil {
		return UserEnvelope{}, err
	}
	done = true
	return envelope, nil
}

func canonicalUserInsert(ctx context.Context, tx *sql.Tx, discordID, username, avatar, guildNick, guildAvatar string, isAdmin bool, now int64) (int64, error) {
	zero := db.EncodeU128(db.U128{})
	revision := db.U128{}
	revision[15] = 1
	var discord any = discordID
	if isAdmin {
		discord = nil
	}
	admin := 0
	if isAdmin {
		admin = 1
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO users(discord_id,username,avatar,guild_nick,guild_avatar_url,is_admin,is_banned,banned_reason,banned_until,auto_banned,charity_suspended_until,endpoint_limit,rpm_limit,concurrency_limit,game_profile_public,donation_credit_mag,level,auto_level,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,lang,created_at,updated_at) VALUES(?,?,?, ?,?, ?,0,'',NULL,0,NULL,NULL,NULL,NULL,0,?,NULL,1,?,?,?,?,?,?,?,'',?,?)`, discord, username, avatar, guildNick, guildAvatar, admin, zero, zero, zero, zero, zero, zero, zero, db.EncodeU128(revision), now, now)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid user id")
	}
	return id, nil
}

func (r *Runtime) findDiscordUser(ctx context.Context, discordID string) (int64, bool, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `SELECT id FROM users WHERE discord_id=? AND is_admin=0`, discordID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func hasRole(roles []string, want string) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

func (r *Runtime) refreshExistingUser(ctx context.Context, userID int64, identity DiscordIdentity, member *GuildMember) (string, int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	done := false
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()
	var revision []byte
	var banned int
	var bannedUntil sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT revision,is_banned,banned_until FROM users WHERE id=? AND is_admin=0`, userID).Scan(&revision, &banned, &bannedUntil)
	if err != nil {
		return "", 0, err
	}
	now := r.now().Unix()
	if banned == 1 && (!bannedUntil.Valid || bannedUntil.Int64 > now) {
		return "", 0, errSessionForbidden
	}
	next, err := incrementU128(revision)
	if err != nil {
		return "", 0, err
	}
	memberSet, guildNick, guildAvatar := 0, "", ""
	if member != nil {
		memberSet, guildNick, guildAvatar = 1, member.Nick, member.Avatar
	}
	result, err := tx.ExecContext(ctx, `UPDATE users SET username=?,avatar=?,guild_nick=CASE WHEN ?=1 THEN ? ELSE guild_nick END,guild_avatar_url=CASE WHEN ?=1 THEN ? ELSE guild_avatar_url END,revision=?,updated_at=? WHERE id=? AND revision=? AND is_admin=0`, identity.Username, identity.Avatar, memberSet, guildNick, memberSet, guildAvatar, next, now, userID, revision)
	if err != nil {
		return "", 0, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return "", 0, errIdentityConflict
	}
	generation, err := randomOpaque(32)
	if err != nil {
		return "", 0, err
	}
	token, expiry, err := r.createSessionTx(ctx, tx, userID, generation, now)
	if err != nil {
		return "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}
	done = true
	return token, expiry, nil
}

func (r *Runtime) registerUser(ctx context.Context, identity DiscordIdentity, member GuildMember, expectedGuild, expectedRole string) (int64, string, int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", 0, err
	}
	done := false
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()
	open, err := configTx(ctx, tx, "registration_open")
	if err != nil {
		return 0, "", 0, ErrProviderUnavailable
	}
	if open == "0" {
		return 0, "", 0, ErrRegistrationPaused
	}
	if open != "1" {
		return 0, "", 0, ErrProviderUnavailable
	}
	guild, err := configTx(ctx, tx, "discord_guild_id")
	if err != nil {
		return 0, "", 0, ErrProviderUnavailable
	}
	role, err := configTx(ctx, tx, "discord_role_id")
	if err != nil {
		return 0, "", 0, ErrProviderUnavailable
	}
	if guild == "" || role == "" || guild != expectedGuild || role != expectedRole {
		return 0, "", 0, ErrProviderUnavailable
	}
	if !hasRole(member.Roles, role) {
		return 0, "", 0, ErrGuildRoleMismatch
	}
	now := r.now().Unix()
	userID, err := canonicalUserInsert(ctx, tx, identity.ID, identity.Username, identity.Avatar, member.Nick, member.Avatar, false, now)
	if err != nil {
		_ = tx.Rollback()
		done = true
		if winner, ok, lookupErr := r.findDiscordUser(ctx, identity.ID); lookupErr == nil && ok {
			return winner, "", 0, errIdentityConflict
		}
		return 0, "", 0, err
	}
	if err := authz.InitializeRegistration(ctx, tx, userID, now, authz.LedgerWalletRegistrationHook{}); err != nil {
		return 0, "", 0, err
	}
	generation, err := randomOpaque(32)
	if err != nil {
		return 0, "", 0, err
	}
	token, expiry, err := r.createSessionTx(ctx, tx, userID, generation, now)
	if err != nil {
		return 0, "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, "", 0, err
	}
	done = true
	return userID, token, expiry, nil
}

func (r *Runtime) ensureAdminAndSession(ctx context.Context) (string, int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	done := false
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()
	var id int64
	var username string
	var discordID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id,username,discord_id FROM users WHERE is_admin=1`).Scan(&id, &username, &discordID)
	now := r.now().Unix()
	if errors.Is(err, sql.ErrNoRows) {
		id, err = canonicalUserInsert(ctx, tx, "", r.adminUsername, "", "", "", true, now)
		if err != nil {
			return "", 0, err
		}
	} else if err != nil {
		return "", 0, err
	} else if username != r.adminUsername || discordID.Valid {
		return "", 0, errIdentityConflict
	}
	if err == nil {
		var wallets, callerKeys int
		if scanErr := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM credit_accounts WHERE user_id=?),(SELECT COUNT(*) FROM caller_keys WHERE user_id=?)`, id, id).Scan(&wallets, &callerKeys); scanErr != nil || wallets != 0 || callerKeys != 0 {
			return "", 0, errIdentityConflict
		}
	}
	token, expiry, err := r.createSessionTx(ctx, tx, id, r.adminCredentialGeneration, now)
	if err != nil {
		return "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}
	done = true
	return token, expiry, nil
}
