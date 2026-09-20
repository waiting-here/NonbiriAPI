package blackjack

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
)

// progress runs under SQLite's writer position, shared with queue acceptance,
// departures and all action batches. Client acknowledgements are irrelevant.
func (s *Service) progress(ctx context.Context, tx *sql.Tx, now int64, allowSeating bool) (activities.PublishFacts, error) {
	facts := activities.PublishFacts{}
	cfg, err := s.config(ctx, tx)
	if err != nil {
		return facts, err
	}
	maintenance, err := maintenanceOn(ctx, tx)
	if err != nil {
		return facts, err
	}
	open := cfg.Enabled && !maintenance
	v, err := currentSession(ctx, tx)
	if err != nil && !noRows(err) {
		return facts, err
	}
	if err == nil {
		if v.Phase == "seating" {
			if !open {
				list, err := sessionEntries(ctx, tx, v.ID)
				if err != nil {
					return facts, err
				}
				for _, e := range list {
					if err := s.releaseEntry(ctx, tx, e, now); err != nil {
						return facts, err
					}
					if e.User.Valid {
						facts.AccountIDs = append(facts.AccountIDs, e.User.Int64)
					}
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM game_blackjack_sessions WHERE id=?`, v.ID); err != nil {
					return facts, err
				}
			} else if now >= v.StartedAt+engine.SeatingSeconds {
				list, err := sessionEntries(ctx, tx, v.ID)
				if err != nil {
					return facts, err
				}
				if len(list) == 0 {
					if _, err := tx.ExecContext(ctx, `DELETE FROM game_blackjack_sessions WHERE id=?`, v.ID); err != nil {
						return facts, err
					}
				} else {
					numbers := make([]int, len(list))
					for i, e := range list {
						numbers[i] = int(e.Seat.Int64)
					}
					secret, err := randomness.Load(ctx, tx, "blackjack", v.ID)
					if err != nil {
						return facts, err
					}
					random := s.random
					if secret != nil {
						random, err = secret.Stream("shoe")
						if err != nil {
							return facts, err
						}
					}
					state, err := engine.New(numbers, int(v.StartedAt/engine.RoundSeconds%engine.MaxSeats), random)
					if err != nil {
						return facts, err
					}
					if err := randomness.Save(ctx, tx, secret); err != nil {
						return facts, err
					}
					if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_entries SET state='playing' WHERE session_id=? AND state='seated'`, v.ID); err != nil {
						return facts, err
					}
					fact, err := factFor(&state, list, now)
					if err != nil {
						return facts, err
					}
					if err := saveState(ctx, tx, &v, state, fact, v.StartedAt+engine.SeatingSeconds, "deal"); err != nil {
						return facts, err
					}
					if state.Finished {
						more, err := s.settleTable(ctx, tx, &v, state, list, now)
						if err != nil {
							return facts, err
						}
						mergeFacts(&facts, more)
					}
				}
			}
		}
		if v.Phase == "decision" {
			more, err := s.advanceDecisions(ctx, tx, &v, now)
			if err != nil {
				return facts, err
			}
			mergeFacts(&facts, more)
		}
	}
	if !open {
		waiting, err := entries(ctx, tx, `state='waiting' ORDER BY ordinal LIMIT 128`)
		if err != nil {
			return facts, err
		}
		for _, e := range waiting {
			if err := s.releaseEntry(ctx, tx, e, now); err != nil {
				return facts, err
			}
			facts.AccountIDs = append(facts.AccountIDs, e.User.Int64)
		}
		return facts, nil
	}
	if !allowSeating || now%engine.RoundSeconds >= engine.SeatingSeconds {
		return facts, nil
	}
	v, err = currentSession(ctx, tx)
	if err != nil && !noRows(err) {
		return facts, err
	}
	if noRows(err) {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_blackjack_entries WHERE state='waiting'`).Scan(&count); err != nil {
			return facts, err
		}
		if count == 0 {
			return facts, nil
		}
		start := now - now%engine.RoundSeconds
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_blackjack_sessions WHERE started_at=?`, start).Scan(&count); err != nil {
			return facts, err
		}
		if count != 0 {
			return facts, nil
		}
		id, err := s.generate("bjt_")
		if err != nil {
			return facts, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_blackjack_sessions(id,started_at,phase,revision,last_batch) VALUES(?,?,'seating',1,?)`, id, start, start); err != nil {
			return facts, err
		}
		if s.random == nil {
			secret, err := randomness.New("blackjack", id, "six-decks-s17-v1", nil)
			if err != nil {
				return facts, err
			}
			if err := randomness.Insert(ctx, tx, secret); err != nil {
				return facts, err
			}
		}
		v = sessionRecord{ID: id, StartedAt: start, Phase: "seating", Revision: 1, LastBatch: start}
	}
	if v.Phase != "seating" || v.StartedAt+engine.SeatingSeconds <= now {
		return facts, nil
	}
	list, err := sessionEntries(ctx, tx, v.ID)
	if err != nil {
		return facts, err
	}
	occupied := [engine.MaxSeats]bool{}
	for _, e := range list {
		occupied[e.Seat.Int64] = true
	}
	if len(list) == engine.MaxSeats {
		return facts, nil
	}
	waiting, err := entries(ctx, tx, `state='waiting' ORDER BY ordinal LIMIT 4096`)
	if err != nil {
		return facts, err
	}
	i := 0
	for number := range engine.MaxSeats {
		if occupied[number] || i >= len(waiting) {
			continue
		}
		var e entryRecord
		found := false
		for i < len(waiting) {
			e = waiting[i]
			i++
			ok, err := eligible(ctx, tx, e.User.Int64, now)
			if err != nil {
				return facts, err
			}
			if ok {
				found = true
				break
			}
			if err := s.releaseEntry(ctx, tx, e, now); err != nil {
				return facts, err
			}
			facts.AccountIDs = append(facts.AccountIDs, e.User.Int64)
		}
		if !found {
			break
		}
		if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_entries SET state='seated',session_id=?,seat_no=? WHERE id=? AND state='waiting'`, v.ID, number, e.ID); err != nil {
			return facts, err
		}
		facts.AccountIDs = append(facts.AccountIDs, e.User.Int64)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_blackjack_sessions WHERE id=? AND phase='seating' AND NOT EXISTS(SELECT 1 FROM game_blackjack_entries e WHERE e.session_id=game_blackjack_sessions.id)`, v.ID); err != nil {
		return facts, err
	}
	return facts, nil
}

func (s *Service) advanceDecisions(ctx context.Context, tx *sql.Tx, v *sessionRecord, now int64) (activities.PublishFacts, error) {
	facts := activities.PublishFacts{}
	state, err := decodeState(*v)
	if err != nil {
		return facts, err
	}
	for batch := v.LastBatch + 1; batch <= min(now, v.StartedAt+engine.DecisionEnd); batch++ {
		list, err := sessionEntries(ctx, tx, v.ID)
		if err != nil {
			return facts, err
		}
		actions := []engine.Action{}
		stop := []int{}
		changed := false
		for _, e := range list {
			if e.Pending.Valid && e.Batch.Int64 <= batch {
				var a engine.Action
				if json.Unmarshal([]byte(e.Pending.String), &a) != nil || a.Seat != int(e.Seat.Int64) {
					return facts, ErrInvariant
				}
				actions = append(actions, a)
				if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_entries SET pending_json=NULL,pending_batch=NULL WHERE id=?`, e.ID); err != nil {
					return facts, err
				}
			}
			if e.Stopped || batch == v.StartedAt+engine.DecisionEnd {
				stop = append(stop, int(e.Seat.Int64))
			}
		}
		if len(actions) > 0 {
			state, err = engine.ApplyBatch(state, actions)
			if err != nil {
				return facts, err
			}
			changed = true
		}
		if !state.Finished && len(stop) > 0 {
			for _, number := range stop {
				for hand := range 2 {
					changed = changed || len(state.Legal(number, hand)) > 0
				}
			}
			state, err = engine.Stop(state, stop...)
			if err != nil {
				return facts, err
			}
		}
		if !changed {
			continue
		}
		fact, err := factFor(&state, list, now)
		if err != nil {
			return facts, err
		}
		kind := "actions"
		if batch == v.StartedAt+engine.DecisionEnd {
			kind = "timeout"
		}
		if err := saveState(ctx, tx, v, state, fact, batch, kind); err != nil {
			return facts, err
		}
		if state.Finished {
			more, err := s.settleTable(ctx, tx, v, state, list, now)
			mergeFacts(&facts, more)
			return facts, err
		}
	}
	return facts, nil
}

