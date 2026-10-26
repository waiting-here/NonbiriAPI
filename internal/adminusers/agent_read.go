package adminusers

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
	"strconv"
)

type AgentReadInput struct {
	UserID string `json:"user_id,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Query  string `json:"query,omitempty"`
}

type agentUserSummary struct {
	ID                    string         `json:"id"`
	Username              string         `json:"username"`
	DiscordID             *string        `json:"discord_id"`
	GuildNick             *string        `json:"guild_nick"`
	Level                 AdminUserLevel `json:"level"`
	IsBanned              bool           `json:"is_banned"`
	BannedUntil           *int64         `json:"banned_until"`
	CharitySuspendedUntil *int64         `json:"charity_suspended_until"`
	Revision              string         `json:"revision"`
}

func ParseAgentRead(raw json.RawMessage) (AgentReadInput, error) {
	var obj map[string]json.RawMessage
	var in AgentReadInput
	if strictjson.ValidateObject(raw) != nil || json.Unmarshal(raw, &obj) != nil || !allowedObject(obj, nil, []string{"user_id", "kind", "query"}) || json.Unmarshal(raw, &in) != nil {
		return in, ErrInvalidRequest
	}
	if in.Kind == "" {
		in.Kind = "profile"
	}
	if in.Query != "" && !validFilter(in.Query) {
		return in, ErrInvalidRequest
	}
	if in.Kind != "profile" && in.Kind != "caller_key" && in.Kind != "permissions" && in.Kind != "mfa" {
		return in, ErrInvalidRequest
	}
	if in.UserID != "" {
		id, err := strconv.ParseInt(in.UserID, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != in.UserID {
			return in, ErrInvalidRequest
		}
	} else if in.Kind != "profile" {
		return in, ErrInvalidRequest
	}
	return in, nil
}
func (s *Service) AgentRead(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, in AgentReadInput, authorize agentplan.Authorize) (json.RawMessage, error) {
	role, err := agentRole(actor)
	if err != nil || authorize == nil {
		return nil, ErrForbidden
	}
	permission := "users.read"
	if in.Kind == "caller_key" {
		permission = "users.caller_keys.read"
	} else if in.Kind == "permissions" {
		permission = "delegate"
	}
	if err = authorize(ctx, tx, actor, permission); err != nil {
		return nil, err
	}
	ctx = authz.WithStewardPermission(ctx, permission)
	trainee, err := s.authorizeManagementTx(ctx, tx, actor.ID, role, true)
	if err != nil {
		return nil, err
	}
	id, _ := strconv.ParseInt(in.UserID, 10, 64)
	if in.Kind == "caller_key" {
		if err = authorizeCallerKeyTargetTx(ctx, tx, actor.ID, id, role, trainee); err != nil {
			return nil, err
		}
		value, _, err := readManagedCallerKey(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	if in.Kind == "permissions" {
		row, err := readUserRow(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if actor.ID == id || role == roleSteward && trainee || !row.manualLevel.Valid || row.manualLevel.Int64 != 5 {
			return nil, ErrForbidden
		}
		value, err := authz.ReadStewardPermissions(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	cfg, err := readProjectionConfig(ctx, tx)
	if err != nil {
		return nil, err
	}
	if id > 0 {
		row, err := readUserRow(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		value, err := projectUser(ctx, tx, row, cfg, s.now().Unix())
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	pattern := "%" + in.Query + "%"
	rows, err := tx.QueryContext(ctx, `SELECT id,username,discord_id,guild_nick,level,auto_level,donation_credit_mag,is_banned,banned_until,charity_suspended_until,revision
FROM users WHERE is_admin=0 AND (username LIKE ? OR discord_id LIKE ?) ORDER BY id DESC`, pattern, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentUserSummary{}
	var budget agentplan.ReadBudget
	now := s.now().Unix()
	for rows.Next() {
		var row userRow
		if err = rows.Scan(&row.id, &row.username, &row.discordID, &row.guildNick, &row.manualLevel, &row.autoLevel, &row.donation, &row.isBanned, &row.bannedUntil, &row.charityUntil, &row.revision); err != nil {
			return nil, err
		}
		if err = agentplan.CheckReadCount(len(out) + 1); err != nil {
			return nil, err
		}
		level, err := projectUserLevel(row, cfg)
		if err != nil {
			return nil, err
		}
		revision, err := db.DecodeU128(row.revision)
		if err != nil {
			return nil, ErrInvariant
		}
		banned, until := projectUserBan(row, now)
		item := agentUserSummary{ID: strconv.FormatInt(row.id, 10), Username: row.username, DiscordID: nullStringPointer(row.discordID), GuildNick: stringPointer(row.guildNick), Level: level, IsBanned: banned, BannedUntil: until, CharitySuspendedUntil: futurePointer(row.charityUntil, now), Revision: revision.Decimal()}
		if err = budget.Add(item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return agentplan.MarshalRead(map[string]any{"data": out, "total": strconv.Itoa(len(out))})
}
