package adminusers

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type ManagedAccountRow struct {
	*AdminUser
	AccountState string          `json:"account_state"`
	Deleted      *DeletedAccount `json:"deleted,omitempty"`
}

type DeletedAccount struct {
	RecordID             string                      `json:"record_id"`
	FormerUserID         *string                     `json:"former_user_id"`
	DiscordID            *string                     `json:"discord_id"`
	SnapshotVersion      int                         `json:"snapshot_version"`
	RegisteredAt         *int64                      `json:"registered_at"`
	DeletedAt            *int64                      `json:"deleted_at"`
	EffectiveLevel       *int                        `json:"effective_level"`
	Ban                  adminalerts.DeletionPenalty `json:"ban"`
	CharityPause         adminalerts.DeletionPenalty `json:"charity_pause"`
	Source               string                      `json:"source"`
	ActorUserID          *string                     `json:"actor_user_id"`
	BlacklistAction      string                      `json:"blacklist_action"`
	BlacklistReasonCodes []string                    `json:"blacklist_reason_codes"`
	GeneralBalance       *string                     `json:"general_balance"`
	GameBalance          *string                     `json:"game_balance"`
	DonationCredit       *string                     `json:"donation_credit"`
	SketchPaper          *string                     `json:"sketch_paper"`
	SketchBrush          *string                     `json:"sketch_brush"`
	AlertID              *string                     `json:"alert_id,omitempty"`
}

