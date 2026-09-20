package blackjack

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
)

func (s *Service) Read(ctx context.Context, identity Identity) (Home, error) {
	tx, now, err := s.begin(ctx)
	if err != nil {
		return Home{}, err
	}
	defer tx.Rollback()
	if err := s.authorize(ctx, tx, identity); err != nil {
		return Home{}, err
	}
	facts, err := s.progress(ctx, tx, now, true)
	if err != nil {
		return Home{}, err
	}
	result, err := s.homeTx(ctx, tx, identity, now)
	if err != nil {
		return Home{}, err
	}
	on, err := maintenanceOn(ctx, tx)
	if err != nil {
		return Home{}, err
	}
	if on {
		if result.YourSeat == nil || result.Table == nil {
			return Home{}, ErrMaintenance
		}
		v, err := readSession(ctx, tx, result.Table.ID)
		if err != nil {
			return Home{}, err
		}
		if err := s.authorizeExisting(ctx, tx, v, identity, "read", now); err != nil {
			return Home{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Home{}, err
	}
	s.publish(ctx, facts)
	return result, nil
}
func (s *Service) homeTx(ctx context.Context, tx *sql.Tx, identity Identity, now int64) (Home, error) {
	cfg, err := s.config(ctx, tx)
	if err != nil {
		return Home{}, err
	}
	start := now - now%engine.RoundSeconds
	result := Home{ServerNow: now, Phase: "seating", Deadline: start + engine.SeatingSeconds, NextRoundAt: start + engine.RoundSeconds, Config: cfg.Wire(), ConfigHash: configurationHash(cfg)}
	if now >= start+engine.SeatingSeconds {
		result.Phase = "decision"
		result.Deadline = start + engine.DecisionEnd
	}
	if now >= start+engine.DecisionEnd {
		result.Phase = "result"
		result.Deadline = start + engine.RoundSeconds
	}
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_blackjack_entries WHERE state='waiting'`).Scan(&count); err != nil {
		return result, err
	}
	result.QueueCount = strconv.FormatInt(count, 10)
	v, err := scanSession(tx.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM game_blackjack_sessions WHERE started_at=? OR phase IN ('seating','decision') ORDER BY started_at DESC LIMIT 1`, start))
	if err != nil && !noRows(err) {
		return result, err
	}
	if err == nil {
		view, err := projectTable(ctx, tx, v, now)
		if err != nil {
			return result, err
		}
		result.Table = &view
		result.Phase = view.Phase
		result.Deadline = view.Deadline
		result.NextRoundAt = view.NextRoundAt
		if own, err := participant(ctx, tx, v.ID, identity.UserID); err == nil {
			seat := int(own.Seat.Int64)
			result.YourSeat = &seat
		} else if !noRows(err) {
			return result, err
		}
	}
	e, err := currentEntry(ctx, tx, identity.UserID)
	if err != nil && !noRows(err) {
		return result, err
	}
	if err == nil {
		position, err := queuePosition(ctx, tx, e)
		if err != nil {
			return result, err
		}
		total, gamePaid, err := paymentTotals(ctx, tx, e.ID)
		if err != nil {
			return result, err
		}
		own := OwnEntry{ID: e.ID, Position: strconv.FormatInt(position, 10), State: e.State, Stake: game.FormatAmount(e.Stake), Rake: e.Rates, Payment: game.PaymentFromMilli(total, gamePaid), Pending: e.Pending.Valid, Legal: map[string][]string{}}
		if e.Seat.Valid {
			seat := int(e.Seat.Int64)
			own.Seat = &seat
		}
		if e.Session.Valid {
			id := e.Session.String
			own.SessionID = &id
		}
		if e.State == "playing" && !e.Pending.Valid && !e.Stopped {
			state, err := decodeState(v)
			if err != nil {
				return result, err
			}
			for hand := range 2 {
				own.Legal[strconv.Itoa(hand)] = state.Legal(int(e.Seat.Int64), hand)
			}
		}
		result.You = &own
	}
	return result, nil
}
