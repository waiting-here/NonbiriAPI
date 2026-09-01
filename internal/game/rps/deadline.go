package rps

import "github.com/waiting-here/NonbiriAPI/internal/game"

func (value *reducer) applyDeadlineDefaults() error {
	if value.record == nil || value.record.State != StateStarted || value.record.PhaseDeadline == nil || value.now < *value.record.PhaseDeadline {
		return ErrConflict
	}
	nextRevision, err := incU128(value.record.Revision)
	if err != nil {
		return err
	}
	value.record.Revision = nextRevision
	switch value.record.Phase {
	case PhaseGesture, PhasePaidPoolGesture, PhaseFreePoolGesture, PhaseUltimateGesture:
		for seat := range value.record.Seats {
			if value.record.Seats[seat].GestureEnvelope != nil {
				continue
			}
			value.service.randomMu.Lock()
			gesture, randomErr := randomGesture(value.service.random)
			var envelope []byte
			if randomErr == nil {
				envelope, randomErr = sealGesture(value.service.random, value.service.keys.gesture, value.record.ID, seat,
					value.record.PhaseSeq, value.record.RulesVersion, gesture)
			}
			value.service.randomMu.Unlock()
			if randomErr != nil {
				return randomErr
			}
			phase := value.record.PhaseSeq
			value.record.Seats[seat].GestureEnvelope = envelope
			value.record.Seats[seat].GesturePhaseSeq, value.record.Seats[seat].LastActionPhaseSeq = &phase, &phase
			value.record.Seats[seat].TimeoutCount, err = addCounter(value.record.Seats[seat].TimeoutCount, 1)
			if err != nil {
				return err
			}
			if err := appendEvent(value.record, EventActionLocked, actionLockedPayload{SeatNo: seat, ActionKind: "gesture"}); err != nil {
				return err
			}
		}
		if err := value.persist(); err != nil {
			return err
		}
		if value.record.Phase == PhaseGesture && value.record.Mode != game.RPSModeQuick {
			for seat := range value.record.Seats {
				value.record.Seats[seat].LastActionPhaseSeq = nil
			}
			if err := value.advancePhase(PhaseDealerRaise); err != nil {
				return err
			}
			return value.persist()
		}
		return value.revealAndReduce()
	case PhaseDealerRaise:
		if value.record.DealerSeat == nil {
			return ErrInvariant
		}
		seat := *value.record.DealerSeat
		value.record.Seats[seat].TimeoutCount, err = addCounter(value.record.Seats[seat].TimeoutCount, 1)
		if err != nil {
			return err
		}
		if err := appendEvent(value.record, EventActionLocked, actionLockedPayload{SeatNo: seat, ActionKind: "dealer_decision"}); err != nil {
			return err
		}
		return value.revealAndReduce()
	case PhaseFollowers:
		if value.record.DealerSeat == nil {
			return ErrInvariant
		}
		for seat := range value.record.Seats {
			if seat == *value.record.DealerSeat || value.record.Seats[seat].FollowerAction != nil {
				continue
			}
			decision, phase := FollowerSurrender, value.record.PhaseSeq
			value.record.Seats[seat].FollowerAction, value.record.Seats[seat].LastActionPhaseSeq = &decision, &phase
			value.record.Seats[seat].TimeoutCount, err = addCounter(value.record.Seats[seat].TimeoutCount, 1)
			if err != nil {
				return err
			}
			if err := appendEvent(value.record, EventActionLocked, actionLockedPayload{SeatNo: seat, ActionKind: "follower_decision"}); err != nil {
				return err
			}
		}
		if err := value.persist(); err != nil {
			return err
		}
		return value.revealAndReduce()
	default:
		return ErrInvariant
	}
}