func knownAmount(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func readDeletedAccount(ctx context.Context, tx *sql.Tx, recordID int64, role managementRole) (DeletedAccount, error) {
	var body string
	var version int
	var former sql.NullInt64
	var discord sql.NullString
	var registered, deleted, level sql.NullInt64
	var source, action string
	var actor sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT snapshot_json,snapshot_version,former_user_id,discord_id,registered_at,deleted_at,effective_level,source,actor_user_id,blacklist_action FROM admin_account_deletions WHERE alert_id=?`, recordID).Scan(&body, &version, &former, &discord, &registered, &deleted, &level, &source, &actor, &action)
	if errors.Is(err, sql.ErrNoRows) {
		return DeletedAccount{}, ErrNotFound
	}
	if err != nil {
		return DeletedAccount{}, classifyDatabaseError("read deleted account", err)
	}
	var snapshot adminalerts.AccountDeletion
	if err = json.Unmarshal([]byte(body), &snapshot); err != nil {
		return DeletedAccount{}, ErrInvariant
	}
	item := DeletedAccount{RecordID: strconv.FormatInt(recordID, 10), SnapshotVersion: version, Source: source, BlacklistAction: action,
		Ban: snapshot.Ban, CharityPause: snapshot.CharityPause, BlacklistReasonCodes: snapshot.BlacklistReasonCodes,
		GeneralBalance: knownAmount(snapshot.GeneralBalance), GameBalance: knownAmount(snapshot.GameBalance), DonationCredit: knownAmount(snapshot.DonationCredit),
		SketchPaper: knownAmount(snapshot.SketchPaper), SketchBrush: knownAmount(snapshot.SketchBrush)}
	if former.Valid {
		value := strconv.FormatInt(former.Int64, 10)
		item.FormerUserID = &value
	}
	if discord.Valid {
		value := discord.String
		item.DiscordID = &value
	}
	if registered.Valid {
		value := registered.Int64
		item.RegisteredAt = &value
	}
	if deleted.Valid {
		value := deleted.Int64
		item.DeletedAt = &value
	}
	if level.Valid {
		value := int(level.Int64)
		item.EffectiveLevel = &value
	}
	if actor.Valid {
		value := strconv.FormatInt(actor.Int64, 10)
		item.ActorUserID = &value
	}
	if item.Ban.State == "" {
		item.Ban.State = "unknown"
	}
	if item.CharityPause.State == "" {
		item.CharityPause.State = "unknown"
	}
	if item.BlacklistReasonCodes == nil {
		item.BlacklistReasonCodes = []string{}
	}
	if role == roleAdmin {
		value := item.RecordID
		item.AlertID = &value
	}
	return item, nil
}

func (service *Service) getDeletedAccount(ctx context.Context, actorID, recordID int64, role managementRole) (DeletedAccount, error) {
	if ctx == nil || recordID <= 0 {
		return DeletedAccount{}, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, pageReadTimeout)
	defer cancel()
	tx, err := service.beginManagement(ctx, actorID, role, true)
	if err != nil {
		return DeletedAccount{}, err
	}
	defer tx.Rollback()
	item, err := readDeletedAccount(ctx, tx, recordID, role)
	if err != nil {
		return DeletedAccount{}, err
	}
	if err = commitTx(tx, "commit deleted account detail"); err != nil {
		return DeletedAccount{}, err
	}
	return item, nil
}

func activeManagedAccountFilter(query UserListQuery, config projectionConfig, now int64) (string, []any) {
	statement := `SELECT printf('%019d:A:%019d',id,id) AS sort_key,'active' AS kind,id AS record_id FROM users
WHERE is_admin=0 AND (?='' OR instr(username,?)>0 OR instr(COALESCE(discord_id,''),?)>0)
AND (?='' OR discord_id=?) AND (?=0 OR id=?)`
	args := []any{query.Q, query.Q, query.Q, query.DiscordID, query.DiscordID, query.UserID, query.UserID}
	if query.IsBanned != nil {
		want := 0
		if *query.IsBanned {
			want = 1
		}
		statement += ` AND (CASE WHEN is_banned=1 AND (banned_until IS NULL OR banned_until>?) THEN 1 ELSE 0 END)=?`
		args = append(args, now, want)
	}
	if query.Level != 0 {
		statement += ` AND COALESCE(level,MAX(auto_level`
		for level := 2; level <= 4; level++ {
			statement += `,CASE WHEN ? > 0 AND donation_credit_mag >= ? THEN ? ELSE 1 END`
			var threshold db.U128
			binary.BigEndian.PutUint64(threshold[8:], uint64(config.thresholds[level]))
			args = append(args, config.thresholds[level], db.EncodeU128(threshold), level)
		}
		statement += `))=?`
		args = append(args, query.Level)
	}
	return statement, args
}

func deletedManagedAccountFilter(query UserListQuery) (string, []any) {
	statement := `SELECT printf('%019d:D:%019d',COALESCE(former_user_id,alert_id),alert_id) AS sort_key,'deleted' AS kind,alert_id AS record_id
FROM admin_account_deletions WHERE (?='' OR instr(COALESCE(discord_id,''),?)>0)
AND (?='' OR discord_id=?) AND (?=0 OR former_user_id=?)`
	args := []any{query.Q, query.Q, query.DiscordID, query.DiscordID, query.UserID, query.UserID}
	if query.IsBanned != nil {
		want := 0
		if *query.IsBanned {
			want = 1
		}
		statement += ` AND ban_active=?`
		args = append(args, want)
	}
	if query.Level != 0 {
		statement += ` AND effective_level=?`
		args = append(args, query.Level)
	}
	return statement, args
}

func (service *Service) listManagedAccounts(ctx context.Context, actorID int64, role managementRole, query UserListQuery) (Page[ManagedAccountRow], error) {
	empty := Page[ManagedAccountRow]{Data: []ManagedAccountRow{}}
	state := query.AccountState
	if state == "" {
		state = "all"
	}
	limit := normalizePageLimit(query.Page, query.Cursor, query.Limit)
	if ctx == nil || limit == 0 || query.Level < 0 || query.Level > 6 || query.UserID < 0 || state != "all" && state != "active" && state != "deleted" || query.Q != "" && !validFilter(query.Q) || query.DiscordID != "" && !validDiscordID(query.DiscordID) {
		return empty, ErrInvalidRequest
	}
	if query.Page != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, pageReadTimeout)
		defer cancel()
	}
	now := service.now().Unix()
	if !validNow(now) {
		return empty, ErrUnavailable
	}
	owner := filterOwner("managed_accounts", role.actorKind(), strconv.FormatInt(actorID, 10), state, query.Q, query.DiscordID, strconv.Itoa(query.Level), strconv.FormatInt(query.UserID, 10), func() string {
		if query.IsBanned == nil {
			return "any"
		}
		return strconv.FormatBool(*query.IsBanned)
	}())
	after, err := service.decodeTextCursor(query.Cursor, "managed_accounts", owner, now)
	if err != nil {
		return empty, err
	}
	tx, err := service.beginManagement(ctx, actorID, role, query.Page != nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	config, err := readProjectionConfig(ctx, tx)
	if err != nil {
		return empty, err
	}
	parts := []string{}
	args := []any{}
	if state != "deleted" {
		clause, values := activeManagedAccountFilter(query, config, now)
		parts = append(parts, clause)
		args = append(args, values...)
	}
	if state != "active" {
		clause, values := deletedManagedAccountFilter(query)
		parts = append(parts, clause)
		args = append(args, values...)
	}
	selection := `SELECT sort_key,kind,record_id FROM (` + strings.Join(parts, ` UNION ALL `) + `) WHERE (?='' OR sort_key>?)`
	args = append(args, after, after)
	statement, args, meta, err := listPageQuery(ctx, tx, selection, ` ORDER BY sort_key ASC`, args, query.Page, limit)
	if err != nil {
		return empty, err
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return empty, classifyDatabaseError("list managed accounts", err)
	}
	type choice struct {
		sortKey, kind string
		id            int64
	}
	choices := make([]choice, 0, limit+1)
	for rows.Next() {
		var item choice
		if err = rows.Scan(&item.sortKey, &item.kind, &item.id); err != nil {
			rows.Close()
			return empty, classifyDatabaseError("scan managed account", err)
		}
		choices = append(choices, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return empty, classifyDatabaseError("iterate managed accounts", err)
	}
	if err = rows.Close(); err != nil {
		return empty, classifyDatabaseError("close managed accounts", err)
	}
	more := len(choices) > limit
	if more {
		choices = choices[:limit]
	}
	page := Page[ManagedAccountRow]{Data: make([]ManagedAccountRow, 0, len(choices)), Pagination: meta}
	for _, choice := range choices {
		switch choice.kind {
		case "active":
			row, err := readUserRow(ctx, tx, choice.id)
			if err != nil {
				return empty, err
			}
			user, err := projectUser(ctx, tx, row, config, now)
			if err != nil {
				return empty, err
			}
			page.Data = append(page.Data, ManagedAccountRow{AdminUser: &user, AccountState: "active"})
		case "deleted":
			item, err := readDeletedAccount(ctx, tx, choice.id, role)
			if err != nil {
				return empty, err
			}
			page.Data = append(page.Data, ManagedAccountRow{AccountState: "deleted", Deleted: &item})
		default:
			return empty, ErrInvariant
		}
	}
	if more && query.Page == nil {
		page.NextCursor, err = service.encodeCursor("managed_accounts", owner, now, db.CursorAtom{Kind: db.CursorText, Text: choices[len(choices)-1].sortKey})
		if err != nil {
			return empty, err
		}
	}
	if err = commitTx(tx, "commit managed accounts"); err != nil {
		return empty, err
	}
	return page, nil
}

type DeletionDuelAbort struct {
	ID           string `json:"id"`
	DiscordID    string `json:"discord_id"`
	GameKey      string `json:"game_key"`
	MatchID      string `json:"match_id"`
	FormerUserID string `json:"former_user_id"`
	Reason       string `json:"reason"`
	OccurredAt   int64  `json:"occurred_at"`
}

func (service *Service) listDeletionDuelAborts(ctx context.Context, adminID int64, discordID string, requested *pagination.Request) (Page[DeletionDuelAbort], error) {
	empty := Page[DeletionDuelAbort]{Data: []DeletionDuelAbort{}}
	if ctx == nil || !validDiscordID(discordID) || requested == nil || !requested.Valid() {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, pageReadTimeout)
	defer cancel()
	tx, err := service.beginManagement(ctx, adminID, roleAdmin, true)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	statement, args, meta, err := listPageQuery(ctx, tx, `SELECT id,discord_id,game_key,match_id,former_user_id,reason,occurred_at FROM self_deletion_duel_aborts WHERE discord_id=? AND expires_at>?`, ` ORDER BY occurred_at DESC,id DESC`, []any{discordID, service.now().Unix()}, requested, requested.Size)
	if err != nil {
		return empty, err
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return empty, classifyDatabaseError("list deletion duel aborts", err)
	}
	page := Page[DeletionDuelAbort]{Data: []DeletionDuelAbort{}, Pagination: meta}
	for rows.Next() {
		var item DeletionDuelAbort
		var id, former int64
		if err = rows.Scan(&id, &item.DiscordID, &item.GameKey, &item.MatchID, &former, &item.Reason, &item.OccurredAt); err != nil {
			rows.Close()
			return empty, classifyDatabaseError("scan deletion duel abort", err)
		}
		item.ID = strconv.FormatInt(id, 10)
		item.FormerUserID = strconv.FormatInt(former, 10)
		page.Data = append(page.Data, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return empty, classifyDatabaseError("iterate deletion duel aborts", err)
	}
	if err = rows.Close(); err != nil {
		return empty, classifyDatabaseError("close deletion duel aborts", err)
	}
	if err = commitTx(tx, "commit deletion duel aborts"); err != nil {
		return empty, err
	}
	return page, nil
}
