package duel

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

// AdminAudit deliberately contains no cursor, match reference, or game content.
type AdminAudit struct {
	Actor               int64
	Role, Game, Dataset string
	Filters             []string
	Records             int
	Result              string
}

type AdminSelection struct {
	Mode         string `json:"mode,omitempty"`
	RulesVersion *int   `json:"rules_version,omitempty"`
	Outcome      string `json:"outcome,omitempty"`
	From         *int64 `json:"from,omitempty"`
	To           *int64 `json:"to,omitempty"`
}
type AdminPageInput struct {
	Dataset   string
	Selection AdminSelection
	Cursor    string
	Limit     int
}
type AdminExportInput struct {
	Dataset   string         `json:"dataset"`
	Selection AdminSelection `json:"selection"`
	Cursor    *string        `json:"cursor"`
}
type AdminExportPage struct {
	Format         string            `json:"format"`
	Dataset        string            `json:"dataset"`
	Items          []json.RawMessage `json:"items"`
	NextCursor     *string           `json:"next_cursor"`
	ExpiredSkipped int               `json:"expired_skipped"`
}
type AdminParticipant struct {
	UserID      *string `json:"user_id"`
	DisplayName string  `json:"display_name"`
	GeneralPaid string  `json:"general_paid"`
	GamePaid    string  `json:"game_paid"`
}
type AdminRecent struct {
	StartedAt    int64               `json:"started_at"`
	TerminalAt   int64               `json:"terminal_at"`
	OperationID  string              `json:"operation_id"`
	LedgerSeq    string              `json:"ledger_seq"`
	Participants [2]AdminParticipant `json:"participants"`
}
type AdminMatch struct {
	Kind     string          `json:"kind"`
	MatchRef string          `json:"match_ref"`
	Facts    anonymousHeader `json:"facts"`
	Recent   *AdminRecent    `json:"recent,omitempty"`
}
type AdminRound struct {
	Kind     string    `json:"kind"`
	MatchRef string    `json:"match_ref"`
	RoundNo  int       `json:"round_no"`
	Record   RoundView `json:"record"`
}
type AdminSummary struct {
	MatchRef     string       `json:"match_ref"`
	Game         string       `json:"game"`
	Mode         string       `json:"mode"`
	RulesVersion int          `json:"rules_version"`
	ContentHash  string       `json:"content_hash"`
	Outcome      string       `json:"outcome"`
	Reason       string       `json:"reason"`
	Winner       *int         `json:"winner"`
	Scores       [2]int64     `json:"scores"`
	Ticket       string       `json:"ticket"`
	Prize        string       `json:"prize"`
	Cuts         RakeAmounts  `json:"cuts"`
	Recent       *AdminRecent `json:"recent,omitempty"`
}
type AdminHistoryPage struct {
	Dataset    string         `json:"dataset"`
	Items      []AdminSummary `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

type adminCursor struct {
	Kind          string         `json:"kind"`
	Game          string         `json:"game"`
	Admin         int64          `json:"admin"`
	Dataset       string         `json:"dataset"`
	Selection     AdminSelection `json:"selection"`
	AsOf          int64          `json:"as_of"`
	Expires       int64          `json:"expires"`
	High          int64          `json:"high"`
	AfterSeq      int64          `json:"after_seq"`
	AfterID       string         `json:"after_id"`
	AfterRecord   int            `json:"after_record"`
	PendingExpiry int64          `json:"pending_expiry"`
	Session       string         `json:"session"`
}

func (s *Service) authorizeAdmin(ctx context.Context, tx *sql.Tx) (int64, error) {
	if s.adminAuthorizer == nil {
		return 0, ErrForbidden
	}
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
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin=1`).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}
func (s *Service) validateSelection(dataset string, v AdminSelection) error {
	if dataset != "recent" && dataset != "anonymous" || v.Mode != "" && s.descriptor.ResolveMode(v.Mode) != nil || v.RulesVersion != nil && (*v.RulesVersion < 1 || *v.RulesVersion > 2147483647) {
		return ErrInvalidRequest
	}
	if v.Outcome != "" && v.Outcome != "normal" && v.Outcome != "draw" && v.Outcome != "system_cancelled" {
		return ErrInvalidRequest
	}
	if dataset == "anonymous" && (v.From != nil || v.To != nil) {
		return ErrInvalidRequest
	}
	for _, value := range []*int64{v.From, v.To} {
		if value != nil && (*value < 0 || *value > maxDecisionTime) {
			return ErrInvalidRequest
		}
	}
	if v.From != nil && v.To != nil && *v.From > *v.To {
		return ErrInvalidRequest
	}
	return nil
}
func (s *Service) sealAdminCursor(c adminCursor) (string, error) {
	body, err := Encode(c)
	if err != nil {
		return "", err
	}
	mac := keyed(s.cursorKey, append([]byte("admin\n"), body...))
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac[:]), nil
}
func (s *Service) adminCursor(ctx context.Context, tx *sql.Tx, admin, now int64, kind, session string, in AdminPageInput) (adminCursor, error) {
	c := adminCursor{Kind: kind, Game: s.rules.ID(), Admin: admin, Dataset: in.Dataset, Selection: in.Selection, AsOf: now, Expires: now + 3600, AfterRecord: 76, Session: session}
	if in.Cursor == "" {
		query := `SELECT COALESCE(MAX(o.ledger_seq),0) FROM credit_operations o CROSS JOIN game_duel_sessions g ON g.terminal_operation_id=o.id WHERE o.kind='duel_terminal' AND o.source_type='duel_session' AND substr(o.source_id,1,4)=? AND g.game_key=? AND g.state='terminal'`
		args := []any{s.sessionPrefix, s.rules.ID()}
		if in.Dataset == "anonymous" {
			query = `SELECT COALESCE(MAX(export_seq),0) FROM game_duel_anonymous WHERE game_key=?`
			args = []any{s.rules.ID()}
		}
		err := tx.QueryRowContext(ctx, query, args...).Scan(&c.High)
		return c, err
	}
	parts := strings.Split(in.Cursor, ".")
	if len(in.Cursor) > 4096 || len(parts) != 2 {
		return c, ErrInvalidRequest
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c, ErrInvalidRequest
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, ErrInvalidRequest
	}
	expected := keyed(s.cursorKey, append([]byte("admin\n"), body...))
	c = adminCursor{}
	if !hmac.Equal(mac, expected[:]) || Decode(body, &c) != nil {
		return c, ErrInvalidRequest
	}
	a, _ := json.Marshal(in.Selection)
	b, _ := json.Marshal(c.Selection)
	if c.Kind != kind || c.Game != s.rules.ID() || c.Admin != admin || c.Dataset != in.Dataset || c.Session != session || string(a) != string(b) || c.AsOf > now || c.AsOf < 0 || c.Expires != c.AsOf+3600 || c.Expires <= now || c.High < 0 || c.AfterSeq < 0 || c.AfterSeq > c.High || c.AfterRecord < 0 || c.AfterRecord > 76 {
		return c, ErrInvalidRequest
	}
	return c, nil
}

