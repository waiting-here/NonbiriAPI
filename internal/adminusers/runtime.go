package adminusers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

const (
	defaultPageLimit = 50
	maxPageLimit     = 100
	cursorTTLSeconds = int64(24 * 60 * 60)
	maxUnixSecond    = int64(253402300799)
)

const (
	cursorScopeUsers     = "admin_users"
	cursorScopeUsage     = "admin_usage_users"
	cursorScopeActivity  = "admin_activity"
	cursorScopeEndpoints = "admin_endpoint_overview"
)

type projectionConfig struct {
	endpointDefault, rpmDefault, lightRPMDefault, globalRPMDefault, concurrencyDefault, lightConcurrencyDefault int64
	thresholds                                                                                                  [5]int64
	display                                                                                                     [7]string
}

type userRow struct {
	id                                                         int64
	discordID                                                  sql.NullString
	username, avatar, guildNick, guildAvatarURL, bannedReason  string
	isBanned, gamePublic                                       int
	bannedUntil, charityUntil                                  sql.NullInt64
	endpointLimit, rpmLimit, concurrencyLimit                  sql.NullInt64
	lightRPMLimit, lightConcurrencyLimit, globalRPMLimit       sql.NullInt64
	donation                                                   []byte
	manualLevel                                                sql.NullInt64
	autoLevel                                                  int
	requests, uncached, cacheWrite, cacheRead, output, unknown []byte
	revision                                                   []byte
	lang                                                       string
	discordGatePolicy                                          string
	createdAt, updatedAt                                       int64
}

func (service *Service) beginAuthorized(ctx context.Context, adminID int64) (*sql.Tx, error) {
	return service.beginManagement(ctx, adminID, roleAdmin, false)
}

func commitTx(tx *sql.Tx, operation string) error {
	if err := tx.Commit(); err != nil {
		return classifyDatabaseError(operation, err)
	}
	return nil
}

func rollbackUnlessDone(tx *sql.Tx, done *bool) {
	if tx != nil && done != nil && !*done {
		_ = tx.Rollback()
	}
}

func readProjectionConfig(ctx context.Context, tx *sql.Tx) (projectionConfig, error) {
	config := projectionConfig{concurrencyDefault: db.DefaultUserConcurrencyLimit, lightConcurrencyDefault: db.DefaultUserLightConcurrencyLimit}
	uints := []struct {
		key      string
		min, max int64
		dst      *int64
	}{
		{"default_endpoint_limit", 0, 10000, &config.endpointDefault},
		{"default_concurrency_per_user", 1, db.MaxUserConcurrencyLimit, &config.concurrencyDefault},
		{"default_light_concurrency_per_user", 1, db.MaxUserConcurrencyLimit, &config.lightConcurrencyDefault},
		{"default_rpm_per_user", 1, db.MaxUserRPMLimit, &config.rpmDefault},
		{"default_light_rpm_per_user", 1, db.MaxUserRPMLimit, &config.lightRPMDefault},
		{"global_rpm_per_user", 1, db.MaxUserRPMLimit, &config.globalRPMDefault},
	}
	for level := 2; level <= 4; level++ {
		uints = append(uints, struct {
			key      string
			min, max int64
			dst      *int64
		}{fmt.Sprintf("level_threshold_%d_milli", level), 0, db.MaxMoneyMilli, &config.thresholds[level]})
	}
	for _, item := range uints {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, item.key).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) && (item.key == "default_concurrency_per_user" || item.key == "default_light_concurrency_per_user") {
				continue
			}
			return projectionConfig{}, classifyDatabaseError("read projection configuration", err)
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || strconv.FormatInt(value, 10) != raw || value < item.min || value > item.max {
			return projectionConfig{}, fmt.Errorf("%w: invalid projection configuration", ErrInvariant)
		}
		*item.dst = value
	}
	for level := 1; level <= 6; level++ {
		if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, fmt.Sprintf("level_display_name_%d", level)).Scan(&config.display[level]); err != nil {
			return projectionConfig{}, classifyDatabaseError("read level display configuration", err)
		}
		if config.display[level] == "" {
			config.display[level] = fmt.Sprintf("Lv. %d", level)
			if level == 5 {
				config.display[level] = "见习协管"
			}
		}
	}
	return config, nil
}

func readUserRow(ctx context.Context, tx *sql.Tx, userID int64) (userRow, error) {
	var row userRow
	err := tx.QueryRowContext(ctx, `
SELECT id,discord_id,username,avatar,guild_nick,guild_avatar_url,is_banned,banned_reason,banned_until,
 charity_suspended_until,endpoint_limit,rpm_limit,concurrency_limit,game_profile_public,
 donation_credit_mag,level,auto_level,total_requests,total_uncached_input_tokens,
 total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
 total_unknown_usage_requests,revision,lang,created_at,updated_at,discord_gate_policy,light_rpm_limit,light_concurrency_limit,global_rpm_limit
FROM users WHERE id=? AND is_admin=0`, userID).Scan(
		&row.id, &row.discordID, &row.username, &row.avatar, &row.guildNick, &row.guildAvatarURL,
		&row.isBanned, &row.bannedReason, &row.bannedUntil, &row.charityUntil, &row.endpointLimit,
		&row.rpmLimit, &row.concurrencyLimit, &row.gamePublic, &row.donation, &row.manualLevel,
		&row.autoLevel, &row.requests, &row.uncached, &row.cacheWrite, &row.cacheRead, &row.output,
		&row.unknown, &row.revision, &row.lang, &row.createdAt, &row.updatedAt, &row.discordGatePolicy, &row.lightRPMLimit, &row.lightConcurrencyLimit, &row.globalRPMLimit,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return userRow{}, ErrNotFound
	}
	if err != nil {
		return userRow{}, classifyDatabaseError("read user", err)
	}
	return row, nil
}

