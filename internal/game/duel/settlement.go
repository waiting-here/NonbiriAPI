package duel

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (s *Service) enterPhase(v *sessionRecord, now int64, advance bool) error {
	info, err := s.rules.Inspect(v.Mode, v.Payload.Rules)
	if err != nil {
		return err
	}
	if advance {
		v.PhaseSeq, err = increment(v.PhaseSeq)
		if err != nil {
			return err
		}
	}
	v.Phase = info.Phase
	v.Round = info.Round
	v.Scores = info.Scores
	for seat := range 2 {
		v.Seats[seat].Action = nil
		v.Seats[seat].Locked = false
	}
	if info.Phase == "terminal" {
		v.Deadline = nil
		return nil
	}
	if info.Phase == "settlement" {
		resolution := v.Payload.Resolution
		if resolution == nil || resolution.Round != info.Round || resolution.StartedAt != now || resolution.EndsAt <= now {
			return ErrInvariant
		}
		deadline := resolution.EndsAt
		v.Deadline = &deadline
		return nil
	}
	if info.Seconds < 1 || info.Seconds > 20 {
		return ErrInvariant
	}
	deadline := now + info.Seconds
	v.Deadline = &deadline
	for seat, required := range info.Required {
		if required {
			continue
		}
		action, err := s.rules.Automatic(v.Mode, v.Payload.Rules, seat)
		if err != nil {
			return err
		}
		v.Seats[seat].Action = action
		v.Seats[seat].Locked = true
	}
	return nil
}
func users(v *sessionRecord) []int64 {
	result := []int64{}
	for _, seat := range v.Seats {
		if seat.User != nil {
			result = append(result, *seat.User)
		}
	}
	return result
}
func (s *Service) resolve(ctx context.Context, tx *sql.Tx, v *sessionRecord, expected db.U128, now int64) (activities.PublishFacts, error) {
	if !v.Seats[0].Locked || !v.Seats[1].Locked {
		return activities.PublishFacts{}, ErrInvariant
	}
	secret, err := randomness.Load(ctx, tx, s.rules.ID(), v.ID)
	if err != nil {
		return activities.PublishFacts{}, err
	}
	actions := [2]json.RawMessage{v.Seats[0].Action, v.Seats[1].Action}
	var next Transition
	if rules, ok := s.rules.(interface {
		ResolveWithRandom(string, json.RawMessage, [2]json.RawMessage, func(int) (int, error)) (Transition, error)
	}); ok && secret != nil {
		stream, streamErr := secret.Stream("round/" + strconv.Itoa(v.Round))
		if streamErr != nil {
			return activities.PublishFacts{}, streamErr
		}
		next, err = rules.ResolveWithRandom(v.Mode, v.Payload.Rules, actions, func(bound int) (int, error) {
			if bound <= 0 {
				return 0, ErrInvariant
			}
			value, err := stream.Uint64n(uint64(bound))
			return int(value), err
		})
	} else {
		next, err = s.rules.Resolve(v.Mode, v.Payload.Rules, actions)
	}
	if err != nil {
		return activities.PublishFacts{}, err
	}
	if err := randomness.Save(ctx, tx, secret); err != nil {
		return activities.PublishFacts{}, err
	}
	if len(next.Record) > 0 {
		record := roundRecord{Round: next.Round, Before: v.Payload.Rules, After: next.State, Facts: next.Record, StartEvents: v.Payload.RoundStartEvents, Timeouts: v.Payload.RoundTimeouts}
		body, err := Encode(record)
		if err != nil {
			return activities.PublishFacts{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO game_duel_rounds(session_id,round_no,record_json) VALUES(?,?,?)`, v.ID, next.Round, string(body)); err != nil {
			return activities.PublishFacts{}, err
		}
		v.Payload.RoundStartEvents = nil
		v.Payload.RoundStartedAt = nil
		v.Payload.RoundTimeouts = [2]bool{}
	}
	v.Payload.Rules = next.State
	if len(next.Presentation) > 0 {
		duration, err := s.presentationDuration(next.Presentation)
		if err != nil || duration <= 0 || now > math.MaxInt64-duration {
			return activities.PublishFacts{}, ErrInvariant
		}
		v.Payload.Resolution = &Resolution{Round: next.Round, StartedAt: now, EndsAt: now + duration, Summary: next.Presentation}
	}
	if err := s.enterPhase(v, now, true); err != nil {
		return activities.PublishFacts{}, err
	}
	info, err := s.rules.Inspect(v.Mode, v.Payload.Rules)
	if err != nil {
		return activities.PublishFacts{}, err
	}
	if info.Result != nil {
		return s.terminal(ctx, tx, v, expected, now, info.Result.Winner, info.Result.Reason, false)
	}
	return activities.PublishFacts{}, s.saveSession(ctx, tx, v, expected)
}

func (s *Service) presentationDuration(summary json.RawMessage) (int64, error) {
	rules, ok := s.rules.(interface {
		PresentationDuration(json.RawMessage) (int64, error)
	})
	if !ok {
		return 0, ErrInvariant
	}
	return rules.PresentationDuration(summary)
}
func (s *Service) advance(ctx context.Context, tx *sql.Tx, v *sessionRecord, now int64) (activities.PublishFacts, bool, error) {
	if v.State != "active" || v.Deadline == nil || now < *v.Deadline {
		return activities.PublishFacts{}, false, nil
	}
	expected := v.Revision
	var err error
	v.Revision, err = increment(v.Revision)
	if err != nil {
		return activities.PublishFacts{}, false, err
	}
	if v.Phase == "settlement" {
		state, events, err := s.rules.Begin(v.Mode, v.Payload.Rules)
		if err != nil {
			return activities.PublishFacts{}, false, err
		}
		v.Payload.Rules = state
		v.Payload.RoundStartEvents = events
		v.Payload.RoundStartedAt = &now
		if err := s.enterPhase(v, now, true); err != nil {
			return activities.PublishFacts{}, false, err
		}
		if v.Seats[0].Locked && v.Seats[1].Locked {
			facts, err := s.resolve(ctx, tx, v, expected, now)
			return facts, true, err
		}
		return activities.PublishFacts{}, true, s.saveSession(ctx, tx, v, expected)
	}
	for seat := range 2 {
		if v.Seats[seat].Locked {
			continue
		}
		action, err := s.rules.Automatic(v.Mode, v.Payload.Rules, seat)
		if err != nil {
			return activities.PublishFacts{}, false, err
		}
		v.Seats[seat].Action = action
		v.Seats[seat].Locked = true
		v.Seats[seat].TimeoutCount++
		v.Payload.RoundTimeouts[seat] = true
	}
	facts, err := s.resolve(ctx, tx, v, expected, now)
	return facts, true, err
}
func (s *Service) terminal(ctx context.Context, tx *sql.Tx, v *sessionRecord, expected db.U128, now int64, winner *int, reason string, cancelled bool) (activities.PublishFacts, error) {
	if v.State != "active" || cancelled && winner != nil {
		return activities.PublishFacts{}, ErrInvariant
	}
	meta, err := s.meta(0, now)
	if err != nil {
		return activities.PublishFacts{}, err
	}
	info, err := s.rules.Inspect(v.Mode, v.Payload.Rules)
	if err != nil {
		return activities.PublishFacts{}, err
	}
	v.State = "terminal"
	v.Phase = "terminal"
	v.Deadline = nil
	v.TerminalAt = &now
	expiry := now + RetentionSeconds
	v.DeleteAt = &expiry
	v.Winner = winner
	v.Scores = info.Scores
	v.Reason = reason
	v.Operation = meta.OperationID
	v.Outcome = "draw"
	if cancelled {
		v.Outcome = "system_cancelled"
	} else if winner != nil {
		v.Outcome = "decided"
	}
	for seat := range 2 {
		v.Payload.TerminalActions[seat] = v.Seats[seat].Action
		v.Seats[seat].Action = nil
		v.Seats[seat].Locked = false
	}
	// A cancellation replaces the result pose with a neutral portrait. It does
	// not turn an already persisted cast into a new terminal action animation.
	if cancelled {
		v.Payload.Resolution = nil
	}
	var destinations []activities.PoolDestination
	var welfare, thursday int64
	if winner != nil {
		cuts, err := ledger.DuelAmounts(ledger.AmountFromMilli(v.Ticket), ledger.DuelRates{Platform: v.Terms.Rake.Platform, Welfare: v.Terms.Rake.Welfare, Thursday: v.Terms.Rake.Thursday})
		if err != nil {
			return activities.PublishFacts{}, err
		}
		if cuts.Welfare.Sign() > 0 {
			pool, err := s.pools.WelfareDestination(ctx, tx)
			if err != nil {
				return activities.PublishFacts{}, err
			}
			welfare = pool.AccountID
			destinations = append(destinations, pool)
		}
		if cuts.Thursday.Sign() > 0 {
			pool, err := s.pools.ThursdayDestination(ctx, tx, now)
			if err != nil {
				return activities.PublishFacts{}, err
			}
			thursday = pool.AccountID
			destinations = append(destinations, pool)
		}
	}
	err = s.finance.Terminal(ctx, tx, finance.DuelFinish{Meta: meta, SessionID: v.ID, Winner: winner, WelfareAccountID: welfare, ThursdayAccountID: thursday}, func(ctx context.Context, tx *sql.Tx, cuts ledger.DuelCuts) error {
		v.Prize = cuts.Prize.Big().Int64()
		v.Platform = cuts.Platform.Big().Int64()
		v.Welfare = cuts.Welfare.Big().Int64()
		v.Thursday = cuts.Thursday.Big().Int64()
		if err := s.saveSession(ctx, tx, v, expected); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM game_duel_user_slots WHERE session_id=? AND game_key=?`, v.ID, s.rules.ID())
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 2 {
			return ErrInvariant
		}
		return nil
	})
	if err != nil {
		return activities.PublishFacts{}, classify(err)
	}
	facts := activities.PublishFacts{AccountIDs: users(v)}
	if len(destinations) > 0 {
		poolFacts, err := s.pools.RecordPoolTransfers(ctx, tx, now, destinations...)
		if err != nil {
			return activities.PublishFacts{}, err
		}
		facts.Global = poolFacts.Global
		facts.AccountIDs = append(facts.AccountIDs, poolFacts.AccountIDs...)
	}
	return facts, nil
}
