package inactivity

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"strconv"
)

func (s *Service) UserStatus(ctx context.Context) (Status, error) {
	actor, ok := auth.ActorFromContext(ctx)
	if !ok || actor.Kind != authz.ActorUserSession {
		return Status{}, ErrForbidden
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Status{}, err
	}
	defer tx.Rollback()
	if _, err = authz.New(authz.Options{Now: s.now}).Authorize(ctx, tx, actor, authz.Requirement{Role: authz.RoleUser}); err != nil {
		return Status{}, ErrForbidden
	}
	c, err := readPolicy(ctx, tx)
	if err != nil {
		return Status{}, err
	}
	a, err := readAccount(ctx, tx, actor.UserID)
	if err != nil {
		return Status{}, err
	}
	result := status(c, a, s.now().Unix())
	return result, tx.Commit()
}

type PreviewInput struct {
	Update
	Cursor string `json:"cursor"`
	Limit  int    `json:"page_size"`
}
type PreviewEntry struct {
	UserID       string `json:"user_id"`
	ExemptReason string `json:"exempt_reason"`
	Action       string `json:"action"`
	ScheduledAt  *int64 `json:"scheduled_at"`
	GeneralMilli string `json:"general_milli"`
	GameMilli    string `json:"game_milli"`
}
type PreviewPage struct {
	Data          []PreviewEntry `json:"data"`
	NextCursor    *string        `json:"next_cursor"`
	AsOf          int64          `json:"as_of"`
	Configuration Configuration  `json:"configuration"`
}

func decodePosition(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	if len(value) > 32 {
		return 0, ErrInvalid
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, ErrInvalid
	}
	id, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != string(b) {
		return 0, ErrInvalid
	}
	return id, nil
}
func position(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}
func (s *Service) Preview(ctx context.Context, input PreviewInput) (PreviewPage, error) {
	out := PreviewPage{Data: make([]PreviewEntry, 0), AsOf: s.now().Unix()}
	if input.Limit == 0 {
		input.Limit = 100
	}
	if input.Limit < 1 || input.Limit > 100 || input.ExpectedRevision < 1 || !validTime(out.AsOf) || Validate(input.Policy) != nil {
		return out, ErrInvalid
	}
	after, err := decodePosition(input.Cursor)
	if err != nil {
		return out, err
	}
	tx, actor, err := s.beginAdmin(ctx, true)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	old, err := readPolicy(ctx, tx)
	if err != nil {
		return out, err
	}
	if old.Revision != input.ExpectedRevision {
		return out, ErrConflict
	}
	c := old
	c.Policy = input.Policy
	c.DecayGraceUntil, c.ProtectionGraceUntil = grace(old, input.Policy, out.AsOf)
	out.Configuration = c
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM user_activity_state WHERE user_id>? ORDER BY user_id LIMIT ?`, after, input.Limit+1)
	if err != nil {
		return out, err
	}
	ids := make([]int64, 0, input.Limit+1)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	if len(ids) > input.Limit {
		ids = ids[:input.Limit]
		cursor := position(ids[len(ids)-1])
		out.NextCursor = &cursor
	}
	for _, id := range ids {
		a, err := readAccount(ctx, tx, id)
		if err != nil {
			return out, err
		}
		view := status(c, a, out.AsOf)
		item := PreviewEntry{UserID: strconv.FormatInt(id, 10), ExemptReason: view.ExemptReason, Action: "none", GeneralMilli: "0", GameMilli: "0"}
		item.ScheduledAt = earliest(view.DecayAt, view.ProtectionAt)
		if view.ProtectionAt != nil && *view.ProtectionAt <= out.AsOf {
			item.ScheduledAt = view.ProtectionAt
		}
		if item.ScheduledAt != nil {
			if view.ProtectionAt != nil && *item.ScheduledAt == *view.ProtectionAt {
				item.Action = "protection"
			} else {
				item.Action = "decay"
				_, payment, err := decayAmount(ctx, tx, id, c.Decay.Assets)
				if err != nil {
					return out, err
				}
				item.GeneralMilli = payment.General.Decimal()
				item.GameMilli = payment.Game.Decimal()
			}
		}
		out.Data = append(out.Data, item)
	}
	details, _ := json.Marshal(struct {
		Count  int    `json:"count"`
		Policy Policy `json:"policy"`
	}{len(out.Data), input.Policy})
	if err = insertAudit(ctx, tx, actor, "preview", old.Revision, string(details), out.AsOf); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

const runColumns = `id,user_id,policy_revision,activity_epoch,due_slot,action,general_milli,game_milli,ledger_operation_id,created_at`

func scanRun(rows *sql.Rows) (Run, error) {
	var out Run
	var user sql.NullInt64
	err := rows.Scan(&out.ID, &user, &out.PolicyRevision, &out.ActivityEpoch, &out.DueSlot, &out.Action, &out.GeneralMilli, &out.GameMilli, &out.OperationID, &out.CreatedAt)
	if user.Valid {
		v := strconv.FormatInt(user.Int64, 10)
		out.UserID = &v
	}
	return out, err
}

type RunsPage struct {
	Data       []Run   `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

func (s *Service) Runs(ctx context.Context, before int64, limit int) (RunsPage, error) {
	out := RunsPage{Data: make([]Run, 0)}
	if before < 0 || limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	tx, _, err := s.beginAdmin(ctx, false)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	// Rowid is an internal stable page position; public execution IDs remain op_.
	query := `SELECT rowid,` + runColumns + ` FROM inactivity_runs WHERE rowid<? ORDER BY rowid DESC LIMIT ?`
	if before == 0 {
		before = 9223372036854775807
	}
	rows, err := tx.QueryContext(ctx, query, before, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var last int64
	for rows.Next() {
		var item Run
		var user sql.NullInt64
		var row int64
		if err = rows.Scan(&row, &item.ID, &user, &item.PolicyRevision, &item.ActivityEpoch, &item.DueSlot, &item.Action, &item.GeneralMilli, &item.GameMilli, &item.OperationID, &item.CreatedAt); err != nil {
			return out, err
		}
		if len(out.Data) == limit {
			cursor := position(last)
			out.NextCursor = &cursor
			break
		}
		if user.Valid {
			v := strconv.FormatInt(user.Int64, 10)
			item.UserID = &v
		}
		last = row
		out.Data = append(out.Data, item)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
