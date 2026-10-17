package inactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
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
	Data       []Audit              `json:"data"`
	NextCursor *string              `json:"next_cursor"`
	Pagination *pagination.Metadata `json:"pagination,omitempty"`
}

func (s *Service) Audits(ctx context.Context, before int64, limit int) (AuditsPage, error) {
	return s.audits(ctx, before, limit, nil)
}

func (s *Service) AuditsNumbered(ctx context.Context, page pagination.Request) (AuditsPage, error) {
	return s.audits(ctx, 0, page.Size, &page)
}

func (s *Service) audits(ctx context.Context, before int64, limit int, page *pagination.Request) (AuditsPage, error) {
	out := AuditsPage{Data: make([]Audit, 0)}
	if before < 0 || limit < 1 || limit > 100 || page != nil && !page.Valid() {
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
	query := `SELECT rowid,id,actor_user_id,action,policy_revision,details_json,created_at FROM inactivity_audits WHERE rowid<? ORDER BY rowid DESC LIMIT ?`
	args := []any{before, limit + 1}
	if page != nil {
		var total int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM inactivity_audits`).Scan(&total); err != nil {
			return out, err
		}
		metadata, offset, err := page.Window(total)
		if err != nil {
			return out, ErrInvalid
		}
		out.Pagination = &metadata
		query = `SELECT rowid,id,actor_user_id,action,policy_revision,details_json,created_at FROM inactivity_audits ORDER BY rowid DESC LIMIT ? OFFSET ?`
		args = []any{limit, offset}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
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
