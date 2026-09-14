package duel

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"slices"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
)

type finalizer struct {
	once   sync.Once
	commit func()
}

func (f *finalizer) Commit() bool {
	ran := false
	f.once.Do(func() {
		ran = true
		if f.commit != nil {
			f.commit()
		}
	})
	return ran
}
func (f *finalizer) Abort() bool { ran := false; f.once.Do(func() { ran = true }); return ran }

// CancelUserTx borrows the qualification transaction. Its caller commits the
// returned finalizer only after the ban or deletion and both games commit.
func (s *Service) CancelUserTx(ctx context.Context, tx *sql.Tx, user, now int64) (host.Finalizer, error) {
	if tx == nil || user <= 0 || now < 0 || now > maxDecisionTime {
		return nil, ErrInvalidRequest
	}
	var err error
	now, err = s.cancellationNow(ctx, tx, now)
	if err != nil {
		return nil, err
	}
	facts, err := s.cancelUser(ctx, tx, user, now)
	if err != nil {
		return nil, err
	}
	return &finalizer{commit: func() { s.publish(ctx, facts) }}, nil
}
func (s *Service) cancelUser(ctx context.Context, tx *sql.Tx, user, now int64) (activities.PublishFacts, error) {
	var qid, sid sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT queue_id,session_id FROM game_duel_user_slots WHERE user_id=? AND game_key=?`, user, s.rules.ID()).Scan(&qid, &sid)
	if errors.Is(err, sql.ErrNoRows) {
		return activities.PublishFacts{}, nil
	}
	if err != nil {
		return activities.PublishFacts{}, err
	}
	if qid.Valid {
		q, err := s.queue(ctx, tx, qid.String)
		if err != nil {
			return activities.PublishFacts{}, err
		}
		err = s.releaseQueue(ctx, tx, q, 0, now)
		return activities.PublishFacts{AccountIDs: []int64{user}}, err
	}
	if !sid.Valid {
		return activities.PublishFacts{}, ErrInvariant
	}
	v, err := s.session(ctx, tx, sid.String)
	if err != nil {
		return activities.PublishFacts{}, err
	}
	expected := v.Revision
	v.Revision, err = increment(v.Revision)
	if err != nil {
		return activities.PublishFacts{}, err
	}
	v.PhaseSeq, err = increment(v.PhaseSeq)
	if err != nil {
		return activities.PublishFacts{}, err
	}
	return s.terminal(ctx, tx, &v, expected, now, nil, "account_unavailable", true)
}
func (s *Service) PrepareDeleteTx(ctx context.Context, tx *sql.Tx, user, now int64) (host.Finalizer, error) {
	if tx == nil || user <= 0 || now < 0 || now > maxDecisionTime {
		return nil, ErrInvalidRequest
	}
	var err error
	now, err = s.cancellationNow(ctx, tx, now)
	if err != nil {
		return nil, err
	}
	facts, err := s.cancelUser(ctx, tx, user, now)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.session_id FROM game_duel_seats p JOIN game_duel_sessions g ON g.id=p.session_id WHERE p.user_id=? AND g.game_key=?`, user, s.rules.ID())
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
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
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE game_duel_seats SET user_id=NULL WHERE session_id=? AND user_id=?`, id, user); err != nil {
			return nil, err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_duel_seats WHERE session_id=? AND user_id IS NOT NULL`, id).Scan(&count); err != nil {
			return nil, err
		}
		if count == 0 {
			v, err := s.session(ctx, tx, id)
			if err != nil {
				return nil, err
			}
			if err := s.anonymize(ctx, tx, v); err != nil {
				return nil, err
			}
		}
	}
	facts.AccountIDs = slices.DeleteFunc(facts.AccountIDs, func(id int64) bool { return id == user })
	return &finalizer{commit: func() { s.actionMu.Lock(); delete(s.actions, user); s.actionMu.Unlock(); s.publish(ctx, facts) }}, nil
}
func (s *Service) cancellationNow(ctx context.Context, tx *sql.Tx, provided int64) (int64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE game_duel_user_slots SET game_key=game_key WHERE 0`); err != nil {
		return 0, err
	}
	now := s.now().UTC().Unix()
	if now < 0 || now > maxDecisionTime {
		return 0, ErrInvariant
	}
	return max(now, provided), nil
}

type anonymousHeader struct {
	Game             string             `json:"game"`
	Mode             string             `json:"mode"`
	RulesVersion     int                `json:"rules_version"`
	ContentHash      string             `json:"content_hash"`
	Initial          json.RawMessage    `json:"initial"`
	Final            json.RawMessage    `json:"final"`
	TerminalActions  [2]json.RawMessage `json:"terminal_actions"`
	RoundStartEvents json.RawMessage    `json:"round_start_events"`
	Outcome          string             `json:"outcome"`
	Reason           string             `json:"reason"`
	Winner           *int               `json:"winner"`
	Scores           [2]int64           `json:"scores"`
	Ticket           string             `json:"ticket"`
	Rake             Rates              `json:"rake_bp"`
	Prize            string             `json:"prize"`
	Cuts             RakeAmounts        `json:"cuts"`
}

func (s *Service) anonymousHeader(v sessionRecord) (anonymousHeader, error) {
	initial, err := s.rules.Archive(v.Mode, v.Initial)
	if err != nil {
		return anonymousHeader{}, err
	}
	final, err := s.rules.Archive(v.Mode, v.Payload.Rules)
	if err != nil {
		return anonymousHeader{}, err
	}
	return anonymousHeader{Game: s.rules.ID(), Mode: v.Mode, RulesVersion: 1, ContentHash: v.Terms.ContentHash, Initial: initial, Final: final, TerminalActions: v.Payload.TerminalActions, RoundStartEvents: v.Payload.RoundStartEvents, Outcome: v.Outcome, Reason: v.Reason, Winner: v.Winner, Scores: v.Scores, Ticket: v.Terms.Ticket, Rake: v.Terms.Rake, Prize: game.FormatAmount(v.Prize), Cuts: RakeAmounts{Platform: game.FormatAmount(v.Platform), Welfare: game.FormatAmount(v.Welfare), Thursday: game.FormatAmount(v.Thursday)}}, nil
}
func (s *Service) anonymize(ctx context.Context, tx *sql.Tx, v sessionRecord) error {
	if v.State != "terminal" {
		return ErrInvariant
	}
	header, err := s.anonymousHeader(v)
	if err != nil {
		return err
	}
	body, err := Encode(header)
	if err != nil {
		return err
	}
	archive, err := s.generate("dah_")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO game_duel_anonymous(archive_id,game_key,mode,content_hash,header_json) VALUES(?,?,?,?,?)`, archive, s.rules.ID(), v.Mode, v.Terms.ContentHash, string(body)); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT round_no,record_json FROM game_duel_rounds WHERE session_id=? ORDER BY round_no`, v.ID)
	if err != nil {
		return err
	}
	type item struct {
		round int
		body  []byte
	}
	records := []item{}
	for rows.Next() {
		var value item
		if err := rows.Scan(&value.round, &value.body); err != nil {
			rows.Close()
			return err
		}
		records = append(records, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, record := range records {
		view, err := s.roundView(v.Mode, record.body, 0, true)
		if err != nil {
			return err
		}
		encoded, err := Encode(view)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_duel_anonymous_rounds(archive_id,round_no,record_json) VALUES(?,?,?)`, archive, record.round, string(encoded)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_duel_rounds WHERE session_id=?`, v.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_duel_seats WHERE session_id=?`, v.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM game_duel_sessions WHERE id=? AND state='terminal'`, v.ID)
	return err
}
func (s *Service) Retain(ctx context.Context, now int64, limit int, deadline time.Time) (WorkResult, error) {
	if now < 0 || now > maxDecisionTime || limit < 1 || limit > 100 || deadline.IsZero() {
		return WorkResult{}, ErrInvalidRequest
	}
	rows, err := s.database.QueryContext(ctx, `SELECT id FROM game_duel_sessions WHERE game_key=? AND state='terminal' AND delete_at<=? ORDER BY delete_at,id LIMIT ?`, s.rules.ID(), now, limit+1)
	if err != nil {
		return WorkResult{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return WorkResult{}, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return WorkResult{}, err
	}
	result := WorkResult{More: len(ids) > limit}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	// Random insertion order prevents export_seq from encoding terminal order.
	for i := len(ids) - 1; i > 0; i-- {
		pick, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return result, ErrUnavailable
		}
		j := int(pick.Int64())
		ids[i], ids[j] = ids[j], ids[i]
	}
	for _, id := range ids {
		if !time.Now().Before(deadline) {
			result.More = true
			break
		}
		err := func() error {
			tx, actual, err := s.begin(ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			v, err := s.session(ctx, tx, id)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if v.State != "terminal" || v.DeleteAt == nil || *v.DeleteAt > actual {
				return nil
			}
			if err := s.anonymize(ctx, tx, v); err != nil {
				return err
			}
			return tx.Commit()
		}()
		if err != nil {
			return result, err
		}
		result.Processed++
	}
	return result, nil
}

type Export struct {
	Queue         *Queue        `json:"queue"`
	Current       *State        `json:"current"`
	CurrentRounds []RoundView   `json:"current_rounds"`
	History       []ExportMatch `json:"history"`
}
type ExportMatch struct {
	Detail HistoryDetail `json:"detail"`
	Rounds []RoundView   `json:"rounds"`
}

func (s *Service) ExportTx(ctx context.Context, tx *sql.Tx, user, now int64, limit int) (any, host.Finalizer, error) {
	if tx == nil || user <= 0 || now < 0 || limit < 1 || limit > 10000 {
		return nil, nil, ErrInvalidRequest
	}
	result := Export{CurrentRounds: []RoundView{}, History: []ExportMatch{}}
	remaining, size := limit, 0
	charge := func(value any) error {
		body, err := json.Marshal(value)
		if err != nil {
			return ErrInvariant
		}
		remaining--
		size += len(body)
		if remaining < 0 || size > 16<<20 {
			return ErrResourceLimit
		}
		return nil
	}
	var qid, sid sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT queue_id,session_id FROM game_duel_user_slots WHERE user_id=? AND game_key=?`, user, s.rules.ID()).Scan(&qid, &sid)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, err
	}
	if qid.Valid {
		q, err := s.queue(ctx, tx, qid.String)
		if err != nil {
			return nil, nil, err
		}
		if q.Deadline > now {
			result.Queue = projectQueue(q)
			if err := charge(result.Queue); err != nil {
				return nil, nil, err
			}
		}
	}
	roundList := func(v sessionRecord, seat int) ([]RoundView, error) {
		rows, err := tx.QueryContext(ctx, `SELECT record_json FROM game_duel_rounds WHERE session_id=? ORDER BY round_no`, v.ID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []RoundView{}
		for rows.Next() {
			var body []byte
			if err := rows.Scan(&body); err != nil {
				return nil, err
			}
			item, err := s.roundView(v.Mode, body, seat, v.State == "terminal")
			if err != nil {
				return nil, err
			}
			if err := charge(item); err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, rows.Err()
	}
	if sid.Valid {
		v, err := s.session(ctx, tx, sid.String)
		if err != nil {
			return nil, nil, err
		}
		seat, err := s.seat(&v, user)
		if err != nil {
			return nil, nil, err
		}
		result.Current, err = s.projectState(v, seat, now)
		if err != nil {
			return nil, nil, err
		}
		if err := charge(result.Current); err != nil {
			return nil, nil, err
		}
		result.CurrentRounds, err = roundList(v, seat)
		if err != nil {
			return nil, nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT g.id FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE p.user_id=? AND g.game_key=? AND g.state='terminal' AND g.delete_at>? ORDER BY g.terminal_at,g.id LIMIT ?`, user, s.rules.ID(), now, limit+1)
	if err != nil {
		return nil, nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	if len(ids) > remaining {
		return nil, nil, ErrResourceLimit
	}
	for _, id := range ids {
		v, err := s.session(ctx, tx, id)
		if err != nil {
			return nil, nil, err
		}
		seat, err := s.seat(&v, user)
		if err != nil {
			return nil, nil, err
		}
		detail, err := s.detail(v, seat)
		if err != nil {
			return nil, nil, err
		}
		if err := charge(detail); err != nil {
			return nil, nil, err
		}
		rounds, err := roundList(v, seat)
		if err != nil {
			return nil, nil, err
		}
		result.History = append(result.History, ExportMatch{Detail: detail, Rounds: rounds})
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, nil, ErrInvariant
	}
	if len(body) > 16<<20 {
		return nil, nil, ErrResourceLimit
	}
	return result, nil, nil
}
