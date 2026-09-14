package duel

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type PageInput struct {
	Cursor string
	Limit  int
	Mode   string
}
type HistoryPage struct {
	Items      []*ResultSummary `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}
type HistoryDetail struct {
	Result           *ResultSummary     `json:"result"`
	RulesVersion     int                `json:"rules_version"`
	ContentHash      string             `json:"content_hash"`
	Ticket           string             `json:"ticket"`
	Rake             Rates              `json:"rake_bp"`
	Initial          json.RawMessage    `json:"initial"`
	TerminalActions  [2]json.RawMessage `json:"terminal_actions"`
	RoundStartEvents json.RawMessage    `json:"round_start_events"`
}
type RoundView struct {
	Round       int             `json:"round"`
	Before      json.RawMessage `json:"before"`
	After       json.RawMessage `json:"after"`
	Facts       json.RawMessage `json:"facts"`
	StartEvents json.RawMessage `json:"start_events"`
	Timeouts    [2]bool         `json:"timeouts"`
}
type RoundPage struct {
	Items      []RoundView `json:"items"`
	NextCursor *string     `json:"next_cursor"`
	ServerNow  int64       `json:"server_now"`
}
type cursor struct {
	Game       string `json:"game"`
	Kind       string `json:"kind"`
	User       int64  `json:"user"`
	Session    string `json:"session"`
	Mode       string `json:"mode"`
	Expires    int64  `json:"expires"`
	AsOf       int64  `json:"as_of"`
	AfterTime  int64  `json:"after_time"`
	AfterID    string `json:"after_id"`
	AfterRound int    `json:"after_round"`
	MaxRound   int    `json:"max_round"`
}

func (s *Service) sealCursor(c cursor) (string, error) {
	body, err := Encode(c)
	if err != nil {
		return "", err
	}
	signature := keyed(s.cursorKey, body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(signature[:]), nil
}
func (s *Service) openCursor(value string, now int64) (cursor, error) {
	if len(value) > 4096 {
		return cursor{}, ErrInvalidRequest
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return cursor{}, ErrInvalidRequest
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursor{}, ErrInvalidRequest
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursor{}, ErrInvalidRequest
	}
	expected := keyed(s.cursorKey, body)
	if !hmac.Equal(signature, expected[:]) {
		return cursor{}, ErrInvalidRequest
	}
	var c cursor
	if Decode(body, &c) != nil || c.Game != s.rules.ID() || c.User <= 0 || c.Expires <= now || c.Expires != c.AsOf+3600 || c.AsOf > now || c.AfterRound < 0 || c.MaxRound < 0 || c.MaxRound > 75 {
		return c, ErrInvalidRequest
	}
	return c, nil
}
func pageLimit(value, defaultValue, maxValue int) (int, error) {
	if value == 0 {
		return defaultValue, nil
	}
	if value < 1 || value > maxValue {
		return 0, ErrInvalidRequest
	}
	return value, nil
}
func (s *Service) History(ctx context.Context, identity Identity, in PageInput) (HistoryPage, error) {
	limit, err := pageLimit(in.Limit, 20, 100)
	if err != nil || in.Mode != "" && s.descriptor.ResolveMode(in.Mode) != nil {
		return HistoryPage{}, ErrInvalidRequest
	}
	tx, now, err := s.beginRead(ctx)
	if err != nil {
		return HistoryPage{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, identity); err != nil {
		return HistoryPage{}, err
	}
	c := cursor{Game: s.rules.ID(), Kind: "history", User: identity.UserID, Mode: in.Mode, AsOf: now, Expires: now + 3600, AfterTime: now + 1}
	if in.Cursor != "" {
		c, err = s.openCursor(in.Cursor, now)
		if err != nil || c.Kind != "history" || c.User != identity.UserID || c.Mode != in.Mode || c.Session != "" {
			return HistoryPage{}, ErrInvalidRequest
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT g.id FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE p.user_id=? AND g.game_key=? AND g.state='terminal' AND g.delete_at>? AND g.terminal_at<=? AND (?='' OR g.mode=?) AND (g.terminal_at<? OR (g.terminal_at=? AND g.id<?)) ORDER BY g.terminal_at DESC,g.id DESC LIMIT ?`, identity.UserID, s.rules.ID(), now, c.AsOf, in.Mode, in.Mode, c.AfterTime, c.AfterTime, c.AfterID, limit+1)
	if err != nil {
		return HistoryPage{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return HistoryPage{}, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return HistoryPage{}, err
	}
	page := HistoryPage{Items: []*ResultSummary{}}
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	for _, id := range ids {
		v, err := s.session(ctx, tx, id)
		if err != nil {
			return page, err
		}
		seat, err := s.seat(&v, identity.UserID)
		if err != nil {
			return page, err
		}
		if err := s.authorizeExisting(ctx, tx, v, identity, "history", now); err != nil {
			return page, err
		}
		summary, err := s.projectResult(v, seat)
		if err != nil {
			return page, err
		}
		summary.Resolution = nil
		summary.View = nil
		summary.Profiles, err = profiles(ctx, tx, v)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, summary)
		c.AfterTime = *v.TerminalAt
		c.AfterID = id
	}
	if more {
		next, err := s.sealCursor(c)
		if err != nil {
			return page, err
		}
		page.NextCursor = &next
	}
	return page, nil
}
func (s *Service) historySession(ctx context.Context, tx *sql.Tx, user int64, id string, now int64, allowActive bool) (sessionRecord, int, error) {
	if !db.ValidateOpaqueID(id, s.sessionPrefix) {
		return sessionRecord{}, 0, ErrNotFound
	}
	v, err := s.session(ctx, tx, id)
	if err != nil {
		return v, 0, notFound(err)
	}
	seat, err := s.seat(&v, user)
	if err != nil {
		return v, 0, err
	}
	if v.State == "terminal" {
		if v.DeleteAt == nil || now >= *v.DeleteAt {
			return v, 0, ErrNotFound
		}
	} else if !allowActive {
		return v, 0, ErrNotFound
	}
	return v, seat, nil
}
func (s *Service) detail(v sessionRecord, seat int) (HistoryDetail, error) {
	summary, err := s.projectResult(v, seat)
	if err != nil {
		return HistoryDetail{}, err
	}
	initial, err := s.rules.View(v.Mode, v.Initial, seat, true, nil)
	if err != nil {
		return HistoryDetail{}, err
	}
	return HistoryDetail{Result: summary, RulesVersion: 1, ContentHash: v.Terms.ContentHash, Ticket: v.Terms.Ticket, Rake: v.Terms.Rake, Initial: initial, TerminalActions: v.Payload.TerminalActions, RoundStartEvents: v.Payload.RoundStartEvents}, nil
}
func (s *Service) HistoryDetail(ctx context.Context, identity Identity, id string) (HistoryDetail, error) {
	tx, now, err := s.beginRead(ctx)
	if err != nil {
		return HistoryDetail{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, identity); err != nil {
		return HistoryDetail{}, err
	}
	v, seat, err := s.historySession(ctx, tx, identity.UserID, id, now, false)
	if err != nil {
		return HistoryDetail{}, err
	}
	if err := s.authorizeExisting(ctx, tx, v, identity, "history", now); err != nil {
		return HistoryDetail{}, err
	}
	detail, err := s.detail(v, seat)
	if err != nil {
		return HistoryDetail{}, err
	}
	detail.Result.Profiles, err = profiles(ctx, tx, v)
	return detail, err
}
func (s *Service) roundView(mode string, raw []byte, seat int, terminal bool) (RoundView, error) {
	var record roundRecord
	if Decode(raw, &record) != nil {
		return RoundView{}, ErrInvariant
	}
	before, err := s.rules.View(mode, record.Before, seat, terminal, nil)
	if err != nil {
		return RoundView{}, err
	}
	after, err := s.rules.View(mode, record.After, seat, terminal, nil)
	if err != nil {
		return RoundView{}, err
	}
	facts, err := s.rules.RoundView(mode, record.Facts, seat, terminal)
	if err != nil {
		return RoundView{}, err
	}
	return RoundView{Round: record.Round, Before: before, After: after, Facts: facts, StartEvents: record.StartEvents, Timeouts: record.Timeouts}, nil
}
func (s *Service) Rounds(ctx context.Context, identity Identity, id string, in PageInput, allowActive bool) (RoundPage, error) {
	limit, err := pageLimit(in.Limit, 5, 10)
	if err != nil || in.Mode != "" || allowActive && s.rules.ID() != "likes" {
		return RoundPage{}, ErrInvalidRequest
	}
	tx, now, err := s.beginRead(ctx)
	if err != nil {
		return RoundPage{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, identity); err != nil {
		return RoundPage{}, err
	}
	v, seat, err := s.historySession(ctx, tx, identity.UserID, id, now, allowActive)
	if err != nil {
		return RoundPage{}, err
	}
	if err := s.authorizeExisting(ctx, tx, v, identity, "rounds", now); err != nil {
		return RoundPage{}, err
	}
	c := cursor{Game: s.rules.ID(), Kind: "rounds", User: identity.UserID, Session: id, AsOf: now, Expires: now + 3600}
	if in.Cursor != "" {
		c, err = s.openCursor(in.Cursor, now)
		if err != nil || c.Kind != "rounds" || c.User != identity.UserID || c.Session != id || c.Mode != "" {
			return RoundPage{}, ErrInvalidRequest
		}
	} else if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(round_no),0) FROM game_duel_rounds WHERE session_id=?`, id).Scan(&c.MaxRound); err != nil {
		return RoundPage{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT record_json FROM game_duel_rounds WHERE session_id=? AND round_no>? AND round_no<=? ORDER BY round_no LIMIT ?`, id, c.AfterRound, c.MaxRound, limit+1)
	if err != nil {
		return RoundPage{}, err
	}
	records := [][]byte{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			rows.Close()
			return RoundPage{}, err
		}
		records = append(records, body)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return RoundPage{}, err
	}
	page := RoundPage{Items: []RoundView{}, ServerNow: now}
	more := len(records) > limit
	if more {
		records = records[:limit]
	}
	size := 0
	for _, body := range records {
		item, err := s.roundView(v.Mode, body, seat, v.State == "terminal")
		if err != nil {
			return page, err
		}
		encoded, err := Encode(item)
		if err != nil {
			return page, err
		}
		if size+len(encoded) > 8<<20 {
			more = true
			break
		}
		page.Items = append(page.Items, item)
		size += len(encoded)
		c.AfterRound = item.Round
	}
	if more {
		next, err := s.sealCursor(c)
		if err != nil {
			return page, err
		}
		page.NextCursor = &next
	}
	return page, nil
}
