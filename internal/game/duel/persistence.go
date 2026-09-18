package duel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

type scanner interface{ Scan(...any) error }

func validRates(r Rates) bool {
	return r.Platform >= 0 && r.Platform < 10000 && r.Welfare >= 0 && r.Welfare < 10000 && r.Thursday >= 0 && r.Thursday < 10000 && r.Platform+r.Welfare+r.Thursday < 10000
}

const queueColumns = `id,mode,user_id,revision,created_at,deadline,terms_json,terms_hash,content_hash,ticket_milli,game_paid_milli,reservation_operation_id,general_account_id,game_account_id,ledger_rows_remaining,device_hash,ip_hash,loadout_json`

func (s *Service) scanQueue(row scanner) (queueRecord, error) {
	var q queueRecord
	var revision, remaining, device, ip []byte
	var terms, content string
	var loadout sql.NullString
	err := row.Scan(&q.ID, &q.Mode, &q.User, &revision, &q.Created, &q.Deadline, &terms, &q.TermsHash, &content, &q.Ticket, &q.GamePaid, &q.Operation, &q.GeneralAccount, &q.GameAccount, &remaining, &device, &ip, &loadout)
	if err != nil {
		return q, err
	}
	q.Revision, err = db.DecodeU128(revision)
	if err != nil || q.Revision.Big().Sign() <= 0 || len(device) != 32 || len(ip) != 32 {
		return q, ErrInvariant
	}
	hold, err := db.DecodeU128(remaining)
	if err != nil || hold != one() {
		return q, ErrInvariant
	}
	if Decode([]byte(terms), &q.Terms) != nil || digest([]byte(terms)) != q.TermsHash || q.Terms.Game != s.rules.ID() || q.Terms.Mode != q.Mode || q.Terms.ContentHash != content || q.Terms.RulesVersion != 1 || q.Terms.Ticket != game.FormatAmount(q.Ticket) {
		return q, ErrInvariant
	}
	if !db.ValidateOpaqueID(q.ID, s.queuePrefix) || q.User <= 0 || q.Deadline != q.Created+QueueSeconds || q.GamePaid < 0 || q.GamePaid > q.Ticket || !validRates(q.Terms.Rake) {
		return q, ErrInvariant
	}
	copy(q.Device[:], device)
	copy(q.IP[:], ip)
	if loadout.Valid {
		q.Loadout = json.RawMessage(loadout.String)
	}
	if _, err := s.rules.Loadout(q.Mode, q.Loadout); err != nil {
		return q, ErrInvariant
	}
	return q, nil
}
func (s *Service) queue(ctx context.Context, tx *sql.Tx, id string) (queueRecord, error) {
	return s.scanQueue(tx.QueryRowContext(ctx, `SELECT `+queueColumns+` FROM game_duel_queue WHERE game_key=? AND id=?`, s.rules.ID(), id))
}
func (s *Service) insertQueue(ctx context.Context, tx *sql.Tx, q queueRecord) error {
	terms, err := Encode(q.Terms)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO game_duel_queue(id,game_key,mode,user_id,revision,created_at,deadline,terms_json,terms_hash,content_hash,ticket_milli,game_paid_milli,reservation_operation_id,general_account_id,game_account_id,ledger_rows_remaining,device_hash,ip_hash,loadout_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, q.ID, s.rules.ID(), q.Mode, q.User, db.EncodeU128(q.Revision), q.Created, q.Deadline, string(terms), q.TermsHash, q.Terms.ContentHash, q.Ticket, q.GamePaid, q.Operation, q.GeneralAccount, q.GameAccount, db.EncodeU128(one()), q.Device[:], q.IP[:], nullableJSON(q.Loadout))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO game_duel_user_slots(user_id,game_key,queue_id) VALUES(?,?,?)`, q.User, s.rules.ID(), q.ID)
	return err
}

const sessionColumns = `id,mode,state,phase,terms_json,terms_hash,content_hash,ticket_milli,platform_bp,welfare_bp,thursday_bp,revision,phase_seq,round,started_at,phase_deadline,general_account_id,game_account_id,ledger_rows_remaining,server_state_json,initial_state_json,terminal_at,delete_at,outcome,reason,winner_seat,score0,score1,prize_milli,platform_milli,welfare_milli,thursday_milli,terminal_operation_id`

func (s *Service) scanSession(row scanner) (sessionRecord, error) {
	var v sessionRecord
	var terms, content, payload, initial string
	var revision, phaseSeq, remaining []byte
	var rates Rates
	var deadline, terminal, expiry, winner, score0, score1, prize, platform, welfare, thursday sql.NullInt64
	var outcome, reason, operation sql.NullString
	err := row.Scan(&v.ID, &v.Mode, &v.State, &v.Phase, &terms, &v.TermsHash, &content, &v.Ticket, &rates.Platform, &rates.Welfare, &rates.Thursday, &revision, &phaseSeq, &v.Round, &v.Started, &deadline, &v.GeneralAccount, &v.GameAccount, &remaining, &payload, &initial, &terminal, &expiry, &outcome, &reason, &winner, &score0, &score1, &prize, &platform, &welfare, &thursday, &operation)
	if err != nil {
		return v, err
	}
	v.Revision, err = db.DecodeU128(revision)
	if err != nil || v.Revision.Big().Sign() <= 0 {
		return v, ErrInvariant
	}
	v.PhaseSeq, err = db.DecodeU128(phaseSeq)
	if err != nil || v.PhaseSeq.Big().Sign() <= 0 {
		return v, ErrInvariant
	}
	if Decode([]byte(terms), &v.Terms) != nil || digest([]byte(terms)) != v.TermsHash || v.Terms.Game != s.rules.ID() || v.Terms.Mode != v.Mode || v.Terms.ContentHash != content || v.Terms.RulesVersion != 1 || v.Terms.Ticket != game.FormatAmount(v.Ticket) || v.Terms.Rake != rates || Decode([]byte(payload), &v.Payload) != nil {
		return v, ErrInvariant
	}
	v.Initial = json.RawMessage(initial)
	if !db.ValidateOpaqueID(v.ID, s.sessionPrefix) || !validRates(rates) {
		return v, ErrInvariant
	}
	if v.Payload.RoundStartedAt != nil {
		if s.rules.ID() != "likes" || v.Round < 2 || *v.Payload.RoundStartedAt < v.Started || *v.Payload.RoundStartedAt > maxDecisionTime || len(v.Payload.RoundStartEvents) == 0 || string(v.Payload.RoundStartEvents) == "null" {
			return v, ErrInvariant
		}
	} else if len(v.Payload.RoundStartEvents) != 0 && string(v.Payload.RoundStartEvents) != "null" {
		return v, ErrInvariant
	}
	info, err := s.rules.Inspect(v.Mode, v.Payload.Rules)
	if err != nil || info.Round != v.Round {
		return v, ErrInvariant
	}
	if _, err := s.rules.Inspect(v.Mode, v.Initial); err != nil {
		return v, ErrInvariant
	}
	hold, err := db.DecodeU128(remaining)
	if err != nil {
		return v, ErrInvariant
	}
	if v.State == "active" {
		if info.Phase != v.Phase || info.Result != nil || !deadline.Valid || hold != one() || terminal.Valid {
			return v, ErrInvariant
		}
		v.Deadline = &deadline.Int64
	} else if v.State == "terminal" {
		if v.Phase != "terminal" || deadline.Valid || hold != (db.U128{}) || !terminal.Valid || !expiry.Valid || expiry.Int64 != terminal.Int64+RetentionSeconds || !outcome.Valid || !reason.Valid || !operation.Valid || !score0.Valid || !score1.Valid || !prize.Valid || !platform.Valid || !welfare.Valid || !thursday.Valid {
			return v, ErrInvariant
		}
		v.TerminalAt = &terminal.Int64
		v.DeleteAt = &expiry.Int64
		v.Outcome = outcome.String
		v.Reason = reason.String
		v.Operation = operation.String
		if winner.Valid {
			n := int(winner.Int64)
			v.Winner = &n
		}
		v.Scores = [2]int64{score0.Int64, score1.Int64}
		v.Prize = prize.Int64
		v.Platform = platform.Int64
		v.Welfare = welfare.Int64
		v.Thursday = thursday.Int64
		if v.Scores != info.Scores {
			return v, ErrInvariant
		}
	} else {
		return v, ErrInvariant
	}
	if r := v.Payload.Resolution; r != nil {
		duration, durationErr := s.presentationDuration(r.Summary)
		if s.rules.ID() != "likes" || durationErr != nil || duration <= 0 || r.Round < 1 || r.Round > v.Round || r.EndsAt <= r.StartedAt || r.EndsAt-r.StartedAt != duration || r.StartedAt < v.Started || len(r.Summary) == 0 || len(r.Summary) > 128<<10 {
			return v, ErrInvariant
		}
		if v.Phase == "settlement" && (r.Round != v.Round || *v.Deadline != r.EndsAt) {
			return v, ErrInvariant
		}
	} else if v.Phase == "settlement" {
		return v, ErrInvariant
	}
	return v, nil
}
func (s *Service) session(ctx context.Context, tx *sql.Tx, id string) (sessionRecord, error) {
	v, err := s.scanSession(tx.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM game_duel_sessions WHERE game_key=? AND id=?`, s.rules.ID(), id))
	if err != nil {
		return v, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT seat_no,user_id,general_paid_milli,game_paid_milli,loadout_json,current_plan_json,locked,timeout_count FROM game_duel_seats WHERE session_id=? ORDER BY seat_no`, id)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var seat, locked int
		var user sql.NullInt64
		var loadout, action sql.NullString
		var p seatRecord
		if err := rows.Scan(&seat, &user, &p.GeneralPaid, &p.GamePaid, &loadout, &action, &locked, &p.TimeoutCount); err != nil {
			return v, err
		}
		if seat != count || count >= 2 || p.GeneralPaid+p.GamePaid != v.Ticket || p.GeneralPaid < 0 || p.GamePaid < 0 || (locked == 1) != action.Valid || locked < 0 || locked > 1 || p.TimeoutCount < 0 || p.TimeoutCount > 150 {
			return v, ErrInvariant
		}
		if user.Valid {
			p.User = &user.Int64
		} else if v.State == "active" {
			return v, ErrInvariant
		}
		if loadout.Valid {
			p.Loadout = json.RawMessage(loadout.String)
		}
		if action.Valid {
			p.Action = json.RawMessage(action.String)
		}
		p.Locked = locked == 1
		v.Seats[seat] = p
		count++
	}
	if rows.Err() != nil {
		return v, rows.Err()
	}
	if count != 2 {
		return v, ErrInvariant
	}
	return v, nil
}
func (s *Service) insertSession(ctx context.Context, tx *sql.Tx, v sessionRecord) error {
	terms, err := Encode(v.Terms)
	if err != nil {
		return err
	}
	payload, err := Encode(v.Payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO game_duel_sessions(id,game_key,mode,content_hash,terms_json,terms_hash,ticket_milli,platform_bp,welfare_bp,thursday_bp,state,phase,round,revision,phase_seq,started_at,phase_deadline,general_account_id,game_account_id,ledger_rows_remaining,server_state_json,initial_state_json) VALUES(?,?,?,?,?,?,?,?,?,?,'active',?,?,?,?,?,?,?,?,?,?,?)`, v.ID, s.rules.ID(), v.Mode, v.Terms.ContentHash, string(terms), v.TermsHash, v.Ticket, v.Terms.Rake.Platform, v.Terms.Rake.Welfare, v.Terms.Rake.Thursday, v.Phase, v.Round, db.EncodeU128(v.Revision), db.EncodeU128(v.PhaseSeq), v.Started, nullable(v.Deadline), v.GeneralAccount, v.GameAccount, db.EncodeU128(one()), string(payload), string(v.Initial))
	if err != nil {
		return err
	}
	for seat, p := range v.Seats {
		_, err = tx.ExecContext(ctx, `INSERT INTO game_duel_seats(session_id,seat_no,user_id,general_paid_milli,game_paid_milli,loadout_json,current_plan_json,locked,timeout_count) VALUES(?,?,?,?,?,?,?,?,?)`, v.ID, seat, nullable(p.User), p.GeneralPaid, p.GamePaid, nullableJSON(p.Loadout), nullableJSON(p.Action), p.Locked, p.TimeoutCount)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) saveSession(ctx context.Context, tx *sql.Tx, v *sessionRecord, expected db.U128) error {
	payload, err := Encode(v.Payload)
	if err != nil {
		return err
	}
	var hold db.U128
	if v.State == "active" {
		hold = one()
	}
	var outcome, reason, operation any
	var score0, score1, prize, platform, welfare, thursday any
	if v.State == "terminal" {
		outcome = v.Outcome
		reason = v.Reason
		operation = v.Operation
		score0 = v.Scores[0]
		score1 = v.Scores[1]
		prize = v.Prize
		platform = v.Platform
		welfare = v.Welfare
		thursday = v.Thursday
	}
	result, err := tx.ExecContext(ctx, `UPDATE game_duel_sessions SET state=?,phase=?,round=?,revision=?,phase_seq=?,phase_deadline=?,ledger_rows_remaining=?,server_state_json=?,terminal_at=?,delete_at=?,outcome=?,reason=?,winner_seat=?,score0=?,score1=?,prize_milli=?,platform_milli=?,welfare_milli=?,thursday_milli=?,terminal_operation_id=? WHERE id=? AND game_key=? AND revision=? AND state='active'`, v.State, v.Phase, v.Round, db.EncodeU128(v.Revision), db.EncodeU128(v.PhaseSeq), nullable(v.Deadline), db.EncodeU128(hold), string(payload), nullable(v.TerminalAt), nullable(v.DeleteAt), outcome, reason, nullable(v.Winner), score0, score1, prize, platform, welfare, thursday, operation, v.ID, s.rules.ID(), db.EncodeU128(expected))
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrConflict
	}
	for seat, p := range v.Seats {
		result, err = tx.ExecContext(ctx, `UPDATE game_duel_seats SET current_plan_json=?,locked=?,timeout_count=? WHERE session_id=? AND seat_no=?`, nullableJSON(p.Action), p.Locked, p.TimeoutCount, v.ID, seat)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return ErrInvariant
		}
	}
	return nil
}
func (s *Service) seat(v *sessionRecord, user int64) (int, error) {
	for seat, p := range v.Seats {
		if p.User != nil && *p.User == user {
			return seat, nil
		}
	}
	return -1, ErrNotFound
}
func nullable[T any](v *T) any {
	if v == nil {
		return nil
	}
	return *v
}
func nullableJSON(v json.RawMessage) any {
	if len(v) == 0 {
		return nil
	}
	return string(v)
}
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return classify(err)
}
func (s *Service) ensureCatalog(ctx context.Context, tx *sql.Tx, mode string) error {
	c, err := s.rules.Catalog(mode)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO game_duel_catalogs(game_key,content_hash,rules_version,design_version,schema_version,catalog_json) VALUES(?,?,1,?,?,?) ON CONFLICT(game_key,content_hash) DO NOTHING`, s.rules.ID(), c.Hash, c.DesignVersion, c.SchemaVersion, string(c.JSON))
	if err != nil {
		return err
	}
	var body string
	err = tx.QueryRowContext(ctx, `SELECT catalog_json FROM game_duel_catalogs WHERE game_key=? AND content_hash=?`, s.rules.ID(), c.Hash).Scan(&body)
	if err != nil {
		return err
	}
	if body != string(c.JSON) {
		return ErrInvariant
	}
	return nil
}
