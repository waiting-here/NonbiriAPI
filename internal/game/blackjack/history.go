package blackjack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

type PageInput struct {
	Cursor  string
	Limit   int
	Dataset string
}
type HistorySummary struct {
	ID         string       `json:"id"`
	StartedAt  int64        `json:"started_at"`
	TerminalAt int64        `json:"terminal_at"`
	Phase      string       `json:"phase"`
	Reason     string       `json:"reason"`
	Seat       int          `json:"seat"`
	Stake      string       `json:"stake"`
	TotalStake string       `json:"total_stake"`
	Net        string       `json:"net"`
	Payment    game.Payment `json:"payment"`
}
type HistoryPage struct {
	Items      []HistorySummary `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}
type HistoryDetail struct {
	Summary HistorySummary `json:"summary"`
	Table   TableView      `json:"table"`
}
type pageCursor struct {
	Kind    string `json:"kind"`
	Actor   int64  `json:"actor"`
	Binding string `json:"binding"`
	Dataset string `json:"dataset"`
	AsOf    int64  `json:"as_of"`
	Expires int64  `json:"expires"`
	High    int64  `json:"high"`
	Before  int64  `json:"before"`
}

func (s *Service) sealCursor(c pageCursor) (string, error) {
	body, err := marshal(c)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.cursorKey[:])
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (s *Service) openCursor(ctx context.Context, tx *sql.Tx, identity Identity, now int64, kind string, in PageInput) (pageCursor, error) {
	hash := sha256.Sum256([]byte(identity.SessionBinding))
	binding := hex.EncodeToString(hash[:])
	c := pageCursor{Kind: kind, Actor: identity.UserID, Binding: binding, Dataset: in.Dataset, AsOf: now, Expires: now + 3600}
	if in.Cursor == "" {
		query := `SELECT COALESCE(MAX(started_at),0) FROM game_blackjack_sessions WHERE terminal_at IS NOT NULL AND terminal_at<=?`
		args := []any{now}
		if in.Dataset == "anonymous" {
			query = `SELECT COALESCE(MAX(export_seq),0) FROM game_blackjack_anonymous`
			args = nil
		}
		err := tx.QueryRowContext(ctx, query, args...).Scan(&c.High)
		c.Before = c.High
		return c, err
	}
	parts := strings.Split(in.Cursor, ".")
	if len(in.Cursor) > 2048 || len(parts) != 2 {
		return c, ErrInvalid
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c, ErrInvalid
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, ErrInvalid
	}
	expected := hmac.New(sha256.New, s.cursorKey[:])
	expected.Write(body)
	if !hmac.Equal(mac, expected.Sum(nil)) || json.Unmarshal(body, &c) != nil || c.Kind != kind || c.Actor != identity.UserID || c.Binding != binding || c.Dataset != in.Dataset || c.AsOf < 0 || c.AsOf > now || c.Expires != c.AsOf+3600 || c.Expires <= now || c.High < 0 || c.Before < 0 || c.Before > c.High {
		return c, ErrInvalid
	}
	return c, nil
}
func pageLimit(n int) (int, error) {
	if n == 0 {
		return 20, nil
	}
	if n < 1 || n > 50 {
		return 0, ErrInvalid
	}
	return n, nil
}
func (s *Service) nextCursor(c pageCursor, last int64) (*string, error) {
	if last <= 0 {
		return nil, nil
	}
	c.Before = last - 1
	next, err := s.sealCursor(c)
	return &next, err
}
func ownSummary(ctx context.Context, tx *sql.Tx, e entryRecord, v TableView) (HistorySummary, error) {
	total, gamePaid, err := paymentTotals(ctx, tx, e.ID)
	if err != nil {
		return HistorySummary{}, err
	}
	net := int64(0)
	for _, settlement := range v.Fact.Settlements {
		if settlement.Seat == int(e.Seat.Int64) {
			for _, h := range settlement.Hands {
				net += h.Net
			}
		}
	}
	terminal := int64(0)
	if v.TerminalAt != nil {
		terminal = *v.TerminalAt
	}
	return HistorySummary{ID: v.ID, StartedAt: v.StartedAt, TerminalAt: terminal, Phase: v.Phase, Reason: v.Reason, Seat: int(e.Seat.Int64), Stake: game.FormatAmount(e.Stake), TotalStake: game.FormatAmount(total), Net: game.FormatAmount(net), Payment: game.PaymentFromMilli(total, gamePaid)}, nil
}
func (s *Service) historyDetailTx(ctx context.Context, tx *sql.Tx, identity Identity, id string, now int64) (HistoryDetail, error) {
	e, err := participant(ctx, tx, id, identity.UserID)
	if err != nil {
		return HistoryDetail{}, classify(err)
	}
	v, err := readSession(ctx, tx, id)
	if err != nil {
		return HistoryDetail{}, classify(err)
	}
	if !v.TerminalAt.Valid || v.TerminalAt.Int64+retentionSeconds <= now {
		return HistoryDetail{}, ErrNotFound
	}
	if err := s.authorizeExisting(ctx, tx, v, identity, "history", now); err != nil {
		return HistoryDetail{}, err
	}
	view, err := projectTable(ctx, tx, v, now)
	if err != nil {
		return HistoryDetail{}, err
	}
	summary, err := ownSummary(ctx, tx, e, view)
	return HistoryDetail{Summary: summary, Table: view}, err
}
func (s *Service) HistoryDetail(ctx context.Context, identity Identity, id string) (HistoryDetail, error) {
	if !db.ValidateOpaqueID(id, "bjt_") {
		return HistoryDetail{}, ErrInvalid
	}
	tx, now, err := s.begin(ctx)
	if err != nil {
		return HistoryDetail{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, identity); err != nil {
		return HistoryDetail{}, err
	}
	return s.historyDetailTx(ctx, tx, identity, id, now)
}
func (s *Service) History(ctx context.Context, identity Identity, in PageInput) (HistoryPage, error) {
	out := HistoryPage{Items: []HistorySummary{}}
	limit, err := pageLimit(in.Limit)
	if err != nil || in.Dataset != "" {
		return out, ErrInvalid
	}
	in.Dataset = "recent"
	tx, now, err := s.begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, identity); err != nil {
		return out, err
	}
	c, err := s.openCursor(ctx, tx, identity, now, "history", in)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT g.id,g.started_at FROM game_blackjack_sessions g WHERE g.terminal_at>? AND g.terminal_at<=? AND g.started_at<=? AND EXISTS(SELECT 1 FROM game_blackjack_entries e WHERE e.session_id=g.id AND e.user_id=?) ORDER BY g.started_at DESC LIMIT ?`, now-retentionSeconds, c.AsOf, c.Before, identity.UserID, limit+1)
	if err != nil {
		return out, err
	}
	type ref struct {
		id    string
		start int64
	}
	refs := []ref{}
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.id, &r.start); err != nil {
			rows.Close()
			return out, err
		}
		refs = append(refs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, r := range refs[:min(limit, len(refs))] {
		detail, err := s.historyDetailTx(ctx, tx, identity, r.id, now)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, detail.Summary)
	}
	if len(refs) > limit {
		out.NextCursor, err = s.nextCursor(c, refs[limit-1].start)
	}
	return out, err
}

func decimal(v int64) string { return strconv.FormatInt(v, 10) }