func decodeUsage(raw ...[]byte) (UsageSummary, error) {
	if len(raw) != 6 {
		return UsageSummary{}, ErrInvariant
	}
	values := make([]db.U128, len(raw))
	for index := range raw {
		value, err := db.DecodeU128(raw[index])
		if err != nil {
			return UsageSummary{}, fmt.Errorf("%w: decode usage", ErrInvariant)
		}
		values[index] = value
	}
	prompt := new(big.Int).Add(values[1].Big(), values[2].Big())
	prompt.Add(prompt, values[3].Big())
	return UsageSummary{
		TotalRequests: values[0].Decimal(), TotalUncachedInputTokens: values[1].Decimal(),
		TotalCacheWriteInputTokens: values[2].Decimal(), TotalCacheReadInputTokens: values[3].Decimal(),
		TotalOutputTokens: values[4].Decimal(), TotalPromptTokens: prompt.String(),
		TotalCompletionTokens: values[4].Decimal(), TotalUnknownUsageRequests: values[5].Decimal(),
	}, nil
}

func projectUser(ctx context.Context, tx *sql.Tx, row userRow, config projectionConfig, now int64) (AdminUser, error) {
	donation, err := db.DecodeU128(row.donation)
	if err != nil {
		return AdminUser{}, fmt.Errorf("%w: decode donation credit", ErrInvariant)
	}
	revision, err := db.DecodeU128(row.revision)
	if err != nil {
		return AdminUser{}, fmt.Errorf("%w: decode user revision", ErrInvariant)
	}
	usage, err := decodeUsage(row.requests, row.uncached, row.cacheWrite, row.cacheRead, row.output, row.unknown)
	if err != nil {
		return AdminUser{}, err
	}
	wallet, err := ledger.UserAccount(ctx, tx, row.id)
	if err != nil {
		return AdminUser{}, classifyLedgerError("read user wallet", err)
	}
	gameWallet, err := ledger.UserAssetAccount(ctx, tx, row.id, ledger.Game)
	if err != nil {
		return AdminUser{}, classifyLedgerError("read game wallet", err)
	}
	level, err := projectUserLevel(row, config)
	if err != nil {
		return AdminUser{}, err
	}
	activeBan, bannedUntil := projectUserBan(row, now)
	var automaticReason *observability.AutomaticReason
	if activeBan {
		var err error
		automaticReason, err = observability.ReadAutomaticReasonTx(ctx, tx, "user_ban", strconv.FormatInt(row.id, 10))
		if err != nil {
			return AdminUser{}, err
		}
	}
	bannedReason := ""
	if activeBan {
		bannedReason = row.bannedReason
	}
	return AdminUser{
		ID: strconv.FormatInt(row.id, 10), DiscordID: nullStringPointer(row.discordID),
		Username: row.username, AvatarURL: discordAvatarURL(row.discordID.String, row.avatar),
		GuildNick: stringPointer(row.guildNick), GuildAvatarURL: stringPointer(row.guildAvatarURL),
		DiscordGatePolicy: row.discordGatePolicy,
		IsAdmin:           false, IsBanned: activeBan, BannedReason: bannedReason, BannedUntil: bannedUntil, AutomaticReason: automaticReason,
		CharitySuspendedUntil: futurePointer(row.charityUntil, now),
		EndpointLimit:         nullableIntString(row.endpointLimit), EffectiveEndpointLimit: effectiveLimit(row.endpointLimit, config.endpointDefault),
		RPMLimit: nullableIntString(row.rpmLimit), EffectiveRPMLimit: effectiveLimit(row.rpmLimit, config.rpmDefault),
		ConcurrencyLimit: nullableIntString(row.concurrencyLimit), EffectiveConcurrencyLimit: effectiveLimit(row.concurrencyLimit, config.concurrencyDefault),
		LightRPMLimit: nullableIntString(row.lightRPMLimit), EffectiveLightRPMLimit: effectiveLimit(row.lightRPMLimit, config.lightRPMDefault),
		LightConcurrencyLimit: nullableIntString(row.lightConcurrencyLimit), EffectiveLightConcurrencyLimit: effectiveLimit(row.lightConcurrencyLimit, config.lightConcurrencyDefault),
		GlobalRPMLimit: nullableIntString(row.globalRPMLimit), EffectiveGlobalRPMLimit: effectiveLimit(row.globalRPMLimit, config.globalRPMDefault),
		Lang: row.lang, Balance: formatMilliPoints(wallet.Balance.Big()), GameBalance: formatMilliPoints(gameWallet.Balance.Big()), DonationCredit: formatMilliPoints(donation.Big()),
		Level:             level,
		GameProfilePublic: row.gamePublic == 1, Revision: revision.Decimal(), Usage: usage,
		CreatedAt: row.createdAt, UpdatedAt: row.updatedAt,
	}, nil
}

func projectUserLevel(row userRow, config projectionConfig) (AdminUserLevel, error) {
	donation, err := db.DecodeU128(row.donation)
	if err != nil {
		return AdminUserLevel{}, fmt.Errorf("%w: decode donation credit", ErrInvariant)
	}
	automatic := row.autoLevel
	if automatic < 1 || automatic > 4 {
		return AdminUserLevel{}, fmt.Errorf("%w: invalid automatic level", ErrInvariant)
	}
	if !row.manualLevel.Valid {
		automatic = authz.AutomaticLevel(automatic, donation, config.thresholds)
	}
	effective := automatic
	var manual *int
	if row.manualLevel.Valid {
		if row.manualLevel.Int64 < 1 || row.manualLevel.Int64 > 6 {
			return AdminUserLevel{}, fmt.Errorf("%w: invalid manual level", ErrInvariant)
		}
		value := int(row.manualLevel.Int64)
		manual, effective = &value, value
	}
	return AdminUserLevel{Manual: manual, Automatic: automatic, Effective: effective, DisplayName: config.display[effective]}, nil
}

