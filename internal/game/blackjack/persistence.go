package blackjack

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
)

type entryRecord struct {
	Ordinal    int64
	ID         string
	User       sql.NullInt64
	State      string
	Stake      int64
	Rates      config.Rates
	CreatedAt  int64
	ResolvedAt sql.NullInt64
	Session    sql.NullString
	Seat       sql.NullInt64
	Pending    sql.NullString
	Batch      sql.NullInt64
	Stopped    bool
	Emote      sql.NullString
	EmoteAt    sql.NullInt64
}
type sessionRecord struct {
	ID                  string
	StartedAt           int64
	Phase               string
	Revision, LastBatch int64
	StateJSON, ViewJSON sql.NullString
	TerminalAt          sql.NullInt64
	Reason              sql.NullString
}
type scanner interface{ Scan(...any) error }

const entryColumns = `ordinal,id,user_id,state,stake_milli,platform_bp,welfare_bp,thursday_bp,created_at,resolved_at,session_id,seat_no,pending_json,pending_batch,stopped,emote,emote_at`
const sessionColumns = `id,started_at,phase,revision,last_batch,state_json,view_json,terminal_at,reason`

func scanEntry(row scanner) (entryRecord, error) {
	var e entryRecord
	err := row.Scan(&e.Ordinal, &e.ID, &e.User, &e.State, &e.Stake, &e.Rates.Platform, &e.Rates.Welfare, &e.Rates.Thursday, &e.CreatedAt, &e.ResolvedAt, &e.Session, &e.Seat, &e.Pending, &e.Batch, &e.Stopped, &e.Emote, &e.EmoteAt)
	return e, err
}
func scanSession(row scanner) (sessionRecord, error) {
	var s sessionRecord
	err := row.Scan(&s.ID, &s.StartedAt, &s.Phase, &s.Revision, &s.LastBatch, &s.StateJSON, &s.ViewJSON, &s.TerminalAt, &s.Reason)
	return s, err
}
func readEntry(ctx context.Context, tx *sql.Tx, id string) (entryRecord, error) {
	return scanEntry(tx.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM game_blackjack_entries WHERE id=?`, id))
}
func currentEntry(ctx context.Context, tx *sql.Tx, user int64) (entryRecord, error) {
	return scanEntry(tx.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM game_blackjack_entries WHERE user_id=? AND state IN ('waiting','seated','playing')`, user))
}
func readSession(ctx context.Context, tx *sql.Tx, id string) (sessionRecord, error) {
	return scanSession(tx.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM game_blackjack_sessions WHERE id=?`, id))
}
func currentSession(ctx context.Context, tx *sql.Tx) (sessionRecord, error) {
	return scanSession(tx.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM game_blackjack_sessions WHERE phase IN ('seating','decision')`))
}
func entries(ctx context.Context, tx *sql.Tx, where string, args ...any) ([]entryRecord, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+entryColumns+` FROM game_blackjack_entries WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []entryRecord{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
func sessionEntries(ctx context.Context, tx *sql.Tx, id string) ([]entryRecord, error) {
	return entries(ctx, tx, `session_id=? AND state<>'released' ORDER BY seat_no`, id)
}
func decodeState(v sessionRecord) (engine.State, error) {
	var s engine.State
	if !v.StateJSON.Valid || json.Unmarshal([]byte(v.StateJSON.String), &s) != nil || s.Validate() != nil {
		return s, ErrInvariant
	}
	return s, nil
}
func termsView(e entryRecord, now int64) SeatTerms {
	v := SeatTerms{Seat: int(e.Seat.Int64), Stake: game.FormatAmount(e.Stake), Rake: e.Rates, Stopped: e.Stopped}
	if e.Emote.Valid && e.EmoteAt.Valid && now < e.EmoteAt.Int64+4 {
		v.Emote = e.Emote.String
		at := e.EmoteAt.Int64
		v.EmoteAt = &at
	}
	return v
}
func factFor(s *engine.State, list []entryRecord, now int64) (TableFact, error) {
	f := TableFact{Seats: []SeatTerms{}, Settlements: []SeatSettlement{}}
	if s != nil {
		view, err := engine.Project(*s)
		if err != nil {
			return f, err
		}
		f.Cards = &view
	}
	for _, e := range list {
		f.Seats = append(f.Seats, termsView(e, now))
	}
	return f, nil
}
func projectTable(ctx context.Context, tx *sql.Tx, v sessionRecord, now int64) (TableView, error) {
	result := TableView{ID: v.ID, Revision: strconv.FormatInt(v.Revision, 10), StartedAt: v.StartedAt, Phase: v.Phase, NextRoundAt: v.StartedAt + 60, Reason: v.Reason.String}
	switch v.Phase {
	case "seating":
		result.Deadline = v.StartedAt + 15
	case "decision":
		result.Deadline = v.StartedAt + 45
	default:
		result.Deadline = v.StartedAt + 60
	}
	if v.TerminalAt.Valid {
		at := v.TerminalAt.Int64
		result.TerminalAt = &at
	}
	if v.Phase == "result" || v.Phase == "cancelled" {
		if !v.ViewJSON.Valid || json.Unmarshal([]byte(v.ViewJSON.String), &result.Fact) != nil {
			return result, ErrInvariant
		}
		for i := range result.Fact.Seats {
			result.Fact.Seats[i].Emote, result.Fact.Seats[i].EmoteAt = "", nil
		}
		if now < v.StartedAt+60 {
			list, err := sessionEntries(ctx, tx, v.ID)
			if err != nil {
				return result, err
			}
			for _, e := range list {
				current := termsView(e, now)
				for i := range result.Fact.Seats {
					if result.Fact.Seats[i].Seat == current.Seat {
						result.Fact.Seats[i].Emote, result.Fact.Seats[i].EmoteAt = current.Emote, current.EmoteAt
					}
				}
			}
		}
		return result, nil
	}
	list, err := sessionEntries(ctx, tx, v.ID)
	if err != nil {
		return result, err
	}
	var state *engine.State
	if v.Phase == "decision" {
		s, err := decodeState(v)
		if err != nil {
			return result, err
		}
		state = &s
	}
	result.Fact, err = factFor(state, list, now)
	return result, err
}
func saveState(ctx context.Context, tx *sql.Tx, v *sessionRecord, s engine.State, fact TableFact, at int64, kind string) error {
	state, err := marshal(s)
	if err != nil {
		return err
	}
	view, err := marshal(fact)
	if err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, `UPDATE game_blackjack_sessions SET phase='decision',revision=revision+1,last_batch=?,state_json=?,view_json=? WHERE id=? AND revision=? AND phase IN ('seating','decision')`, at, string(state), string(view), v.ID, v.Revision)
	if err != nil {
		return err
	}
	if n, err := r.RowsAffected(); err != nil || n != 1 {
		return ErrConflict
	}
	v.Revision++
	v.Phase = "decision"
	v.LastBatch = at
	v.StateJSON = sql.NullString{String: string(state), Valid: true}
	v.ViewJSON = sql.NullString{String: string(view), Valid: true}
	return appendEvent(ctx, tx, v.ID, at, kind, view)
}
func appendEvent(ctx context.Context, tx *sql.Tx, id string, at int64, kind string, view []byte) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO game_blackjack_events(session_id,seq,occurred_at,kind,public_json) SELECT ?,COALESCE(MAX(seq),0)+1,?,?,? FROM game_blackjack_events WHERE session_id=?`, id, at, kind, string(view), id)
	return err
}
func queuePosition(ctx context.Context, tx *sql.Tx, e entryRecord) (int64, error) {
	if e.State != "waiting" {
		return 0, nil
	}
	var n int64
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_blackjack_entries WHERE state='waiting' AND ordinal<=?`, e.Ordinal).Scan(&n)
	return n, err
}
func paymentTotals(ctx context.Context, tx *sql.Tx, entry string) (int64, int64, error) {
	var total, game int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount_milli),0),COALESCE(SUM(game_paid_milli),0) FROM game_blackjack_payments WHERE entry_id=?`, entry).Scan(&total, &game)
	return total, game, err
}
func noRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