func (s *Service) cancelCurrent(ctx context.Context, tx *sql.Tx, now int64, reason string) (activities.PublishFacts, error) {
	facts := activities.PublishFacts{}
	v, err := currentSession(ctx, tx)
	if noRows(err) {
		return facts, nil
	}
	if err != nil {
		return facts, err
	}
	list, err := sessionEntries(ctx, tx, v.ID)
	if err != nil {
		return facts, err
	}
	var state *engine.State
	if v.Phase == "decision" {
		parsed, err := decodeState(v)
		if err != nil {
			return facts, err
		}
		state = &parsed
	}
	fact, err := factFor(state, list, now)
	if err != nil {
		return facts, err
	}
	for _, e := range list {
		if v.Phase == "decision" {
			total, _, err := paymentTotals(ctx, tx, e.ID)
			if err != nil {
				return facts, err
			}
			fact.Refunds = append(fact.Refunds, SeatRefund{Seat: int(e.Seat.Int64), Amount: game.FormatAmount(total)})
		}
		if err := s.releaseEntry(ctx, tx, e, now); err != nil {
			return facts, err
		}
		if e.User.Valid {
			facts.AccountIDs = append(facts.AccountIDs, e.User.Int64)
		}
	}
	if v.Phase == "seating" {
		_, err = tx.ExecContext(ctx, `DELETE FROM game_blackjack_sessions WHERE id=?`, v.ID)
		return facts, err
	}
	body, err := marshal(fact)
	if err != nil {
		return facts, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE game_blackjack_sessions SET phase='cancelled',revision=revision+1,state_json=NULL,view_json=?,terminal_at=?,reason=? WHERE id=?`, string(body), now, reason, v.ID); err != nil {
		return facts, err
	}
	return facts, appendEvent(ctx, tx, v.ID, now, "cancelled", body)
}
