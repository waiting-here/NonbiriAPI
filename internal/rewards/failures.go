package rewards

import (
	"context"
	"database/sql"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"strconv"
)

type Failure struct {
	ID     int64  `json:"id"`
	UserID string `json:"user_id"`
	Reason string `json:"reason"`
	At     int64  `json:"at"`
}
type FailurePage struct {
	Items []Failure `json:"items"`
	Next  string    `json:"next_cursor"`
}

func (s *Service) Failures(ctx context.Context, actor int64, campaign, cursor string) (FailurePage, error) {
	out := FailurePage{Items: []Failure{}}
	if !db.ValidateOpaqueID(campaign, "rw_") {
		return out, ErrInvalid
	}
	before, err := cursorValue(cursor)
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, actor); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,user_id,failure,completed_at FROM reward_claims WHERE campaign_id=? AND status='failed' AND id<? ORDER BY id DESC LIMIT 51", campaign, before)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var f Failure
		var user sql.NullInt64
		if err = rows.Scan(&f.ID, &user, &f.Reason, &f.At); err != nil {
			return out, err
		}
		if user.Valid {
			f.UserID = strconv.FormatInt(user.Int64, 10)
		}
		out.Items = append(out.Items, f)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > 50 {
		out.Next = strconv.FormatInt(out.Items[49].ID, 10)
		out.Items = out.Items[:50]
	}
	return out, nil
}