func projectUserBan(row userRow, now int64) (bool, *int64) {
	active := row.isBanned == 1 && (!row.bannedUntil.Valid || row.bannedUntil.Int64 > now)
	if active && row.bannedUntil.Valid {
		value := row.bannedUntil.Int64
		return true, &value
	}
	return active, nil
}

func (service *Service) ListUsers(ctx context.Context, adminID int64, query UserListQuery) (Page[AdminUser], error) {
	return service.listUsers(ctx, adminID, roleAdmin, query)
}

func (service *Service) listUsers(ctx context.Context, adminID int64, role managementRole, query UserListQuery) (Page[AdminUser], error) {
	limit := normalizePageLimit(query.Page, query.Cursor, query.Limit)
	if limit == 0 || query.Level < 0 || query.Level > 6 {
		return Page[AdminUser]{}, ErrInvalidRequest
	}
	if query.Page != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, pageReadTimeout)
		defer cancel()
	}
	now := service.now().Unix()
	if !validNow(now) {
		return Page[AdminUser]{}, ErrUnavailable
	}
	owner := usersCursorOwner(query, role, adminID)
	after, err := service.decodeUintCursor(query.Cursor, cursorScopeUsers, owner, now)
	if err != nil {
		return Page[AdminUser]{}, err
	}
	if query.Cursor != "" && (after == 0 || after > math.MaxInt64) {
		return Page[AdminUser]{}, ErrInvalidRequest
	}
	tx, err := service.beginManagedUserRead(ctx, adminID, role, true)
	if err != nil {
		return Page[AdminUser]{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	config, err := readProjectionConfig(ctx, tx)
	if err != nil {
		return Page[AdminUser]{}, err
	}
	args := []any{query.Q, query.Q, query.Q, query.UserID, query.UserID, after}
	filter := ""
	if query.IsBanned != nil {
		filter = " AND (CASE WHEN is_banned=1 AND (banned_until IS NULL OR banned_until>?) THEN 1 ELSE 0 END)=?"
		want := 0
		if *query.IsBanned {
			want = 1
		}
		args = append(args, now, want)
	}
	if query.Level != 0 {
		// Fixed-width big-endian unsigned blobs compare in numeric order.
		filter += " AND COALESCE(level, MAX(auto_level"
		for level := 2; level <= 4; level++ {
			filter += ", CASE WHEN ? > 0 AND donation_credit_mag >= ? THEN ? ELSE 1 END"
			var threshold db.U128
			binary.BigEndian.PutUint64(threshold[8:], uint64(config.thresholds[level]))
			args = append(args, config.thresholds[level], db.EncodeU128(threshold), level)
		}
		filter += ")) = ?"
		args = append(args, query.Level)
	}
	selection, args, metadata, err := listPageQuery(ctx, tx, `
SELECT id FROM users
WHERE is_admin=0
 AND (?='' OR instr(username,?)>0 OR instr(COALESCE(discord_id,''),?)>0)
 AND (?=0 OR id=?)
 AND id>?`+filter, ` ORDER BY id ASC`, args, query.Page, limit)
	if err != nil {
		return Page[AdminUser]{}, err
	}
	rows, err := tx.QueryContext(ctx, selection, args...)
	if err != nil {
		return Page[AdminUser]{}, classifyDatabaseError("list users", err)
	}
	ids := make([]int64, 0, limit+1)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return Page[AdminUser]{}, classifyDatabaseError("scan user list", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Page[AdminUser]{}, classifyDatabaseError("iterate user list", err)
	}
	if err := rows.Close(); err != nil {
		return Page[AdminUser]{}, classifyDatabaseError("close user list", err)
	}
	hasMore := len(ids) > limit
	if hasMore {
		ids = ids[:limit]
	}
	page := Page[AdminUser]{Data: make([]AdminUser, 0, len(ids)), Pagination: metadata}
	for _, id := range ids {
		row, err := readUserRow(ctx, tx, id)
		if err != nil {
			return Page[AdminUser]{}, err
		}
		user, err := projectUser(ctx, tx, row, config, now)
		if err != nil {
			return Page[AdminUser]{}, err
		}
		page.Data = append(page.Data, user)
	}
	if hasMore && len(ids) > 0 {
		page.NextCursor, err = service.encodeCursor(cursorScopeUsers, owner, now, db.CursorAtom{Kind: db.CursorUint, Uint: uint64(ids[len(ids)-1])})
		if err != nil {
			return Page[AdminUser]{}, err
		}
	}
	if err := commitTx(tx, "commit user list"); err != nil {
		return Page[AdminUser]{}, err
	}
	done = true
	return page, nil
}

func (service *Service) GetUser(ctx context.Context, adminID, userID int64) (AdminUser, error) {
	return service.getUser(ctx, adminID, userID, roleAdmin)
}

func (service *Service) getUser(ctx context.Context, adminID, userID int64, role managementRole) (AdminUser, error) {
	if userID <= 0 {
		return AdminUser{}, ErrNotFound
	}
	now := service.now().Unix()
	if !validNow(now) {
		return AdminUser{}, ErrUnavailable
	}
	tx, err := service.beginManagedUserRead(ctx, adminID, role, true)
	if err != nil {
		return AdminUser{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	row, err := readUserRow(ctx, tx, userID)
	if err != nil {
		return AdminUser{}, err
	}
	config, err := readProjectionConfig(ctx, tx)
	if err != nil {
		return AdminUser{}, err
	}
	user, err := projectUser(ctx, tx, row, config, now)
	if err != nil {
		return AdminUser{}, err
	}
	if err := commitTx(tx, "commit user detail"); err != nil {
		return AdminUser{}, err
	}
	done = true
	return user, nil
}

func (service *Service) SiteUsage(ctx context.Context, adminID int64) (UsageSummary, error) {
	tx, err := service.beginManagement(ctx, adminID, roleAdmin, true)
	if err != nil {
		return UsageSummary{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	raw := make([][]byte, 6)
	err = tx.QueryRowContext(ctx, `
SELECT total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,
 total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests
FROM site_usage_totals WHERE id=1`).Scan(&raw[0], &raw[1], &raw[2], &raw[3], &raw[4], &raw[5])
	if err != nil {
		return UsageSummary{}, classifyDatabaseError("read site usage", err)
	}
	usage, err := decodeUsage(raw...)
	if err != nil {
		return UsageSummary{}, err
	}
	if err := commitTx(tx, "commit site usage"); err != nil {
		return UsageSummary{}, err
	}
	done = true
	return usage, nil
}

func (service *Service) UserUsage(ctx context.Context, adminID int64, query PageQuery) (Page[AdminUserUsage], error) {
	limit := normalizePageLimit(query.Page, query.Cursor, query.Limit)
	if limit == 0 {
		return Page[AdminUserUsage]{}, ErrInvalidRequest
	}
	if query.Page != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, pageReadTimeout)
		defer cancel()
	}
	now := service.now().Unix()
	if !validNow(now) {
		return Page[AdminUserUsage]{}, ErrUnavailable
	}
	after, err := service.decodeUintCursor(query.Cursor, cursorScopeUsage, "", now)
	if err != nil {
		return Page[AdminUserUsage]{}, err
	}
	if query.Cursor != "" && (after == 0 || after > math.MaxInt64) {
		return Page[AdminUserUsage]{}, ErrInvalidRequest
	}
	tx, err := service.beginListRead(ctx, adminID)
	if err != nil {
		return Page[AdminUserUsage]{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	selection, args, metadata, err := listPageQuery(ctx, tx, `
SELECT id,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,
 total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests
FROM users WHERE is_admin=0 AND id>?`, ` ORDER BY id ASC`, []any{after}, query.Page, limit)
	if err != nil {
		return Page[AdminUserUsage]{}, err
	}
	rows, err := tx.QueryContext(ctx, selection, args...)
	if err != nil {
		return Page[AdminUserUsage]{}, classifyDatabaseError("list user usage", err)
	}
	data := make([]AdminUserUsage, 0, limit+1)
	var last int64
	for rows.Next() {
		var id int64
		raw := make([][]byte, 6)
		if err := rows.Scan(&id, &raw[0], &raw[1], &raw[2], &raw[3], &raw[4], &raw[5]); err != nil {
			_ = rows.Close()
			return Page[AdminUserUsage]{}, classifyDatabaseError("scan user usage", err)
		}
		usage, err := decodeUsage(raw...)
		if err != nil {
			_ = rows.Close()
			return Page[AdminUserUsage]{}, err
		}
		data = append(data, AdminUserUsage{UserID: strconv.FormatInt(id, 10), Usage: usage})
		last = id
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Page[AdminUserUsage]{}, classifyDatabaseError("iterate user usage", err)
	}
	if err := rows.Close(); err != nil {
		return Page[AdminUserUsage]{}, classifyDatabaseError("close user usage", err)
	}
	hasMore := len(data) > limit
	if hasMore {
		data = data[:limit]
		last, _ = strconv.ParseInt(data[len(data)-1].UserID, 10, 64)
	}
	page := Page[AdminUserUsage]{Data: data, Pagination: metadata}
	if hasMore {
		page.NextCursor, err = service.encodeCursor(cursorScopeUsage, "", now, db.CursorAtom{Kind: db.CursorUint, Uint: uint64(last)})
		if err != nil {
			return Page[AdminUserUsage]{}, err
		}
	}
	if err := commitTx(tx, "commit user usage"); err != nil {
		return Page[AdminUserUsage]{}, err
	}
	done = true
	return page, nil
}

func (service *Service) Activity(ctx context.Context, adminID int64, query PageQuery) (ActivityPage, error) {
	limit := normalizePageLimit(query.Page, query.Cursor, query.Limit)
	if limit == 0 {
		return ActivityPage{}, ErrInvalidRequest
	}
	if query.Page != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, pageReadTimeout)
		defer cancel()
	}
	now := service.now().Unix()
	if !validNow(now) {
		return ActivityPage{}, ErrUnavailable
	}
	after, err := service.decodeUintCursor(query.Cursor, cursorScopeActivity, "", now)
	if err != nil {
		return ActivityPage{}, err
	}
	if query.Cursor != "" && after > uint64(maxUnixSecond) {
		return ActivityPage{}, ErrInvalidRequest
	}
	tx, err := service.beginListRead(ctx, adminID)
	if err != nil {
		return ActivityPage{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	var enabled string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='activities_enabled'`).Scan(&enabled); err != nil {
		return ActivityPage{}, classifyDatabaseError("read activity state", err)
	}
	if enabled != "0" && enabled != "1" {
		return ActivityPage{}, fmt.Errorf("%w: invalid activity state", ErrInvariant)
	}
	page := ActivityPage{Enabled: enabled == "1", Data: []ActivityDay{}}
	if !page.Enabled {
		if query.Page != nil {
			meta, _, err := query.Page.Window(0)
			if err != nil {
				return ActivityPage{}, ErrInvariant
			}
			page.Pagination = &meta
		}
		if err := commitTx(tx, "commit disabled activity"); err != nil {
			return ActivityPage{}, err
		}
		done = true
		return page, nil
	}
	upper := int64(math.MaxInt64)
	if query.Cursor != "" {
		if after > math.MaxInt64 {
			return ActivityPage{}, ErrInvalidRequest
		}
		upper = int64(after)
	}
	selection, args, metadata, err := listPageQuery(ctx, tx, `
SELECT day,product_active,api_requests,uncached_input_tokens,cache_write_input_tokens,
 cache_read_input_tokens,output_tokens,checkins,console_writes,game_active,game_rounds,distinct_product_users,game_checkins
FROM site_activity_daily WHERE day<?`, ` ORDER BY day DESC`, []any{upper}, query.Page, limit)
	if err != nil {
		return ActivityPage{}, err
	}
	page.Pagination = metadata
	rows, err := tx.QueryContext(ctx, selection, args...)
	if err != nil {
		return ActivityPage{}, classifyDatabaseError("list activity", err)
	}
	for rows.Next() {
		var day ActivityDay
		var product, game int
		raw := make([][]byte, 10)
		if err := rows.Scan(&day.Day, &product, &raw[0], &raw[1], &raw[2], &raw[3], &raw[4], &raw[5], &raw[6], &game, &raw[7], &raw[8], &raw[9]); err != nil {
			_ = rows.Close()
			return ActivityPage{}, classifyDatabaseError("scan activity", err)
		}
		values := make([]db.U128, len(raw))
		for index := range raw {
			values[index], err = db.DecodeU128(raw[index])
			if err != nil {
				_ = rows.Close()
				return ActivityPage{}, fmt.Errorf("%w: decode activity", ErrInvariant)
			}
		}
		day.GameCheckins = values[9].Decimal()
		day.ProductActive, day.GameActive = product == 1, game == 1
		day.APIRequests, day.UncachedInputTokens = values[0].Decimal(), values[1].Decimal()
		day.CacheWriteInputTokens, day.CacheReadInputTokens = values[2].Decimal(), values[3].Decimal()
		day.OutputTokens, day.Checkins, day.ConsoleWrites, day.GameRounds = values[4].Decimal(), values[5].Decimal(), values[6].Decimal(), values[7].Decimal()
		if values[8].Big().Cmp(big.NewInt(5)) >= 0 {
			value := values[8].Decimal()
			day.DistinctProductUsers = &value
		}
		page.Data = append(page.Data, day)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ActivityPage{}, classifyDatabaseError("iterate activity", err)
	}
	if err := rows.Close(); err != nil {
		return ActivityPage{}, classifyDatabaseError("close activity", err)
	}
	hasMore := len(page.Data) > limit
	if hasMore {
		page.Data = page.Data[:limit]
		last := page.Data[len(page.Data)-1].Day
		if last < 0 {
			return ActivityPage{}, fmt.Errorf("%w: invalid activity day", ErrInvariant)
		}
		page.NextCursor, err = service.encodeCursor(cursorScopeActivity, "", now, db.CursorAtom{Kind: db.CursorUint, Uint: uint64(last)})
		if err != nil {
			return ActivityPage{}, err
		}
	}
	if err := commitTx(tx, "commit activity"); err != nil {
		return ActivityPage{}, err
	}
	done = true
	return page, nil
}

func (service *Service) EndpointOverview(ctx context.Context, adminID int64, query EndpointOverviewQuery) (Page[EndpointOverview], error) {
	return service.endpointOverview(ctx, adminID, roleAdmin, query)
}
func (service *Service) endpointOverview(ctx context.Context, adminID int64, role managementRole, query EndpointOverviewQuery) (Page[EndpointOverview], error) {
	limit := normalizePageLimit(query.Page, query.Cursor, query.Limit)
	if limit == 0 || query.UserID < 0 || !validEndpointTagFilter(query.Tag) {
		return Page[EndpointOverview]{}, ErrInvalidRequest
	}
	if query.Page != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, pageReadTimeout)
		defer cancel()
	}
	now := service.now().Unix()
	if !validNow(now) {
		return Page[EndpointOverview]{}, ErrUnavailable
	}
	owner := filterOwner("endpoints", query.Q, query.Tag)
	if query.UserID != 0 {
		owner = filterOwner("endpoints", query.Q, query.Tag, strconv.FormatInt(query.UserID, 10))
	}
	after, err := service.decodeTextCursor(query.Cursor, cursorScopeEndpoints, owner, now)
	if err != nil {
		return Page[EndpointOverview]{}, err
	}
	tx, err := service.beginEndpointManagement(ctx, adminID, role, true, "endpoints.read")
	if err != nil {
		return Page[EndpointOverview]{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	selection, args, metadata, err := endpointOverviewPageQuery(ctx, tx, query.Q, after, query.Page, limit, query)
	if err != nil {
		return Page[EndpointOverview]{}, err
	}
	rows, err := tx.QueryContext(ctx, selection, args...)
	if err != nil {
		return Page[EndpointOverview]{}, classifyDatabaseError("list endpoint overview", err)
	}
	page := Page[EndpointOverview]{Data: make([]EndpointOverview, 0, limit+1), Pagination: metadata}
	for rows.Next() {
		var item EndpointOverview
		var users, endpoints, keys int64
		var tags, manualTags string
		if err := rows.Scan(&item.BaseURL, &users, &endpoints, &keys, &tags, &manualTags); err != nil {
			_ = rows.Close()
			return Page[EndpointOverview]{}, classifyDatabaseError("scan endpoint overview", err)
		}
		item.UserCount, item.EndpointCount, item.KeyCount = strconv.FormatInt(users, 10), strconv.FormatInt(endpoints, 10), strconv.FormatInt(keys, 10)
		item.Tags = []string{}
		if tags != "" {
			item.Tags = strings.Split(tags, ",")
		}
		item.ManualTags = []string{}
		if manualTags != "" {
			item.ManualTags = strings.Split(manualTags, ",")
		}
		item.Users = []EndpointOverviewUser{}
		page.Data = append(page.Data, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Page[EndpointOverview]{}, classifyDatabaseError("iterate endpoint overview", err)
	}
	if err := rows.Close(); err != nil {
		return Page[EndpointOverview]{}, classifyDatabaseError("close endpoint overview", err)
	}
	hasMore := len(page.Data) > limit
	if hasMore {
		page.Data = page.Data[:limit]
	}
	for index := range page.Data {
		page.Data[index].TagRules, err = readEndpointTagRules(ctx, tx, `SELECT r.domain,r.action FROM endpoint_tag_rule_matches r WHERE r.base_url=? ORDER BY length(r.domain),r.domain`, page.Data[index].BaseURL)
		if err != nil {
			return Page[EndpointOverview]{}, err
		}
		userCount, err := strconv.Atoi(page.Data[index].UserCount)
		if err != nil || userCount < 0 {
			return Page[EndpointOverview]{}, fmt.Errorf("%w: endpoint overview user count", ErrInvariant)
		}
		if query.Page == nil && userCount > 100 {
			return Page[EndpointOverview]{}, ErrPayloadTooLarge
		}
		previewLimit := 100
		if query.Page != nil {
			previewLimit = 3
		}
		page.Data[index].Users, err = readEndpointOverviewUsers(ctx, tx, endpointOverviewUsersSelection+` ORDER BY e.user_id ASC LIMIT ?`, []any{page.Data[index].BaseURL, previewLimit})
		if err != nil {
			return Page[EndpointOverview]{}, err
		}
		if len(page.Data[index].Users) != min(userCount, previewLimit) {
			return Page[EndpointOverview]{}, fmt.Errorf("%w: endpoint overview user cardinality", ErrInvariant)
		}
	}
	if hasMore {
		last := page.Data[len(page.Data)-1].BaseURL
		page.NextCursor, err = service.encodeCursor(cursorScopeEndpoints, owner, now, db.CursorAtom{Kind: db.CursorText, Text: last})
		if err != nil {
			return Page[EndpointOverview]{}, err
		}
	}
	if err := commitTx(tx, "commit endpoint overview"); err != nil {
		return Page[EndpointOverview]{}, err
	}
	done = true
	return page, nil
}

func (service *Service) Profile(ctx context.Context, adminID, userID int64, control ControlMutation, input ProfileMutation) (MutationResult[AdminUser], error) {
	return service.profile(ctx, adminID, userID, roleAdmin, control, input)
}

func (service *Service) profile(ctx context.Context, adminID, userID int64, role managementRole, control ControlMutation, input ProfileMutation) (MutationResult[AdminUser], error) {
	ctx = authz.WithStewardPermission(ctx, "user_fields")
	if userID <= 0 {
		return MutationResult[AdminUser]{}, ErrNotFound
	}
	now := service.now().Unix()
	if !validNow(now) {
		return MutationResult[AdminUser]{}, ErrUnavailable
	}
	if input.DiscordGatePolicySet {
		if role != roleAdmin {
			return MutationResult[AdminUser]{}, ErrForbidden
		}
		if !db.ValidDiscordGatePolicy(input.DiscordGatePolicy) {
			return MutationResult[AdminUser]{}, ErrInvalidRequest
		}
	}
	var lockedDiscord string
	if role == roleSteward && input.LevelSet && input.Level != nil && *input.Level == 6 {
		return MutationResult[AdminUser]{}, ErrForbidden
	}
	if input.LevelSet {
		var release func()
		var err error
		lockedDiscord, release, err = service.lockUserIdentity(ctx, adminID, userID, role, false)
		if err != nil {
			return MutationResult[AdminUser]{}, err
		}
		defer release()
	}
	tx, row, decision, err := service.beginUserMutationAccess(ctx, adminID, userID, role, control, now, false, input.limitsOnly())
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	if role == roleSteward {
		if err := authorizeProfilePermissions(ctx, tx, adminID, input); err != nil {
			return MutationResult[AdminUser]{}, err
		}
	}
	if input.LevelSet && decision.Kind != idempotency.Replay && (!row.discordID.Valid || row.discordID.String != lockedDiscord) {
		return MutationResult[AdminUser]{}, ErrConflict
	}
	if role == roleSteward && input.LevelSet && input.Level != nil && *input.Level == 6 {
		return MutationResult[AdminUser]{}, ErrForbidden
	}
	if decision.Kind == idempotency.Replay {
		result, err := replayJSON[AdminUser](decision)
		if err != nil {
			return MutationResult[AdminUser]{}, err
		}
		if err := commitTx(tx, "commit profile replay"); err != nil {
			return MutationResult[AdminUser]{}, err
		}
		done = true
		return result, nil
	}
	user, err := service.applyProfileTx(ctx, tx, row, userID, input, now)
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	mutationResult, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, user)
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	if err := commitTx(tx, "commit profile mutation"); err != nil {
		return MutationResult[AdminUser]{}, err
	}
	done = true
	if input.LevelSet {
		service.invalidator.InvalidateUserAuthority(userID)
	} else if input.RPMLimitSet || input.ConcurrencySet || input.LightRPMLimitSet || input.LightConcurrencySet || input.GlobalRPMLimitSet {
		if notifier, ok := service.invalidator.(interface{ UserLimitsChanged(int64) }); ok {
			notifier.UserLimitsChanged(userID)
		}
	}
	return mutationResult, nil
}

func (service *Service) Economy(ctx context.Context, adminID, userID int64, control ControlMutation, input EconomyMutation) (MutationResult[AdminUser], error) {
	return service.economy(ctx, adminID, userID, roleAdmin, control, input)
}

func (service *Service) economy(ctx context.Context, adminID, userID int64, role managementRole, control ControlMutation, input EconomyMutation) (MutationResult[AdminUser], error) {
	ctx = authz.WithStewardPermission(ctx, "user_fields")
	if userID <= 0 {
		return MutationResult[AdminUser]{}, ErrNotFound
	}
	now := service.now().Unix()
	if !validNow(now) {
		return MutationResult[AdminUser]{}, ErrUnavailable
	}
	tx, row, decision, err := service.beginUserMutation(ctx, adminID, userID, role, control, now, false)
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	if role == roleSteward && input.Target != "donation_credit" {
		if err := authorizeEconomyPermissions(ctx, tx, adminID, input); err != nil {
			return MutationResult[AdminUser]{}, err
		}
	}
	if role == roleSteward && input.Target == "donation_credit" {
		return MutationResult[AdminUser]{}, ErrForbidden
	}
	if decision.Kind == idempotency.Replay {
		result, err := replayJSON[AdminUser](decision)
		if err != nil {
			return MutationResult[AdminUser]{}, err
		}
		if err := commitTx(tx, "commit economy replay"); err != nil {
			return MutationResult[AdminUser]{}, err
		}
		done = true
		return result, nil
	}
	user, authorityChanged, err := service.applyEconomyTx(ctx, tx, row, adminID, userID, input, now)
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	mutationResult, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, user)
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	if err := commitTx(tx, "commit economy mutation"); err != nil {
		return MutationResult[AdminUser]{}, err
	}
	done = true
	if authorityChanged {
		service.invalidator.InvalidateUserAuthority(userID)
	}
	return mutationResult, nil
}

func (service *Service) Ban(ctx context.Context, adminID, userID int64, control ControlMutation, input BanMutation) (MutationResult[struct{}], error) {
	return service.setBan(ctx, adminID, userID, roleAdmin, control, input.ExpectedRevision, true, input.Reason, input.DurationSeconds)
}

func (service *Service) Unban(ctx context.Context, adminID, userID int64, control ControlMutation, expected db.U128) (MutationResult[struct{}], error) {
	return service.setBan(ctx, adminID, userID, roleAdmin, control, expected, false, "", nil)
}

func (service *Service) setBan(ctx context.Context, adminID, userID int64, role managementRole, control ControlMutation, expected db.U128, banned bool, reason string, duration *int64) (MutationResult[struct{}], error) {
	permission := "users.unban"
	if banned {
		permission = "users.ban"
	}
	ctx = authz.WithStewardPermission(ctx, permission)
	if userID <= 0 {
		return MutationResult[struct{}]{}, ErrNotFound
	}
	now := service.now().Unix()
	if !validNow(now) {
		return MutationResult[struct{}]{}, ErrUnavailable
	}
	lockedDiscord, release, err := service.lockUserIdentity(ctx, adminID, userID, role, banned)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	defer release()
	tx, row, decision, err := service.beginUserMutation(ctx, adminID, userID, role, control, now, banned)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	if role == roleSteward && banned {
		if err := authorizeBanPermissions(ctx, tx, adminID, duration); err != nil {
			return MutationResult[struct{}]{}, err
		}
	}
	if decision.Kind != idempotency.Replay && (!row.discordID.Valid || row.discordID.String != lockedDiscord) {
		return MutationResult[struct{}]{}, ErrConflict
	}
	if decision.Kind == idempotency.Replay {
		result := MutationResult[struct{}]{Status: decision.HTTPStatus, Body: append([]byte(nil), decision.ResponseBody...), Replayed: true}
		if err := commitTx(tx, "commit ban replay"); err != nil {
			return MutationResult[struct{}]{}, err
		}
		done = true
		return result, nil
	}
	var finalizers []func(bool)
	defer func() {
		for i := len(finalizers) - 1; i >= 0; i-- {
			finalizers[i](done)
		}
	}()
	if err := service.applyBanTx(ctx, tx, row, adminID, userID, expected, banned, reason, duration, now, &finalizers); err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := idempotency.Complete(ctx, tx, decision, http.StatusNoContent, []byte{}); err != nil {
		return MutationResult[struct{}]{}, classifyDatabaseError("complete ban mutation", err)
	}
	if err := commitTx(tx, "commit ban mutation"); err != nil {
		return MutationResult[struct{}]{}, err
	}
	done = true
	if banned {
		service.invalidator.InvalidateUserAuthority(userID)
	}
	return MutationResult[struct{}]{Status: http.StatusNoContent, Body: []byte{}}, nil
}

func beginControlMutation(ctx context.Context, tx *sql.Tx, adminID int64, role managementRole, control ControlMutation, now int64) (idempotency.Decision, error) {
	actor, err := idempotency.ActorScopeHash(role.actorKind(), strconv.FormatInt(adminID, 10))
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	managementlog.ChangedFields(ctx, control.CanonicalBody)
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: control.Method, Route: control.Route,
		PathResourceIDs: control.PathIDs, Body: control.CanonicalBody,
	})
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: control.IdempotencyKey,
		RequestHash: digest, DecisionNow: now,
	})
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		return idempotency.Decision{}, ErrConflict
	}
	if err != nil {
		return idempotency.Decision{}, classifyDatabaseError("accept control mutation", err)
	}
	return decision, nil
}

func replayJSON[T any](decision idempotency.Decision) (MutationResult[T], error) {
	var value T
	if len(decision.ResponseBody) > 0 {
		if err := json.Unmarshal(decision.ResponseBody, &value); err != nil {
			return MutationResult[T]{}, fmt.Errorf("%w: decode idempotency response", ErrInvariant)
		}
	}
	return MutationResult[T]{Value: value, Status: decision.HTTPStatus, Body: append([]byte(nil), decision.ResponseBody...), Replayed: true}, nil
}

func finishJSONMutation[T any](ctx context.Context, tx *sql.Tx, decision idempotency.Decision, status int, value T) (MutationResult[T], error) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > idempotency.MaxResponseBytes {
		return MutationResult[T]{}, fmt.Errorf("%w: encode mutation response", ErrInvariant)
	}
	if err := idempotency.Complete(ctx, tx, decision, status, body); err != nil {
		return MutationResult[T]{}, classifyDatabaseError("complete control mutation", err)
	}
	return MutationResult[T]{Value: value, Status: status, Body: body}, nil
}

func (service *Service) encodeCursor(scope, owner string, now int64, atom db.CursorAtom) (*string, error) {
	if now < 0 || now > maxUnixSecond-cursorTTLSeconds {
		return nil, ErrUnavailable
	}
	key, err := service.deriveCursorKey()
	if err != nil {
		return nil, err
	}
	defer clear(key)
	token, err := db.EncodePaginationCursorWithDerivedKey(key, scope, owner, uint64(now+cursorTTLSeconds), []db.CursorAtom{atom})
	if err != nil {
		return nil, fmt.Errorf("%w: encode cursor", ErrInvariant)
	}
	return &token, nil
}

func (service *Service) decodeUintCursor(token, scope, owner string, now int64) (uint64, error) {
	if token == "" {
		return 0, nil
	}
	key, err := service.deriveCursorKey()
	if err != nil {
		return 0, err
	}
	defer clear(key)
	cursor, err := db.DecodePaginationCursorWithDerivedKey(key, token, scope, owner, uint64(now))
	if err != nil || len(cursor.Atoms) != 1 || cursor.Atoms[0].Kind != db.CursorUint {
		return 0, ErrInvalidRequest
	}
	return cursor.Atoms[0].Uint, nil
}

func (service *Service) decodeTextCursor(token, scope, owner string, now int64) (string, error) {
	if token == "" {
		return "", nil
	}
	key, err := service.deriveCursorKey()
	if err != nil {
		return "", err
	}
	defer clear(key)
	cursor, err := db.DecodePaginationCursorWithDerivedKey(key, token, scope, owner, uint64(now))
	if err != nil || len(cursor.Atoms) != 1 || cursor.Atoms[0].Kind != db.CursorText || cursor.Atoms[0].Text == "" {
		return "", ErrInvalidRequest
	}
	return cursor.Atoms[0].Text, nil
}

func (service *Service) deriveCursorKey() ([]byte, error) {
	if service == nil || nilDependency(service.cursorKeys) {
		return nil, ErrUnavailable
	}
	key, err := service.cursorKeys.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
	if err != nil || len(key) != 32 {
		clear(key)
		return nil, ErrUnavailable
	}
	return key, nil
}

func filterOwner(parts ...string) string {
	hash := sha256.New()
	for _, part := range append([]string{"NonbiriAPI/adminusers-cursor-owner/v1"}, parts...) {
		_, _ = hash.Write([]byte(strconv.Itoa(len(part))))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part))
	}
	return base64.RawURLEncoding.EncodeToString(hash.Sum(nil))
}

func normalizeLimit(limit int) int {
	if limit == 0 {
		return defaultPageLimit
	}
	if limit < 1 || limit > maxPageLimit {
		return 0
	}
	return limit
}

func validNow(now int64) bool { return now >= 0 && now <= maxUnixSecond }

func incrementU128(value db.U128) (db.U128, error) {
	next := value.Big()
	next.Add(next, big.NewInt(1))
	return db.U128FromBig(next)
}

func equalU128Bytes(raw []byte, expected db.U128) bool {
	value, err := db.DecodeU128(raw)
	return err == nil && value == expected
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	out := value.String
	return &out
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	out := value
	return &out
}

func futurePointer(value sql.NullInt64, now int64) *int64 {
	if !value.Valid || value.Int64 <= now {
		return nil
	}
	out := value.Int64
	return &out
}

func nullableIntString(value sql.NullInt64) *string {
	if !value.Valid {
		return nil
	}
	out := strconv.FormatInt(value.Int64, 10)
	return &out
}

func effectiveLimit(value sql.NullInt64, fallback int64) string {
	if value.Valid {
		return strconv.FormatInt(value.Int64, 10)
	}
	return strconv.FormatInt(fallback, 10)
}

func nullableIntValue(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableIntValueFromInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func discordAvatarURL(id, hash string) *string {
	if id == "" || hash == "" {
		return nil
	}
	extension := ".png"
	if strings.HasPrefix(hash, "a_") {
		extension = ".gif"
	}
	value := "https://cdn.discordapp.com/avatars/" + id + "/" + hash + extension + "?size=64"
	return &value
}

func formatMilliPoints(value *big.Int) string {
	if value == nil || value.Sign() == 0 {
		return "0"
	}
	negative := value.Sign() < 0
	abs := new(big.Int).Abs(new(big.Int).Set(value))
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(abs, big.NewInt(1000), fraction)
	formatted := whole.String()
	if fraction.Sign() != 0 {
		fractionText := strconv.FormatInt(fraction.Int64()+1000, 10)[1:]
		formatted += "." + strings.TrimRight(fractionText, "0")
	}
	if negative {
		return "-" + formatted
	}
	return formatted
}
