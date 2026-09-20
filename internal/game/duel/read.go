package duel

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func projectQueue(q queueRecord) *Queue {
	return &Queue{ID: q.ID, Revision: q.Revision.Decimal(), Mode: q.Mode, Deadline: q.Deadline, Ticket: q.Terms.Ticket, Payment: game.PaymentFromMilli(q.Ticket, q.GamePaid), TermsHash: q.TermsHash, RulesVersion: 1, Loadout: q.Loadout}
}
func (s *Service) projectState(v sessionRecord, seat int, now int64) (*State, error) {
	view, err := v.rules.View(v.Mode, v.Payload.Rules, seat, v.State == "terminal", v.Seats[seat].Action)
	if err != nil {
		return nil, err
	}
	var start *RoundStart
	if v.Payload.RoundStartedAt != nil {
		start = &RoundStart{Round: v.Round, StartedAt: *v.Payload.RoundStartedAt, Events: v.Payload.RoundStartEvents}
	}
	return &State{ID: v.ID, Game: s.rules.ID(), Mode: v.Mode, RulesVersion: 1, ContentHash: v.Terms.ContentHash, Revision: v.Revision.Decimal(), PhaseSeq: v.PhaseSeq.Decimal(), Phase: v.Phase, Round: v.Round, Deadline: v.Deadline, ServerNow: now, You: seat, Locked: [2]bool{v.Seats[0].Locked, v.Seats[1].Locked}, Ticket: v.Terms.Ticket, Rake: v.Terms.Rake, OwnPayment: game.PaymentFromMilli(v.Ticket, v.Seats[seat].GamePaid), View: view, Resolution: v.Payload.Resolution, RoundStart: start}, nil
}
func (s *Service) projectResult(v sessionRecord, seat int) (*ResultSummary, error) {
	if v.State != "terminal" || v.TerminalAt == nil {
		return nil, ErrInvariant
	}
	outcome := v.Outcome
	refund := game.PaymentFromMilli(0, 0)
	prize := "0"
	if outcome == "decided" {
		outcome = "loss"
		if v.Winner != nil && *v.Winner == seat {
			outcome = "win"
			refund = game.PaymentFromMilli(v.Ticket, v.Seats[seat].GamePaid)
			prize = game.FormatAmount(v.Prize)
		}
	} else {
		refund = game.PaymentFromMilli(v.Ticket, v.Seats[seat].GamePaid)
	}
	view, err := v.rules.View(v.Mode, v.Payload.Rules, seat, true, nil)
	if err != nil {
		return nil, err
	}
	return &ResultSummary{ContentHash: v.Terms.ContentHash, ID: v.ID, Game: s.rules.ID(), Mode: v.Mode, TerminalAt: *v.TerminalAt, Outcome: outcome, Reason: v.Reason, Scores: v.Scores, OwnPayment: game.PaymentFromMilli(v.Ticket, v.Seats[seat].GamePaid), OwnRefund: refund, PrizeGeneral: prize, Rake: RakeAmounts{Platform: game.FormatAmount(v.Platform), Welfare: game.FormatAmount(v.Welfare), Thursday: game.FormatAmount(v.Thursday)}, Resolution: v.Payload.Resolution, You: seat, View: view}, nil
}
func (s *Service) Read(ctx context.Context, identity Identity) (Home, error) {
	tx, now, err := s.begin(ctx)
	if err != nil {
		return Home{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, identity); err != nil {
		return Home{}, err
	}
	var qid, sid sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT queue_id,session_id FROM game_duel_user_slots WHERE user_id=? AND game_key=?`, identity.UserID, s.rules.ID()).Scan(&qid, &sid)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Home{}, err
	}
	home := Home{ServerNow: now}
	facts := activities.PublishFacts{}
	if qid.Valid {
		q, err := s.queue(ctx, tx, qid.String)
		if err != nil {
			return Home{}, classify(err)
		}
		maintenance, err := maintenanceOn(ctx, tx)
		if err != nil {
			return Home{}, err
		}
		cfg, err := s.config(ctx, tx)
		if err != nil {
			return Home{}, err
		}
		if now >= q.Deadline || maintenance || !cfg.Enabled || !cfg.Modes[q.Mode].Enabled {
			if err := s.releaseQueue(ctx, tx, q, 0, now); err != nil {
				return Home{}, err
			}
			facts.AccountIDs = append(facts.AccountIDs, q.User)
		} else {
			home.Queue = projectQueue(q)
		}
	}
	if sid.Valid {
		v, err := s.session(ctx, tx, sid.String)
		if err != nil {
			return Home{}, classify(err)
		}
		seat, err := s.seat(&v, identity.UserID)
		if err != nil {
			return Home{}, err
		}
		if err := s.authorizeExisting(ctx, tx, v, identity, "read", now); err != nil {
			return Home{}, err
		}
		facts, _, err = s.advance(ctx, tx, &v, now)
		if err != nil {
			return Home{}, classify(err)
		}
		if v.State == "active" {
			home.Current, err = s.projectState(v, seat, now)
			if err != nil {
				return Home{}, err
			}
			home.Current.Profiles, err = profiles(ctx, tx, v)
			if err != nil {
				return Home{}, err
			}
		}
	}
	var latest string
	err = tx.QueryRowContext(ctx, `SELECT g.id FROM game_duel_sessions g JOIN game_duel_seats p ON p.session_id=g.id WHERE p.user_id=? AND g.game_key=? AND g.state='terminal' AND g.delete_at>? ORDER BY g.terminal_at DESC,g.id DESC LIMIT 1`, identity.UserID, s.rules.ID(), now).Scan(&latest)
	if err == nil {
		v, err := s.session(ctx, tx, latest)
		if err != nil {
			return Home{}, err
		}
		seat, err := s.seat(&v, identity.UserID)
		if err != nil {
			return Home{}, err
		}
		if err := s.authorizeExisting(ctx, tx, v, identity, "read", now); err != nil {
			return Home{}, err
		}
		home.LatestResult, err = s.projectResult(v, seat)
		if err != nil {
			return Home{}, err
		}
		home.LatestResult.Profiles, err = profiles(ctx, tx, v)
		if err != nil {
			return Home{}, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Home{}, err
	}
	if err := tx.Commit(); err != nil {
		return Home{}, classify(err)
	}
	s.publish(ctx, facts)
	return home, nil
}
