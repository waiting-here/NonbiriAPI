package adminusers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type BlacklistQuery struct {
	ActorKind   string
	ActorUserID int64
	DiscordID   string
	Q           string
	Cursor      string
	Limit       int
	Page        *pagination.Request
}

type BlacklistEvent struct {
	ID          string   `json:"id"`
	ActorKind   string   `json:"actor_kind"`
	ActorUserID *string  `json:"actor_user_id"`
	ReasonCodes []string `json:"reason_codes"`
	SafeNote    string   `json:"safe_note"`
	CreatedAt   int64    `json:"created_at"`
}

func (s *Service) listBlacklistFiltered(ctx context.Context, actorID int64, role managementRole, query BlacklistQuery) (Page[BlacklistEntry], error) {
	empty := Page[BlacklistEntry]{Data: []BlacklistEntry{}}
	limit := normalizePageLimit(query.Page, query.Cursor, query.Limit)
	if limit == 0 || query.ActorUserID < 0 ||
		query.ActorKind != "" && query.ActorKind != "admin" && query.ActorKind != "steward6" && query.ActorKind != "automatic" && query.ActorKind != "unknown" ||
		query.DiscordID != "" && !validDiscordID(query.DiscordID) || query.Q != "" && !validFilter(query.Q) {
		return empty, ErrInvalidRequest
	}
	if ctx == nil {
		return empty, ErrUnauthorized
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	now := s.now().Unix()
	owner := filterOwner("blacklist", role.actorKind(), strconv.FormatInt(actorID, 10), query.ActorKind, strconv.FormatInt(query.ActorUserID, 10), query.DiscordID, query.Q)
	afterTime, afterID, err := s.decodeBlacklistCursor(query.Cursor, owner, now)
	if err != nil {
		return empty, err
	}
	tx, err := s.beginManagement(ctx, actorID, role, query.Page != nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	selection := `SELECT b.discord_id,b.reason,b.created_at,u.id,COALESCE(o.first_actor_kind,'unknown'),o.first_actor_user_id
FROM discord_blacklist b LEFT JOIN discord_blacklist_origins o ON o.discord_id=b.discord_id
LEFT JOIN users u ON u.discord_id=b.discord_id
WHERE (?='' OR COALESCE(o.first_actor_kind,'unknown')=?)
AND (?=0 OR o.first_actor_user_id=?) AND (?='' OR b.discord_id=?)
AND (?='' OR instr(b.discord_id,?)>0 OR instr(b.reason,?)>0)
AND (?='' OR b.created_at<? OR (b.created_at=? AND b.discord_id<?))`
	args := []any{query.ActorKind, query.ActorKind, query.ActorUserID, query.ActorUserID, query.DiscordID, query.DiscordID, query.Q, query.Q, query.Q, afterID, afterTime, afterTime, afterID}
	if role == roleSteward {
		selection += ` AND (u.id IS NULL OR u.is_admin=0)`
	}
	statement, args, meta, err := listPageQuery(ctx, tx, selection, ` ORDER BY b.created_at DESC,b.discord_id DESC`, args, query.Page, limit)
	if err != nil {
		return empty, err
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return empty, classifyDatabaseError("list blacklist", err)
	}
	page := Page[BlacklistEntry]{Data: []BlacklistEntry{}, Pagination: meta}
	for rows.Next() {
		var item BlacklistEntry
		var userID, firstActor sql.NullInt64
		if err = rows.Scan(&item.DiscordID, &item.Reason, &item.CreatedAt, &userID, &item.FirstActorKind, &firstActor); err != nil {
			rows.Close()
			return empty, classifyDatabaseError("scan blacklist", err)
		}
		if userID.Valid {
			value := strconv.FormatInt(userID.Int64, 10)
			item.UserID = &value
		}
		if firstActor.Valid {
			value := strconv.FormatInt(firstActor.Int64, 10)
			item.FirstActorUserID = &value
		}
		page.Data = append(page.Data, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return empty, classifyDatabaseError("iterate blacklist", err)
	}
	if err = rows.Close(); err != nil {
		return empty, classifyDatabaseError("close blacklist", err)
	}
	more := len(page.Data) > limit
	if more {
		page.Data = page.Data[:limit]
	}
	if more && query.Page == nil {
		page.NextCursor, err = s.encodeBlacklistCursor(owner, now, page.Data[len(page.Data)-1])
		if err != nil {
			return empty, err
		}
	}
	if err = commitTx(tx, "commit blacklist list"); err != nil {
		return empty, err
	}
	return page, nil
}

const blacklistCursorScope = "admin_blacklist_created"

func (s *Service) decodeBlacklistCursor(token, owner string, now int64) (int64, string, error) {
	if token == "" {
		return 0, "", nil
	}
	key, err := s.deriveCursorKey()
	if err != nil {
		return 0, "", err
	}
	defer clear(key)
	cursor, err := db.DecodePaginationCursorWithDerivedKey(key, token, blacklistCursorScope, owner, uint64(now))
	if err != nil || len(cursor.Atoms) != 2 || cursor.Atoms[0].Kind != db.CursorUint || cursor.Atoms[0].Uint > uint64(maxUnixSecond) || cursor.Atoms[1].Kind != db.CursorText || !validDiscordID(cursor.Atoms[1].Text) {
		return 0, "", ErrInvalidRequest
	}
	return int64(cursor.Atoms[0].Uint), cursor.Atoms[1].Text, nil
}

func (s *Service) encodeBlacklistCursor(owner string, now int64, last BlacklistEntry) (*string, error) {
	if now < 0 || now > maxUnixSecond-cursorTTLSeconds {
		return nil, ErrUnavailable
	}
	key, err := s.deriveCursorKey()
	if err != nil {
		return nil, err
	}
	defer clear(key)
	token, err := db.EncodePaginationCursorWithDerivedKey(key, blacklistCursorScope, owner, uint64(now+cursorTTLSeconds), []db.CursorAtom{
		{Kind: db.CursorUint, Uint: uint64(last.CreatedAt)},
		{Kind: db.CursorText, Text: last.DiscordID},
	})
	if err != nil {
		return nil, ErrInvariant
	}
	return &token, nil
}

func (s *Service) listBlacklistEvents(ctx context.Context, actorID int64, role managementRole, discordID string, requested pagination.Request) (Page[BlacklistEvent], error) {
	empty := Page[BlacklistEvent]{Data: []BlacklistEvent{}}
	if ctx == nil || !validDiscordID(discordID) || !requested.Valid() {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, pageReadTimeout)
	defer cancel()
	tx, err := s.beginManagement(ctx, actorID, role, true)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	var targetAdmin int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(u.is_admin,0) FROM discord_blacklist b LEFT JOIN users u ON u.discord_id=b.discord_id WHERE b.discord_id=?`, discordID).Scan(&targetAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, classifyDatabaseError("read blacklist origin", err)
	}
	if role == roleSteward && targetAdmin != 0 {
		return empty, ErrForbidden
	}
	statement, args, meta, err := listPageQuery(ctx, tx, `SELECT id,actor_kind,actor_user_id,reason_codes_json,safe_note,created_at FROM discord_blacklist_events WHERE discord_id=?`, ` ORDER BY created_at ASC,id ASC`, []any{discordID}, &requested, requested.Size)
	if err != nil {
		return empty, err
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return empty, classifyDatabaseError("list blacklist events", err)
	}
	page := Page[BlacklistEvent]{Data: []BlacklistEvent{}, Pagination: meta}
	for rows.Next() {
		var item BlacklistEvent
		var id int64
		var actor sql.NullInt64
		var codes string
		if err = rows.Scan(&id, &item.ActorKind, &actor, &codes, &item.SafeNote, &item.CreatedAt); err != nil {
			rows.Close()
			return empty, classifyDatabaseError("scan blacklist event", err)
		}
		item.ID = strconv.FormatInt(id, 10)
		if actor.Valid {
			value := strconv.FormatInt(actor.Int64, 10)
			item.ActorUserID = &value
		}
		if err = json.Unmarshal([]byte(codes), &item.ReasonCodes); err != nil {
			rows.Close()
			return empty, ErrInvariant
		}
		page.Data = append(page.Data, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return empty, classifyDatabaseError("iterate blacklist events", err)
	}
	if err = rows.Close(); err != nil {
		return empty, classifyDatabaseError("close blacklist events", err)
	}
	if err = commitTx(tx, "commit blacklist events"); err != nil {
		return empty, err
	}
	return page, nil
}