type adminMatchPosition struct {
	id, mode    string
	seq, expiry int64
}

// Positions are small. Game JSON is loaded only for the next bounded record.
func (s *Service) nextAdminMatch(ctx context.Context, tx *sql.Tx, c adminCursor) (adminMatchPosition, error) {
	var p adminMatchPosition
	args := []any{s.rules.ID(), c.High}
	query := `SELECT g.id,g.mode,o.ledger_seq,g.delete_at FROM credit_operations o CROSS JOIN game_duel_sessions g ON g.terminal_operation_id=o.id WHERE g.game_key=? AND o.ledger_seq<=? AND o.kind='duel_terminal' AND o.source_type='duel_session' AND substr(o.source_id,1,4)=? AND g.state='terminal' AND g.terminal_at<=? AND g.delete_at>? AND o.ledger_seq>=? AND (o.ledger_seq>? OR (o.ledger_seq=? AND g.id>?))`
	if c.Dataset == "recent" {
		args = append(args, s.sessionPrefix, c.AsOf, c.AsOf, c.AfterSeq, c.AfterSeq, c.AfterSeq, c.AfterID)
	} else {
		query = `SELECT g.archive_id,g.mode,g.export_seq,0 FROM game_duel_anonymous g WHERE g.game_key=? AND g.export_seq<=? AND g.archive_id>?`
		args = append(args, c.AfterID)
	}
	if c.Selection.Mode != "" {
		query += ` AND g.mode=?`
		args = append(args, c.Selection.Mode)
	}
	if c.Selection.RulesVersion != nil {
		query += ` AND ?=1`
		args = append(args, *c.Selection.RulesVersion)
	}
	if c.Selection.Outcome != "" {
		outcome := c.Selection.Outcome
		if c.Dataset == "recent" {
			query += ` AND g.outcome=?`
			if outcome == "normal" {
				outcome = "decided"
			}
		} else {
			query += ` AND json_extract(g.header_json,'$.outcome')=?`
		}
		args = append(args, outcome)
	}
	if c.Selection.From != nil {
		query += ` AND g.terminal_at>=?`
		args = append(args, *c.Selection.From)
	}
	if c.Selection.To != nil {
		query += ` AND g.terminal_at<=?`
		args = append(args, *c.Selection.To)
	}
	if c.Dataset == "recent" {
		query += ` ORDER BY o.ledger_seq,g.id LIMIT 1`
	} else {
		query += ` ORDER BY g.archive_id LIMIT 1`
	}
	err := tx.QueryRowContext(ctx, query, args...).Scan(&p.id, &p.mode, &p.seq, &p.expiry)
	return p, err
}

