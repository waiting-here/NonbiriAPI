package adminusers

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
	"strconv"
)

type AgentReadInput struct {
	UserID   string `json:"user_id,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Query    string `json:"query,omitempty"`
	Page     int    `json:"page,omitempty"`
	PageSize int    `json:"page_size,omitempty"`
}

func ParseAgentRead(raw json.RawMessage) (AgentReadInput, error) {
	var obj map[string]json.RawMessage
	var in AgentReadInput
	if strictjson.ValidateObject(raw) != nil || json.Unmarshal(raw, &obj) != nil || !allowedObject(obj, nil, []string{"user_id", "kind", "query", "page", "page_size"}) || json.Unmarshal(raw, &in) != nil {
		return in, ErrInvalidRequest
	}
	if in.Kind == "" {
		in.Kind = "profile"
	}
	if in.Page == 0 {
		in.Page = 1
	}
	if in.PageSize == 0 {
		in.PageSize = 20
	}
	if in.Page < 1 || in.Page > 1000000 || in.PageSize < 1 || in.PageSize > 100 || (in.Query != "" && !validFilter(in.Query)) {
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
	var count int64
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE is_admin=0 AND (username LIKE ? OR discord_id LIKE ?)", pattern, pattern).Scan(&count); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM users WHERE is_admin=0 AND (username LIKE ? OR discord_id LIKE ?) ORDER BY id DESC LIMIT ? OFFSET ?", pattern, pattern, in.PageSize, (in.Page-1)*in.PageSize)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]AdminUser, 0, len(ids))
	for _, id := range ids {
		row, err := readUserRow(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		user, err := projectUser(ctx, tx, row, cfg, s.now().Unix())
		if err != nil {
			return nil, err
		}
		out = append(out, user)
	}
	return json.Marshal(map[string]any{"data": out, "total": strconv.FormatInt(count, 10), "page": in.Page, "page_size": in.PageSize})
}
