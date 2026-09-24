package inactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
)

type Audit struct {
	ID             string          `json:"id"`
	ActorUserID    *string         `json:"actor_user_id"`
	Action         string          `json:"action"`
	PolicyRevision int64           `json:"policy_revision,string"`
	Details        json.RawMessage `json:"details"`
	CreatedAt      int64           `json:"created_at"`
}
type AuditsPage struct {
	Data       []Audit `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

func (s *Service) Audits(ctx context.Context, before int64, limit int) (AuditsPage, error) {
	out := AuditsPage{Data: make([]Audit, 0)}
	if before < 0 || limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	tx, _, err := s.beginAdmin(ctx, false)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if before == 0 {
		before = 9223372036854775807
	}
	rows, err := tx.QueryContext(ctx, `SELECT rowid,id,actor_user_id,action,policy_revision,details_json,created_at FROM inactivity_audits WHERE rowid<? ORDER BY rowid DESC LIMIT ?`, before, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var last int64
	for rows.Next() {
		var item Audit
		var row int64
		var user sql.NullInt64
		var details string
		if err = rows.Scan(&row, &item.ID, &user, &item.Action, &item.PolicyRevision, &details, &item.CreatedAt); err != nil {
			return out, err
		}
		if len(out.Data) == limit {
			cursor := position(last)
			out.NextCursor = &cursor
			break
		}
		if user.Valid {
			value := strconv.FormatInt(user.Int64, 10)
			item.ActorUserID = &value
		}
		item.Details = json.RawMessage(details)
		out.Data = append(out.Data, item)
		last = row
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