// Reuse only the current fully validated session within one read transaction.
// Advancing to another match replaces it; later requests always read afresh.
func (s *Service) adminSession(ctx context.Context, tx *sql.Tx, id string, last *sessionRecord) (sessionRecord, error) {
	if last.ID == id {
		return *last, nil
	}
	v, err := s.session(ctx, tx, id)
	if err == nil {
		*last = v
	}
	return v, err
}

func (s *Service) adminMatch(ctx context.Context, tx *sql.Tx, dataset, id string, now int64, last *sessionRecord) (AdminMatch, error) {
	item := AdminMatch{Kind: "match", MatchRef: id}
	if dataset == "anonymous" {
		if !db.ValidateOpaqueID(id, "dah_") {
			return item, ErrNotFound
		}
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT header_json FROM game_duel_anonymous WHERE game_key=? AND archive_id=?`, s.rules.ID(), id).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return item, ErrNotFound
		}
		if err != nil {
			return item, err
		}
		if Decode(raw, &item.Facts) != nil {
			return item, ErrInvariant
		}
		return item, nil
	}
	if !db.ValidateOpaqueID(id, s.sessionPrefix) {
		return item, ErrNotFound
	}
	v, err := s.adminSession(ctx, tx, id, last)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	if v.State != "terminal" || v.DeleteAt == nil || *v.DeleteAt <= now {
		return item, ErrNotFound
	}
	item.Facts, err = s.anonymousHeader(v)
	if err != nil {
		return item, err
	}
	r := &AdminRecent{StartedAt: v.Started, TerminalAt: *v.TerminalAt, OperationID: v.Operation}
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT ledger_seq FROM credit_operations WHERE id=?`, v.Operation).Scan(&seq); err != nil {
		return item, err
	}
	r.LedgerSeq = strconv.FormatInt(seq, 10)
	for i, p := range v.Seats {
		r.Participants[i] = AdminParticipant{GeneralPaid: game.FormatAmount(p.GeneralPaid), GamePaid: game.FormatAmount(p.GamePaid)}
		if p.User == nil {
			continue
		}
		id := strconv.FormatInt(*p.User, 10)
		r.Participants[i].UserID = &id
		var name string
		if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN guild_nick<>'' THEN guild_nick ELSE username END FROM users WHERE id=?`, *p.User).Scan(&name); err != nil {
			return item, err
		}
		name = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, name)
		runes := []rune(name)
		if len(runes) > 128 {
			runes = runes[:128]
		}
		r.Participants[i].DisplayName = string(runes)
	}
	item.Recent = r
	return item, nil
}
func (s *Service) nextAdminRound(ctx context.Context, tx *sql.Tx, dataset, id, mode string, after int, last *sessionRecord) (AdminRound, error) {
	item := AdminRound{Kind: "round", MatchRef: id}
	query := `SELECT round_no,record_json FROM game_duel_rounds WHERE session_id=? AND round_no>? ORDER BY round_no LIMIT 1`
	if dataset == "anonymous" {
		query = `SELECT round_no,record_json FROM game_duel_anonymous_rounds WHERE archive_id=? AND round_no>? ORDER BY round_no LIMIT 1`
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, query, id, after).Scan(&item.RoundNo, &raw); err != nil {
		return item, err
	}
	if dataset == "anonymous" {
		if Decode(raw, &item.Record) != nil {
			return item, ErrInvariant
		}
	} else {
		v, err := s.adminSession(ctx, tx, id, last)
		if err != nil {
			return item, err
		}
		item.Record, err = s.roundView(v.rules, mode, raw, 0, true)
		if err != nil {
			return item, err
		}
	}
	return item, nil
}

func (s *Service) adminMode(ctx context.Context, tx *sql.Tx, dataset, id string, now int64) (string, error) {
	query := `SELECT mode FROM game_duel_sessions WHERE game_key=? AND id=? AND state='terminal' AND delete_at>?`
	args := []any{s.rules.ID(), id, now}
	if dataset == "anonymous" {
		query = `SELECT mode FROM game_duel_anonymous WHERE game_key=? AND archive_id=?`
		args = args[:2]
	}
	var mode string
	err := tx.QueryRowContext(ctx, query, args...).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return mode, err
}

func (s *Service) AdminExport(ctx context.Context, in AdminExportInput) (page AdminExportPage, err error) {
	page = AdminExportPage{Format: "duel-history-v1", Dataset: in.Dataset, Items: []json.RawMessage{}}
	if err = s.validateSelection(in.Dataset, in.Selection); err != nil {
		return page, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, now, err := s.beginRead(ctx)
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	admin, err := s.authorizeAdmin(ctx, tx)
	if err != nil {
		return page, err
	}
	s.exportMu.Lock()
	busy := s.exporting[admin]
	if !busy {
		s.exporting[admin] = true
	}
	s.exportMu.Unlock()
	if busy {
		return page, ErrRateLimited
	}
	defer func() { s.exportMu.Lock(); delete(s.exporting, admin); s.exportMu.Unlock() }()
	defer func() {
		if s.adminAudit != nil {
			result := "ok"
			if err != nil {
				result = "failed"
			}
			filters := []string{}
			if in.Selection.Mode != "" {
				filters = append(filters, "mode")
			}
			if in.Selection.RulesVersion != nil {
				filters = append(filters, "rules_version")
			}
			if in.Selection.Outcome != "" {
				filters = append(filters, "outcome")
			}
			if in.Selection.From != nil {
				filters = append(filters, "from")
			}
			if in.Selection.To != nil {
				filters = append(filters, "to")
			}
			s.adminAudit(AdminAudit{Actor: admin, Role: "administrator", Game: s.rules.ID(), Dataset: in.Dataset, Filters: filters, Records: len(page.Items), Result: result})
		}
	}()
	input := AdminPageInput{Dataset: in.Dataset, Selection: in.Selection}
	if in.Cursor != nil {
		input.Cursor = *in.Cursor
		if input.Cursor == "" {
			return page, ErrInvalidRequest
		}
	}
	c, err := s.adminCursor(ctx, tx, admin, now, "export", "", input)
	if err != nil {
		return page, err
	}
	size := 8192 // Reserved for the envelope and the maximum signed cursor.
	var lastSession sessionRecord
	for visited := 0; visited < 200; visited++ {
		if err = ctx.Err(); err != nil {
			return page, err
		}
		before := c
		var item any
		if c.AfterRecord < 76 {
			if c.Dataset == "recent" && c.PendingExpiry <= now {
				page.ExpiredSkipped++
				c.AfterRecord = 76
				continue
			}
			mode := lastSession.Mode
			var readErr error
			if c.Dataset != "recent" || lastSession.ID != c.AfterID {
				mode, readErr = s.adminMode(ctx, tx, c.Dataset, c.AfterID, now)
			}
			if errors.Is(readErr, ErrNotFound) {
				page.ExpiredSkipped++
				c.AfterRecord = 76
				continue
			}
			if readErr != nil {
				return page, readErr
			}
			round, readErr := s.nextAdminRound(ctx, tx, c.Dataset, c.AfterID, mode, c.AfterRecord, &lastSession)
			if errors.Is(readErr, sql.ErrNoRows) {
				c.AfterRecord = 76
				continue
			}
			if readErr != nil {
				return page, readErr
			}
			item = round
			c.AfterRecord = round.RoundNo
		} else {
			p, readErr := s.nextAdminMatch(ctx, tx, c)
			if errors.Is(readErr, sql.ErrNoRows) {
				return page, nil
			}
			if readErr != nil {
				return page, readErr
			}
			c.AfterID, c.AfterSeq, c.PendingExpiry = p.id, p.seq, p.expiry
			if c.Dataset == "recent" && p.expiry <= now {
				page.ExpiredSkipped++
				continue
			}
			match, readErr := s.adminMatch(ctx, tx, c.Dataset, p.id, now, &lastSession)
			if readErr != nil {
				return page, readErr
			}
			item = match
			c.AfterRecord = 0
		}
		raw, encodeErr := Encode(item)
		if encodeErr != nil {
			return page, encodeErr
		}
		if len(page.Items) == 100 || size+len(raw)+1 > 8<<20 {
			c = before
			break
		}
		page.Items = append(page.Items, raw)
		size += len(raw) + 1
	}
	value, err := s.sealAdminCursor(c)
	if err != nil {
		return page, err
	}
	page.NextCursor = &value
	return page, nil
}

func (s *Service) AdminHistory(ctx context.Context, in AdminPageInput) (AdminHistoryPage, error) {
	page := AdminHistoryPage{Dataset: in.Dataset, Items: []AdminSummary{}}
	limit, err := pageLimit(in.Limit, 20, 100)
	if err != nil || s.validateSelection(in.Dataset, in.Selection) != nil {
		return page, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, now, err := s.beginRead(ctx)
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	admin, err := s.authorizeAdmin(ctx, tx)
	if err != nil {
		return page, err
	}
	c, err := s.adminCursor(ctx, tx, admin, now, "list", "", in)
	if err != nil {
		return page, err
	}
	for scanned := 0; scanned < 200; scanned++ {
		p, err := s.nextAdminMatch(ctx, tx, c)
		if errors.Is(err, sql.ErrNoRows) {
			return page, nil
		}
		if err != nil {
			return page, err
		}
		if len(page.Items) == limit {
			break
		}
		c.AfterSeq, c.AfterID = p.seq, p.id
		if in.Dataset == "recent" && p.expiry <= now {
			continue
		}
		match, err := s.adminMatch(ctx, tx, in.Dataset, p.id, now, &sessionRecord{})
		if err != nil {
			return page, err
		}
		f := match.Facts
		page.Items = append(page.Items, AdminSummary{MatchRef: p.id, Game: f.Game, Mode: f.Mode, RulesVersion: f.RulesVersion, ContentHash: f.ContentHash, Outcome: f.Outcome, Reason: f.Reason, Winner: f.Winner, Scores: f.Scores, Ticket: f.Ticket, Prize: f.Prize, Cuts: f.Cuts, Recent: match.Recent})
	}
	value, err := s.sealAdminCursor(c)
	page.NextCursor = &value
	return page, err
}
func (s *Service) AdminDetail(ctx context.Context, dataset, id string) (AdminMatch, error) {
	if s.validateSelection(dataset, AdminSelection{}) != nil {
		return AdminMatch{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, now, err := s.beginRead(ctx)
	if err != nil {
		return AdminMatch{}, err
	}
	defer tx.Rollback()
	if _, err := s.authorizeAdmin(ctx, tx); err != nil {
		return AdminMatch{}, err
	}
	return s.adminMatch(ctx, tx, dataset, id, now, &sessionRecord{})
}
func (s *Service) AdminRounds(ctx context.Context, id string, in AdminPageInput) (RoundPage, error) {
	page := RoundPage{Items: []RoundView{}}
	limit, err := pageLimit(in.Limit, 5, 10)
	if err != nil || s.validateSelection(in.Dataset, in.Selection) != nil {
		return page, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, now, err := s.beginRead(ctx)
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	page.ServerNow = now
	admin, err := s.authorizeAdmin(ctx, tx)
	if err != nil {
		return page, err
	}
	var lastSession sessionRecord
	match, err := s.adminMatch(ctx, tx, in.Dataset, id, now, &lastSession)
	if err != nil {
		return page, err
	}
	c, err := s.adminCursor(ctx, tx, admin, now, "rounds", id, in)
	if err != nil {
		return page, err
	}
	if in.Cursor == "" {
		c.AfterRecord = 0
	}
	size := 8192
	for {
		round, err := s.nextAdminRound(ctx, tx, in.Dataset, id, match.Facts.Mode, c.AfterRecord, &lastSession)
		if errors.Is(err, sql.ErrNoRows) {
			return page, nil
		}
		if err != nil {
			return page, err
		}
		raw, err := Encode(round.Record)
		if err != nil {
			return page, err
		}
		if len(page.Items) == limit || size+len(raw)+1 > 8<<20 {
			break
		}
		page.Items = append(page.Items, round.Record)
		size += len(raw) + 1
		c.AfterRecord = round.RoundNo
	}
	value, err := s.sealAdminCursor(c)
	page.NextCursor = &value
	return page, err
}
