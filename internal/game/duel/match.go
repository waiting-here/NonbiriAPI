package duel

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (s *Service) match(ctx context.Context, tx *sql.Tx, now int64) (bool, error) {
	maintenance, err := maintenanceOn(ctx, tx)
	if err != nil || maintenance {
		return false, err
	}
	cfg, err := s.config(ctx, tx)
	if err != nil || !cfg.Enabled {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+queueColumns+` FROM game_duel_queue q WHERE game_key=? AND deadline>? AND EXISTS(SELECT 1 FROM users u WHERE u.id=q.user_id AND u.is_admin=0 AND u.discord_id IS NOT NULL AND u.discord_id<>'' AND (u.is_banned=0 OR u.banned_until<=?)) ORDER BY created_at,id LIMIT 4096`, s.rules.ID(), now, now)
	if err != nil {
		return false, err
	}
	queues := []queueRecord{}
	for rows.Next() {
		q, err := s.scanQueue(rows)
		if err != nil {
			rows.Close()
			return false, err
		}
		if cfg.Modes[q.Mode].Enabled {
			queues = append(queues, q)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for i, a := range queues {
		chosen := -1
		for j := i + 1; j < len(queues); j++ {
			b := queues[j]
			if a.Mode != b.Mode || a.TermsHash != b.TermsHash || a.Device == b.Device {
				continue
			}
			if chosen < 0 {
				chosen = j
			}
			if a.IP != b.IP {
				chosen = j
				break
			}
		}
		if chosen < 0 {
			continue
		}
		pair := [2]queueRecord{a, queues[chosen]}
		return true, s.startSession(ctx, tx, pair, now)
	}
	return false, nil
}
func (s *Service) startSession(ctx context.Context, tx *sql.Tx, queues [2]queueRecord, now int64) error {
	selected, err := s.rulesFor(queues[0].Mode, queues[0].Terms.ContentHash)
	if err != nil {
		return err
	}
	id, err := s.generate(s.sessionPrefix)
	if err != nil {
		return err
	}
	meta, err := s.meta(0, now)
	if err != nil {
		return err
	}
	secret, err := randomness.New(s.rules.ID(), id, queues[0].Mode+"/"+queues[0].Terms.ContentHash, nil)
	if err != nil {
		return err
	}
	seatRandom, err := secret.Stream("seat-order")
	if err != nil {
		return err
	}
	seat, err := seatRandom.Uint64n(2)
	if err != nil {
		return err
	}
	if seat != 0 {
		queues[0], queues[1] = queues[1], queues[0]
	}
	loadouts := [2]json.RawMessage{queues[0].Loadout, queues[1].Loadout}
	var state json.RawMessage
	if rules, ok := selected.(interface {
		CreateWithRandom(string, [2]json.RawMessage, io.Reader) (json.RawMessage, error)
	}); ok {
		stream, streamErr := secret.Stream("initial")
		if streamErr != nil {
			return streamErr
		}
		state, err = rules.CreateWithRandom(queues[0].Mode, loadouts, stream)
	} else {
		state, err = selected.Create(queues[0].Mode, loadouts)
	}
	if err != nil {
		return err
	}
	v := sessionRecord{ID: id, Mode: queues[0].Mode, State: "active", Terms: queues[0].Terms, TermsHash: queues[0].TermsHash, Ticket: queues[0].Ticket, Revision: one(), PhaseSeq: one(), Started: now, Initial: state, Payload: storedPayload{Rules: state}}
	v.rules = selected
	var inputs [2]finance.QueueInput
	for seat, q := range queues {
		user := q.User
		v.Seats[seat] = seatRecord{User: &user, GeneralPaid: q.Ticket - q.GamePaid, GamePaid: q.GamePaid, Loadout: q.Loadout}
		inputs[seat] = finance.QueueInput{QueueID: q.ID, UserID: q.User, Amount: ledger.AmountFromMilli(q.Ticket), GamePaid: ledger.AmountFromMilli(q.GamePaid)}
	}
	if err := s.enterPhase(&v, now, false); err != nil {
		return err
	}
	return classify(s.finance.SessionStart(ctx, tx, finance.DuelStart{Meta: meta, SessionID: id, Queues: inputs}, func(ctx context.Context, tx *sql.Tx, accounts ledger.AccountPair) error {
		v.GeneralAccount = accounts.General
		v.GameAccount = accounts.Game
		if err := s.insertSession(ctx, tx, v); err != nil {
			return err
		}
		if err := randomness.Insert(ctx, tx, secret); err != nil {
			return err
		}
		for seat, q := range queues {
			if err := s.finance.TransferOnboarding(ctx, tx, finance.QueueOnboardingTransfer{QueueID: q.ID, SessionID: id, UserID: q.User, SeatNo: seat}); err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `UPDATE game_duel_user_slots SET queue_id=NULL,session_id=? WHERE user_id=? AND game_key=? AND queue_id=?`, id, q.User, s.rules.ID(), q.ID)
			if err != nil {
				return err
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				return ErrInvariant
			}
			if _, err = tx.ExecContext(ctx, `UPDATE game_duel_queue SET ledger_rows_remaining=? WHERE id=?`, db.EncodeU128(db.U128{}), q.ID); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM game_duel_queue WHERE id=?`, q.ID); err != nil {
				return err
			}
		}
		return nil
	}))
}
