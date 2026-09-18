package blackjack

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

type AdminAudit struct {
	Actor   int64
	Dataset string
	Records int
	Result  string
}
type AnonymousFact struct {
	RulesVersion int       `json:"rules_version"`
	Phase        string    `json:"phase"`
	Reason       string    `json:"reason"`
	Fact         TableFact `json:"fact"`
}
type AdminParticipant struct {
	Seat       int          `json:"seat"`
	UserID     *string      `json:"user_id"`
	Payment    game.Payment `json:"payment"`
	Operations []string     `json:"operations"`
}
type AdminRecent struct {
	StartedAt    int64              `json:"started_at"`
	TerminalAt   int64              `json:"terminal_at"`
	Participants []AdminParticipant `json:"participants"`
}
type AdminDetail struct {
	ID      string        `json:"id"`
	Dataset string        `json:"dataset"`
	Record  AnonymousFact `json:"record"`
	Recent  *AdminRecent  `json:"recent,omitempty"`
}
type AdminSummary struct {
	ID         string `json:"id"`
	Phase      string `json:"phase"`
	Reason     string `json:"reason"`
	Seats      int    `json:"seats"`
	TotalStake string `json:"total_stake"`
	Net        string `json:"net"`
	StartedAt  *int64 `json:"started_at,omitempty"`
	TerminalAt *int64 `json:"terminal_at,omitempty"`
}
type AdminPage struct {
	Dataset    string         `json:"dataset"`
	Items      []AdminSummary `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}
type AdminExportPage struct {
	Format     string        `json:"format"`
	Dataset    string        `json:"dataset"`
	Items      []AdminDetail `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

func (s *Service) authorizeAdmin(ctx context.Context, tx *sql.Tx) (int64, error) {
	if err := s.adminAuthorizer.AuthorizeAdminMutation(ctx, tx); err != nil {
		if errors.Is(err, authz.ErrUnauthorized) {
			return 0, ErrUnauthorized
		}
		if errors.Is(err, authz.ErrForbidden) {
			return 0, ErrForbidden
		}
		return 0, classify(err)
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin=1`).Scan(&id)
	return id, err
}
func anonymizeFact(v sessionRecord) (AnonymousFact, error) {
	out := AnonymousFact{RulesVersion: 1, Phase: v.Phase, Reason: v.Reason.String}
	if !v.TerminalAt.Valid || !v.ViewJSON.Valid || json.Unmarshal([]byte(v.ViewJSON.String), &out.Fact) != nil {
		return out, ErrInvariant
	}
	for i := range out.Fact.Seats {
		out.Fact.Seats[i].Emote = ""
		out.Fact.Seats[i].EmoteAt = nil
	}
	return out, nil
}
func (s *Service) adminDetailTx(ctx context.Context, tx *sql.Tx, dataset, id string, now int64) (AdminDetail, error) {
	out := AdminDetail{ID: id, Dataset: dataset}
	if dataset == "anonymous" {
		if !db.ValidateOpaqueID(id, "bja_") {
			return out, ErrInvalid
		}
		var body []byte
		err := tx.QueryRowContext(ctx, `SELECT public_json FROM game_blackjack_anonymous WHERE archive_id=?`, id).Scan(&body)
		if err != nil {
			return out, classify(err)
		}
		if json.Unmarshal(body, &out.Record) != nil {
			return out, ErrInvariant
		}
		return out, nil
	}
	if dataset != "recent" || !db.ValidateOpaqueID(id, "bjt_") {
		return out, ErrInvalid
	}
	v, err := readSession(ctx, tx, id)
	if err != nil {
		return out, classify(err)
	}
	if !v.TerminalAt.Valid || v.TerminalAt.Int64+retentionSeconds <= now {
		return out, ErrNotFound
	}
	out.Record, err = anonymizeFact(v)
	if err != nil {
		return out, err
	}
	r := &AdminRecent{StartedAt: v.StartedAt, TerminalAt: v.TerminalAt.Int64, Participants: []AdminParticipant{}}
	list, err := entries(ctx, tx, `session_id=? ORDER BY seat_no`, id)
	if err != nil {
		return out, err
	}
	for _, e := range list {
		total, g, err := paymentTotals(ctx, tx, e.ID)
		if err != nil {
			return out, err
		}
		p := AdminParticipant{Seat: int(e.Seat.Int64), Payment: game.PaymentFromMilli(total, g), Operations: []string{}}
		if e.User.Valid {
			id := decimal(e.User.Int64)
			p.UserID = &id
		}
		rows, err := tx.QueryContext(ctx, `SELECT reserve_operation_id,terminal_operation_id FROM game_blackjack_payments WHERE entry_id=? ORDER BY ordinal`, e.ID)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var reserve, terminal string
			if err := rows.Scan(&reserve, &terminal); err != nil {
				rows.Close()
				return out, err
			}
			p.Operations = append(p.Operations, reserve, terminal)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		r.Participants = append(r.Participants, p)
	}
	out.Recent = r
	return out, nil
}
func (s *Service) AdminDetail(ctx context.Context, dataset, id string) (AdminDetail, error) {
	tx, now, err := s.begin(ctx)
	if err != nil {
		return AdminDetail{}, err
	}
	defer tx.Rollback()
	if _, err := s.authorizeAdmin(ctx, tx); err != nil {
		return AdminDetail{}, err
	}
	return s.adminDetailTx(ctx, tx, dataset, id, now)
}

type adminRef struct {
	id       string
	position int64
}

func adminRefs(ctx context.Context, tx *sql.Tx, c pageCursor, now int64, limit int) ([]adminRef, error) {
	query := `SELECT id,started_at FROM game_blackjack_sessions WHERE started_at<=? AND terminal_at>? AND terminal_at<=? ORDER BY started_at DESC LIMIT ?`
	args := []any{c.Before, now - retentionSeconds, c.AsOf, limit + 1}
	if c.Dataset == "anonymous" {
		query = `SELECT archive_id,export_seq FROM game_blackjack_anonymous WHERE export_seq<=? ORDER BY export_seq DESC LIMIT ?`
		args = []any{c.Before, limit + 1}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []adminRef{}
	for rows.Next() {
		var r adminRef
		if err := rows.Scan(&r.id, &r.position); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func summarizeAdmin(d AdminDetail) AdminSummary {
	stake, net := int64(0), int64(0)
	for _, seat := range d.Record.Fact.Settlements {
		for _, h := range seat.Hands {
			stake += h.Stake
			net += h.Net
		}
	}
	if d.Record.Phase == "cancelled" {
		for _, refund := range d.Record.Fact.Refunds {
			amount, _ := game.ParseAmount(refund.Amount)
			stake += amount
		}
	}
	out := AdminSummary{ID: d.ID, Phase: d.Record.Phase, Reason: d.Record.Reason, Seats: len(d.Record.Fact.Seats), TotalStake: game.FormatAmount(stake), Net: game.FormatAmount(net)}
	if d.Recent != nil {
		out.StartedAt = &d.Recent.StartedAt
		out.TerminalAt = &d.Recent.TerminalAt
	}
	return out
}
func (s *Service) AdminHistory(ctx context.Context, in PageInput) (AdminPage, error) {
	out := AdminPage{Dataset: in.Dataset, Items: []AdminSummary{}}
	limit, err := pageLimit(in.Limit)
	if err != nil || in.Dataset != "recent" && in.Dataset != "anonymous" {
		return out, ErrInvalid
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	admin, err := s.authorizeAdmin(ctx, tx)
	if err != nil {
		return out, err
	}
	c, err := s.openCursor(ctx, tx, Identity{UserID: admin}, now, "admin-history", in)
	if err != nil {
		return out, err
	}
	refs, err := adminRefs(ctx, tx, c, now, limit)
	if err != nil {
		return out, err
	}
	for _, r := range refs[:min(limit, len(refs))] {
		d, err := s.adminDetailTx(ctx, tx, in.Dataset, r.id, now)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, summarizeAdmin(d))
	}
	if len(refs) > limit {
		out.NextCursor, err = s.nextCursor(c, refs[limit-1].position)
	}
	return out, err
}
func (s *Service) AdminExport(ctx context.Context, in PageInput) (out AdminExportPage, err error) {
	out = AdminExportPage{Format: "blackjack-history/v1", Dataset: in.Dataset, Items: []AdminDetail{}}
	if in.Dataset != "recent" && in.Dataset != "anonymous" || in.Limit != 0 {
		return out, ErrInvalid
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	admin, err := s.authorizeAdmin(ctx, tx)
	if err != nil {
		return out, err
	}
	defer func() {
		if s.adminAudit != nil {
			result := "success"
			if err != nil {
				result = "failed"
			}
			s.adminAudit(AdminAudit{Actor: admin, Dataset: in.Dataset, Records: len(out.Items), Result: result})
		}
	}()
	c, err := s.openCursor(ctx, tx, Identity{UserID: admin}, now, "admin-export", in)
	if err != nil {
		return out, err
	}
	const limit = 10
	refs, err := adminRefs(ctx, tx, c, now, limit)
	if err != nil {
		return out, err
	}
	for _, r := range refs[:min(limit, len(refs))] {
		d, e := s.adminDetailTx(ctx, tx, in.Dataset, r.id, now)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, d)
	}
	if len(refs) > limit {
		out.NextCursor, err = s.nextCursor(c, refs[limit-1].position)
	}
	return out, err
}
