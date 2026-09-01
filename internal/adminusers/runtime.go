package adminusers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
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
	endpointDefault, rpmDefault, concurrencyDefault int64
	thresholds                                      [5]int64
	display                                         [6]string
}

type userRow struct {
	id                                                         int64
	discordID                                                  sql.NullString
	username, avatar, guildNick, guildAvatarURL, bannedReason  string
	isBanned, gamePublic                                       int
	bannedUntil, charityUntil                                  sql.NullInt64
	endpointLimit, rpmLimit, concurrencyLimit                  sql.NullInt64
	donation                                                   []byte
	manualLevel                                                sql.NullInt64
	autoLevel                                                  int
	requests, uncached, cacheWrite, cacheRead, output, unknown []byte
	revision                                                   []byte
	lang                                                       string
	createdAt, updatedAt                                       int64
}

func (service *Service) beginAuthorized(ctx context.Context, adminID int64) (*sql.Tx, error) {
	if service == nil || service.database == nil || ctx == nil || adminID <= 0 {
		return nil, ErrUnauthorized
	}
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, classifyDatabaseError("begin transaction", err)
	}
	if err := service.finalAuth.AuthorizeAdmin(ctx, tx, adminID); err != nil {
		_ = tx.Rollback()
		return nil, classifyAuthorizationError(err)
	}
	return tx, nil
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
	var config projectionConfig
	uints := []struct {
		key      string
		min, max int64
		dst      *int64
	}{
		{"default_endpoint_limit", 0, 10000, &config.endpointDefault},
		{"default_rpm_per_user", 1, db.MaxUserRPMLimit, &config.rpmDefault},
		{"default_per_endpoint_concurrency", 1, db.MaxUserConcurrencyLimit, &config.concurrencyDefault},
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
			return projectionConfig{}, classifyDatabaseError("read projection configuration", err)
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || strconv.FormatInt(value, 10) != raw || value < item.min || value > item.max {
			return projectionConfig{}, fmt.Errorf("%w: invalid projection configuration", ErrInvariant)
		}
		*item.dst = value
	}
	for level := 1; level <= 5; level++ {
		if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, fmt.Sprintf("level_display_name_%d", level)).Scan(&config.display[level]); err != nil {
			return projectionConfig{}, classifyDatabaseError("read level display configuration", err)
		}
		if config.display[level] == "" {
			config.display[level] = fmt.Sprintf("Lv. %d", level)
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
 total_unknown_usage_requests,revision,lang,created_at,updated_at
FROM users WHERE id=? AND is_admin=0`, userID).Scan(
		&row.id, &row.discordID, &row.username, &row.avatar, &row.guildNick, &row.guildAvatarURL,
		&row.isBanned, &row.bannedReason, &row.bannedUntil, &row.charityUntil, &row.endpointLimit,
		&row.rpmLimit, &row.concurrencyLimit, &row.gamePublic, &row.donation, &row.manualLevel,
		&row.autoLevel, &row.requests, &row.uncached, &row.cacheWrite, &row.cacheRead, &row.output,
		&row.unknown, &row.revision, &row.lang, &row.createdAt, &row.updatedAt,
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
	automatic := row.autoLevel
	if automatic < 1 || automatic > 4 {
		return AdminUser{}, fmt.Errorf("%w: invalid automatic level", ErrInvariant)
	}
	effective := automatic
	var manual *int
	if row.manualLevel.Valid {
		if row.manualLevel.Int64 < 1 || row.manualLevel.Int64 > 5 {
			return AdminUser{}, fmt.Errorf("%w: invalid manual level", ErrInvariant)
		}
		value := int(row.manualLevel.Int64)
		manual = &value
		effective = value
	}
	activeBan := row.isBanned == 1 && (!row.bannedUntil.Valid || row.bannedUntil.Int64 > now)
	bannedReason := ""
	var bannedUntil *int64
	if activeBan {
		bannedReason = row.bannedReason
		if row.bannedUntil.Valid {
			value := row.bannedUntil.Int64
			bannedUntil = &value
		}
	}
	return AdminUser{
		ID: strconv.FormatInt(row.id, 10), DiscordID: nullStringPointer(row.discordID),
		Username: row.username, AvatarURL: discordAvatarURL(row.discordID.String, row.avatar),
		GuildNick: stringPointer(row.guildNick), GuildAvatarURL: stringPointer(row.guildAvatarURL),
		IsAdmin: false, IsBanned: activeBan, BannedReason: bannedReason, BannedUntil: bannedUntil,
		CharitySuspendedUntil: futurePointer(row.charityUntil, now),
		EndpointLimit:         nullableIntString(row.endpointLimit), EffectiveEndpointLimit: effectiveLimit(row.endpointLimit, config.endpointDefault),
		RPMLimit: nullableIntString(row.rpmLimit), EffectiveRPMLimit: effectiveLimit(row.rpmLimit, config.rpmDefault),
		ConcurrencyLimit: nullableIntString(row.concurrencyLimit), EffectiveConcurrencyLimit: effectiveLimit(row.concurrencyLimit, config.concurrencyDefault),
		Lang: row.lang, Balance: formatMilliPoints(wallet.Balance.Big()), DonationCredit: formatMilliPoints(donation.Big()),
		Level:             AdminUserLevel{Manual: manual, Automatic: automatic, Effective: effective, DisplayName: config.display[effective]},
		GameProfilePublic: row.gamePublic == 1, Revision: revision.Decimal(), Usage: usage,
		CreatedAt: row.createdAt, UpdatedAt: row.updatedAt,
	}, nil
}

func (service *Service) ListUsers(ctx context.Context, adminID int64, query UserListQuery) (Page[AdminUser], error) {
	limit := normalizeLimit(query.Limit)
	if limit == 0 {
		return Page[AdminUser]{}, ErrInvalidRequest
	}
	now := service.now().Unix()
	if !validNow(now) {
		return Page[AdminUser]{}, ErrUnavailable
	}
	owner := usersCursorOwner(query)
	after, err := service.decodeUintCursor(query.Cursor, cursorScopeUsers, owner, now)
	if err != nil {
		return Page[AdminUser]{}, err
	}
	if query.Cursor != "" && (after == 0 || after > math.MaxInt64) {
		return Page[AdminUser]{}, ErrInvalidRequest
	}
	tx, err := service.beginAuthorized(ctx, adminID)
	if err != nil {
		return Page[AdminUser]{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	args := []any{query.Q, query.Q, query.Q, after}
	filter := ""
	if query.IsBanned != nil {
		filter = " AND (CASE WHEN is_banned=1 AND (banned_until IS NULL OR banned_until>?) THEN 1 ELSE 0 END)=?"
		want := 0
		if *query.IsBanned {
			want = 1
		}
		args = append(args, now, want)
	}
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, `
SELECT id FROM users
WHERE is_admin=0
 AND (?='' OR instr(username,?)>0 OR instr(COALESCE(discord_id,''),?)>0)
 AND id>?`+filter+`
ORDER BY id ASC LIMIT ?`, args...)
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
	if err := rows.Close(); err != nil {
		return Page[AdminUser]{}, classifyDatabaseError("close user list", err)
	}
	config, err := readProjectionConfig(ctx, tx)
	if err != nil {
		return Page[AdminUser]{}, err
	}
	hasMore := len(ids) > limit
	if hasMore {
		ids = ids[:limit]
	}
	page := Page[AdminUser]{Data: make([]AdminUser, 0, len(ids))}
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
	if userID <= 0 {
		return AdminUser{}, ErrNotFound
	}
	now := service.now().Unix()
	if !validNow(now) {
		return AdminUser{}, ErrUnavailable
	}
	tx, err := service.beginAuthorized(ctx, adminID)
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
	tx, err := service.beginAuthorized(ctx, adminID)
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
	limit := normalizeLimit(query.Limit)
	if limit == 0 {
		return Page[AdminUserUsage]{}, ErrInvalidRequest
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
	tx, err := service.beginAuthorized(ctx, adminID)
	if err != nil {
		return Page[AdminUserUsage]{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	rows, err := tx.QueryContext(ctx, `
SELECT id,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,
 total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests
FROM users WHERE is_admin=0 AND id>? ORDER BY id ASC LIMIT ?`, after, limit+1)
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
	if err := rows.Close(); err != nil {
		return Page[AdminUserUsage]{}, classifyDatabaseError("close user usage", err)
	}
	hasMore := len(data) > limit
	if hasMore {
		data = data[:limit]
		last, _ = strconv.ParseInt(data[len(data)-1].UserID, 10, 64)
	}
	page := Page[AdminUserUsage]{Data: data}
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
	limit := normalizeLimit(query.Limit)
	if limit == 0 {
		return ActivityPage{}, ErrInvalidRequest
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
	tx, err := service.beginAuthorized(ctx, adminID)
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
	rows, err := tx.QueryContext(ctx, `
SELECT day,product_active,api_requests,uncached_input_tokens,cache_write_input_tokens,
 cache_read_input_tokens,output_tokens,checkins,console_writes,game_active,game_rounds,distinct_product_users
FROM site_activity_daily WHERE day<? ORDER BY day DESC LIMIT ?`, upper, limit+1)
	if err != nil {
		return ActivityPage{}, classifyDatabaseError("list activity", err)
	}
	for rows.Next() {
		var day ActivityDay
		var product, game int
		raw := make([][]byte, 9)
		if err := rows.Scan(&day.Day, &product, &raw[0], &raw[1], &raw[2], &raw[3], &raw[4], &raw[5], &raw[6], &game, &raw[7], &raw[8]); err != nil {
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
	limit := normalizeLimit(query.Limit)
	if limit == 0 {
		return Page[EndpointOverview]{}, ErrInvalidRequest
	}
	now := service.now().Unix()
	if !validNow(now) {
		return Page[EndpointOverview]{}, ErrUnavailable
	}
	owner := filterOwner("endpoints", query.Q)
	after, err := service.decodeTextCursor(query.Cursor, cursorScopeEndpoints, owner, now)
	if err != nil {
		return Page[EndpointOverview]{}, err
	}
	tx, err := service.beginAuthorized(ctx, adminID)
	if err != nil {
		return Page[EndpointOverview]{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	rows, err := tx.QueryContext(ctx, `
SELECT e.base_url,COUNT(DISTINCT e.user_id),COUNT(*),
 COALESCE(SUM((SELECT COUNT(*) FROM endpoint_keys k WHERE k.endpoint_id=e.id)),0)
FROM endpoints e JOIN users u ON u.id=e.user_id AND u.is_admin=0
WHERE (?='' OR instr(e.base_url,?)>0) AND e.base_url>?
GROUP BY e.base_url ORDER BY e.base_url ASC LIMIT ?`, query.Q, query.Q, after, limit+1)
	if err != nil {
		return Page[EndpointOverview]{}, classifyDatabaseError("list endpoint overview", err)
	}
	page := Page[EndpointOverview]{Data: make([]EndpointOverview, 0, limit+1)}
	for rows.Next() {
		var item EndpointOverview
		var users, endpoints, keys int64
		if err := rows.Scan(&item.BaseURL, &users, &endpoints, &keys); err != nil {
			_ = rows.Close()
			return Page[EndpointOverview]{}, classifyDatabaseError("scan endpoint overview", err)
		}
		item.UserCount, item.EndpointCount, item.KeyCount = strconv.FormatInt(users, 10), strconv.FormatInt(endpoints, 10), strconv.FormatInt(keys, 10)
		item.Users = []EndpointOverviewUser{}
		page.Data = append(page.Data, item)
	}
	if err := rows.Close(); err != nil {
		return Page[EndpointOverview]{}, classifyDatabaseError("close endpoint overview", err)
	}
	hasMore := len(page.Data) > limit
	if hasMore {
		page.Data = page.Data[:limit]
	}
	for index := range page.Data {
		userCount, err := strconv.Atoi(page.Data[index].UserCount)
		if err != nil || userCount < 0 {
			return Page[EndpointOverview]{}, fmt.Errorf("%w: endpoint overview user count", ErrInvariant)
		}
		if userCount > 100 {
			return Page[EndpointOverview]{}, ErrPayloadTooLarge
		}
		detailRows, err := tx.QueryContext(ctx, `
SELECT e.user_id,COUNT(*),
 COALESCE(SUM((SELECT COUNT(*) FROM endpoint_keys k WHERE k.endpoint_id=e.id)),0),
 COALESCE(SUM(e.enabled),0)
FROM endpoints e JOIN users u ON u.id=e.user_id AND u.is_admin=0
WHERE e.base_url=? GROUP BY e.user_id ORDER BY e.user_id ASC`, page.Data[index].BaseURL)
		if err != nil {
			return Page[EndpointOverview]{}, classifyDatabaseError("list endpoint overview users", err)
		}
		for detailRows.Next() {
			var userID, endpoints, keys, enabled int64
			if err := detailRows.Scan(&userID, &endpoints, &keys, &enabled); err != nil {
				_ = detailRows.Close()
				return Page[EndpointOverview]{}, classifyDatabaseError("scan endpoint overview user", err)
			}
			page.Data[index].Users = append(page.Data[index].Users, EndpointOverviewUser{
				UserID: strconv.FormatInt(userID, 10), EndpointCount: strconv.FormatInt(endpoints, 10),
				KeyCount: strconv.FormatInt(keys, 10), EnabledCount: strconv.FormatInt(enabled, 10),
			})
		}
		if err := detailRows.Close(); err != nil {
			return Page[EndpointOverview]{}, classifyDatabaseError("close endpoint overview users", err)
		}
		if len(page.Data[index].Users) > 100 || strconv.Itoa(len(page.Data[index].Users)) != page.Data[index].UserCount {
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
	if userID <= 0 {
		return MutationResult[AdminUser]{}, ErrNotFound
	}
	now := service.now().Unix()
	if !validNow(now) {
		return MutationResult[AdminUser]{}, ErrUnavailable
	}
	tx, err := service.beginAuthorized(ctx, adminID)
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	decision, err := beginControlMutation(ctx, tx, adminID, control, now)
	if err != nil {
		return MutationResult[AdminUser]{}, err
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
	row, err := readUserRow(ctx, tx, userID)
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	if !equalU128Bytes(row.revision, input.ExpectedRevision) {
		return MutationResult[AdminUser]{}, ErrConflict
	}
	next, err := incrementU128(input.ExpectedRevision)
	if err != nil {
		return MutationResult[AdminUser]{}, ErrResourceLimit
	}
	result, err := tx.ExecContext(ctx, `
UPDATE users SET
 endpoint_limit=CASE WHEN ? THEN ? ELSE endpoint_limit END,
 rpm_limit=CASE WHEN ? THEN ? ELSE rpm_limit END,
 concurrency_limit=CASE WHEN ? THEN ? ELSE concurrency_limit END,
 lang=CASE WHEN ? THEN ? ELSE lang END,
 level=CASE WHEN ? THEN ? ELSE level END,
 revision=?,updated_at=?
WHERE id=? AND is_admin=0 AND revision=?`,
		input.EndpointLimitSet, nullableIntValue(input.EndpointLimit),
		input.RPMLimitSet, nullableIntValue(input.RPMLimit),
		input.ConcurrencySet, nullableIntValue(input.Concurrency),
		input.LangSet, input.Lang, input.LevelSet, nullableIntValueFromInt(input.Level),
		db.EncodeU128(next), now, userID, row.revision)
	if err != nil {
		return MutationResult[AdminUser]{}, classifyDatabaseError("update user profile", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return MutationResult[AdminUser]{}, ErrConflict
	}
	row, err = readUserRow(ctx, tx, userID)
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	config, err := readProjectionConfig(ctx, tx)
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	user, err := projectUser(ctx, tx, row, config, now)
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
	}
	return mutationResult, nil
}

func (service *Service) Economy(ctx context.Context, adminID, userID int64, control ControlMutation, input EconomyMutation) (MutationResult[AdminUser], error) {
	if userID <= 0 {
		return MutationResult[AdminUser]{}, ErrNotFound
	}
	now := service.now().Unix()
	if !validNow(now) {
		return MutationResult[AdminUser]{}, ErrUnavailable
	}
	tx, err := service.beginAuthorized(ctx, adminID)
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	decision, err := beginControlMutation(ctx, tx, adminID, control, now)
	if err != nil {
		return MutationResult[AdminUser]{}, err
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
	row, err := readUserRow(ctx, tx, userID)
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	if !equalU128Bytes(row.revision, input.ExpectedRevision) {
		return MutationResult[AdminUser]{}, ErrConflict
	}
	wallet, err := ledger.UserAccount(ctx, tx, userID)
	if err != nil {
		return MutationResult[AdminUser]{}, classifyLedgerError("read adjustment wallet", err)
	}
	external, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		return MutationResult[AdminUser]{}, classifyLedgerError("read external account", err)
	}
	delta := input.AmountMilli
	if input.Direction == "decrease" {
		delta = -delta
	}
	creditDelta, donationDelta := ledger.AmountFromMilli(0), ledger.AmountFromMilli(0)
	donationUserID := int64(0)
	if input.Target == "balance" {
		creditDelta = ledger.AmountFromMilli(delta)
	} else {
		donationDelta = ledger.AmountFromMilli(delta)
		donationUserID = userID
	}
	operationID, err := service.newID("op_")
	if err != nil || !db.ValidateOpaqueID(operationID, "op_") {
		return MutationResult[AdminUser]{}, ErrUnavailable
	}
	plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: operationID, ActorUserID: adminID, CreatedAt: now}, wallet.ID, external.ID, creditDelta, donationUserID, donationDelta, input.Reason)
	if err != nil {
		return MutationResult[AdminUser]{}, classifyLedgerError("build user adjustment", err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		return MutationResult[AdminUser]{}, classifyLedgerError("apply user adjustment", err)
	}
	next, err := incrementU128(input.ExpectedRevision)
	if err != nil {
		return MutationResult[AdminUser]{}, ErrResourceLimit
	}
	newAuto := row.autoLevel
	if input.Target == "donation_credit" {
		var donationRaw []byte
		if err := tx.QueryRowContext(ctx, `SELECT donation_credit_mag FROM users WHERE id=?`, userID).Scan(&donationRaw); err != nil {
			return MutationResult[AdminUser]{}, classifyDatabaseError("read adjusted donation credit", err)
		}
		donation, err := db.DecodeU128(donationRaw)
		if err != nil {
			return MutationResult[AdminUser]{}, fmt.Errorf("%w: decode adjusted donation credit", ErrInvariant)
		}
		config, err := readProjectionConfig(ctx, tx)
		if err != nil {
			return MutationResult[AdminUser]{}, err
		}
		for level := 2; level <= 4; level++ {
			if config.thresholds[level] > 0 && donation.Big().Cmp(big.NewInt(config.thresholds[level])) >= 0 && level > newAuto {
				newAuto = level
			}
		}
	}
	authorityChanged := newAuto != row.autoLevel
	var update sql.Result
	if input.Target == "donation_credit" {
		// The closed ledger primitive advances the user revision together with
		// donation_credit_mag. Reuse that CAS winner instead of advancing the
		// same mutation a second time.
		update, err = tx.ExecContext(ctx, `UPDATE users SET auto_level=?,updated_at=? WHERE id=? AND is_admin=0 AND revision=?`, newAuto, now, userID, db.EncodeU128(next))
	} else {
		update, err = tx.ExecContext(ctx, `UPDATE users SET auto_level=?,revision=?,updated_at=? WHERE id=? AND is_admin=0 AND revision=?`, newAuto, db.EncodeU128(next), now, userID, row.revision)
	}
	if err != nil {
		return MutationResult[AdminUser]{}, classifyDatabaseError("update adjusted user revision", err)
	}
	updated, err := update.RowsAffected()
	if err != nil || updated != 1 {
		return MutationResult[AdminUser]{}, ErrConflict
	}
	row, err = readUserRow(ctx, tx, userID)
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	config, err := readProjectionConfig(ctx, tx)
	if err != nil {
		return MutationResult[AdminUser]{}, err
	}
	user, err := projectUser(ctx, tx, row, config, now)
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
	return service.setBan(ctx, adminID, userID, control, input.ExpectedRevision, true, input.Reason, input.DurationSeconds)
}

func (service *Service) Unban(ctx context.Context, adminID, userID int64, control ControlMutation, expected db.U128) (MutationResult[struct{}], error) {
	return service.setBan(ctx, adminID, userID, control, expected, false, "", nil)
}

func (service *Service) setBan(ctx context.Context, adminID, userID int64, control ControlMutation, expected db.U128, banned bool, reason string, duration *int64) (MutationResult[struct{}], error) {
	if userID <= 0 {
		return MutationResult[struct{}]{}, ErrNotFound
	}
	now := service.now().Unix()
	if !validNow(now) {
		return MutationResult[struct{}]{}, ErrUnavailable
	}
	tx, err := service.beginAuthorized(ctx, adminID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	done := false
	defer rollbackUnlessDone(tx, &done)
	decision, err := beginControlMutation(ctx, tx, adminID, control, now)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if decision.Kind == idempotency.Replay {
		result := MutationResult[struct{}]{Status: decision.HTTPStatus, Body: append([]byte(nil), decision.ResponseBody...), Replayed: true}
		if err := commitTx(tx, "commit ban replay"); err != nil {
			return MutationResult[struct{}]{}, err
		}
		done = true
		return result, nil
	}
	row, err := readUserRow(ctx, tx, userID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if !equalU128Bytes(row.revision, expected) {
		return MutationResult[struct{}]{}, ErrConflict
	}
	next, err := incrementU128(expected)
	if err != nil {
		return MutationResult[struct{}]{}, ErrResourceLimit
	}
	var result sql.Result
	if banned {
		var until any
		if duration != nil {
			if *duration <= 0 || *duration > db.MaxBanDurationSeconds || now > maxUnixSecond-*duration {
				return MutationResult[struct{}]{}, ErrInvalidRequest
			}
			until = now + *duration
		}
		result, err = tx.ExecContext(ctx, `
UPDATE users SET is_banned=1,banned_reason=?,banned_until=?,auto_banned=0,revision=?,updated_at=?
WHERE id=? AND is_admin=0 AND revision=?`, reason, until, db.EncodeU128(next), now, userID, row.revision)
	} else {
		result, err = tx.ExecContext(ctx, `
UPDATE users SET is_banned=0,banned_reason='',banned_until=NULL,auto_banned=0,revision=?,updated_at=?
WHERE id=? AND is_admin=0 AND revision=?`, db.EncodeU128(next), now, userID, row.revision)
	}
	if err != nil {
		return MutationResult[struct{}]{}, classifyDatabaseError("update ban state", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return MutationResult[struct{}]{}, ErrConflict
	}
	if banned {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID); err != nil {
			return MutationResult[struct{}]{}, classifyDatabaseError("revoke user sessions", err)
		}
		keyUpdate, err := tx.ExecContext(ctx, `
UPDATE caller_keys SET generation=generation+1,key_hash=NULL,display_head='',display_tail='',key_created_at=NULL,updated_at=?
WHERE user_id=? AND generation<?`, now, userID, int64(math.MaxInt64))
		if err != nil {
			return MutationResult[struct{}]{}, classifyDatabaseError("revoke caller key", err)
		}
		keys, err := keyUpdate.RowsAffected()
		if err != nil || keys != 1 {
			return MutationResult[struct{}]{}, fmt.Errorf("%w: caller key generation", ErrInvariant)
		}
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

func beginControlMutation(ctx context.Context, tx *sql.Tx, adminID int64, control ControlMutation, now int64) (idempotency.Decision, error) {
	actor, err := idempotency.ActorScopeHash("admin", strconv.FormatInt(adminID, 10))
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
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

func usersCursorOwner(query UserListQuery) string {
	filter := "any"
	if query.IsBanned != nil {
		filter = strconv.FormatBool(*query.IsBanned)
	}
	return filterOwner("users", filter, query.Q)
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
