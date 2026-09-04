package rps

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

type reducer struct {
	service          *Service
	ctx              context.Context
	tx               *sql.Tx
	record           *sessionRecord
	expectedRevision db.U128
	now              int64
	actorUserID      int64
	facts            activities.PublishFacts
	terminal         *terminalResult
	transitions      int
}

func (value *reducer) countTransition() error {
	value.transitions++
	if value.transitions > 16 {
		return ErrInvariant
	}
	return nil
}

func phaseSeconds(record *sessionRecord, phase string) (int, error) {
	if record == nil {
		return 0, ErrInvariant
	}
	switch phase {
	case PhaseGesture, PhasePaidPoolGesture, PhaseFreePoolGesture, PhaseUltimateGesture:
		return record.GestureSeconds, nil
	case PhaseDealerRaise:
		return record.DealerSeconds, nil
	case PhaseFollowers:
		return record.FollowerSeconds, nil
	default:
		return 0, ErrInvariant
	}
}

func (value *reducer) advancePhase(phase string) error {
	if err := value.countTransition(); err != nil {
		return err
	}
	sequence, err := incU128(value.record.PhaseSeq)
	if err != nil {
		return err
	}
	seconds, err := phaseSeconds(value.record, phase)
	if err != nil {
		return err
	}
	deadline := value.now + int64(seconds)
	if deadline < value.now || deadline > 253402300799 {
		return ErrInvariant
	}
	value.record.State, value.record.Phase, value.record.PhaseSeq = StateStarted, phase, sequence
	value.record.PhaseDeadline = &deadline
	return appendEvent(value.record, EventPhaseChanged, phaseChangedPayload{Phase: phase, Deadline: &deadline})
}

func clearCurrentActions(record *sessionRecord) {
	for seat := range record.Seats {
		record.Seats[seat].GestureEnvelope = nil
		record.Seats[seat].GesturePhaseSeq = nil
		record.Seats[seat].FollowerAction = nil
		record.Seats[seat].LastActionPhaseSeq = nil
	}
	record.DealerRaise = nil
}

func resetRoundInputs(record *sessionRecord) {
	for seat := range record.Seats {
		record.Seats[seat].CurrentRoundInput = db.U128{}
		record.Seats[seat].CurrentAllIn = false
	}
}

func (value *reducer) persist() error {
	if err := persistSessionTx(value.ctx, value.tx, value.record, value.expectedRevision); err != nil {
		return err
	}
	value.expectedRevision = value.record.Revision
	return nil
}

func (value *reducer) applyInputs(inputs [3]*big.Int) error {
	facts, err := value.service.applyInputsTx(value.ctx, value.tx, value.record, value.expectedRevision, inputs, value.now, value.actorUserID)
	if err != nil {
		return err
	}
	value.expectedRevision = value.record.Revision
	value.facts = mergePublishFacts(value.facts, facts)
	return nil
}

func saturatedPlannedInput(base int64, multiplier *big.Int, balance *big.Int) (*big.Int, error) {
	if base <= 0 || multiplier == nil || multiplier.Sign() <= 0 || balance == nil || balance.Sign() < 0 {
		return nil, ErrInvariant
	}
	baseBig := big.NewInt(base)
	if multiplier.Cmp(new(big.Int).Quo(maxU128, baseBig)) > 0 {
		return cloneBig(balance), nil
	}
	planned := new(big.Int).Mul(baseBig, multiplier)
	if planned.Cmp(balance) > 0 {
		return cloneBig(balance), nil
	}
	return planned, nil
}

func (value *reducer) nextBase() error {
	if value.record.DealerSeat == nil {
		return ErrInvariant
	}
	dealer := (*value.record.DealerSeat + 1) % 3
	value.record.DealerSeat = &dealer
	value.record.PoolBaseMultiplier = nil
	permanent := value.record.PermanentMultiplier
	value.record.CurrentPlanMultiplier = &permanent
	value.record.PaidPoolStreak, value.record.FreePoolStreak = db.U128{}, db.U128{}
	value.record.ReminderState = "none"
	clearCurrentActions(value.record)
	resetRoundInputs(value.record)
	inputs := [3]*big.Int{}
	allZeroAfter := true
	for seat := range value.record.Seats {
		var input *big.Int
		var err error
		if value.record.Mode == game.RPSModeStandard {
			input = big.NewInt(value.record.BaseMilli)
			if input.Cmp(value.record.Seats[seat].CurrentBalance.Big()) > 0 {
				return ErrInvariant
			}
		} else {
			input, err = saturatedPlannedInput(value.record.BaseMilli, permanent.Big(), value.record.Seats[seat].CurrentBalance.Big())
			if err != nil {
				return err
			}
		}
		inputs[seat] = input
		if input.Cmp(value.record.Seats[seat].CurrentBalance.Big()) != 0 {
			allZeroAfter = false
		}
	}
	phase := PhaseGesture
	if value.record.Mode == game.RPSModeDeathmatch && allZeroAfter {
		phase = PhaseUltimateGesture
		value.record.DealerSeat = nil
	}
	if err := value.advancePhase(phase); err != nil {
		return err
	}
	return value.applyInputs(inputs)
}

func anyBalanceBelow(record *sessionRecord, threshold *big.Int) bool {
	for _, seat := range record.Seats {
		if seat.CurrentBalance.Big().Cmp(threshold) < 0 {
			return true
		}
	}
	return false
}

func anyBalanceZero(record *sessionRecord) bool {
	for _, seat := range record.Seats {
		if seat.CurrentBalance.Big().Sign() == 0 {
			return true
		}
	}
	return false
}

func (value *reducer) enterFreePool(resetStreak bool) error {
	base := value.record.PermanentMultiplier
	value.record.PoolBaseMultiplier, value.record.CurrentPlanMultiplier = &base, nil
	value.record.DealerRaise = nil
	if resetStreak {
		value.record.FreePoolStreak = db.U128{}
	}
	value.record.ReminderState = "none"
	clearCurrentActions(value.record)
	resetRoundInputs(value.record)
	if err := value.advancePhase(PhaseFreePoolGesture); err != nil {
		return err
	}
	return value.persist()
}

func (value *reducer) enterPaidPool() error {
	if value.record.Mode == game.RPSModeStandard && anyBalanceBelow(value.record, big.NewInt(value.record.BaseMilli)) ||
		value.record.Mode == game.RPSModeDeathmatch && anyBalanceZero(value.record) {
		return value.enterFreePool(true)
	}
	base := value.record.PermanentMultiplier
	value.record.PoolBaseMultiplier, value.record.CurrentPlanMultiplier = &base, nil
	value.record.DealerRaise = nil
	clearCurrentActions(value.record)
	resetRoundInputs(value.record)
	planMultiplier, err := paidPlanMultiplier(value.record.Mode, base.Big(), value.record.PaidPoolStreak.Big())
	if err != nil {
		return err
	}
	inputs := [3]*big.Int{}
	for seat := range value.record.Seats {
		if value.record.Mode == game.RPSModeStandard {
			inputs[seat] = big.NewInt(value.record.BaseMilli)
		} else {
			inputs[seat], err = saturatedPlannedInput(value.record.BaseMilli, planMultiplier, value.record.Seats[seat].CurrentBalance.Big())
			if err != nil {
				return err
			}
		}
	}
	if err := value.advancePhase(PhasePaidPoolGesture); err != nil {
		return err
	}
	return value.applyInputs(inputs)
}

func (value *reducer) applyPayout(weights [3]int) ([3]*big.Int, error) {
	payout, carry, err := payouts(value.record.PlayerPool.Big(), weights)
	if err != nil {
		return [3]*big.Int{}, err
	}
	for seat := range value.record.Seats {
		balance, err := u128(new(big.Int).Add(value.record.Seats[seat].CurrentBalance.Big(), payout[seat]))
		if err != nil {
			return [3]*big.Int{}, err
		}
		value.record.Seats[seat].CurrentBalance = balance
		value.record.Seats[seat].TotalReturned, err = addU256Value(value.record.Seats[seat].TotalReturned, payout[seat])
		if err != nil {
			return [3]*big.Int{}, err
		}
	}
	pool, err := u128(carry)
	if err != nil {
		return [3]*big.Int{}, err
	}
	value.record.PlayerPool = pool
	return payout, nil
}

func (value *reducer) appendSettlement(deltas [3]*big.Int) error {
	payload := settlementPayload{
		PlayerPool: formatMilli(value.record.PlayerPool.Big()),
		Cuts:       Cuts{Platform: formatMilli(value.record.PlatformCutTotal.Big()), Welfare: formatMilli(value.record.WelfareCutTotal.Big()), Thursday: formatMilli(value.record.ThursdayCutTotal.Big())},
	}
	for seat := range deltas {
		payload.SeatDeltas = append(payload.SeatDeltas, settlementSeatPayload{SeatNo: seat, Delta: formatMilli(deltas[seat])})
	}
	return appendEvent(value.record, EventSettlement, payload)
}

func (value *reducer) revealAndReduce() error {
	if err := value.countTransition(); err != nil {
		return err
	}
	revealedPhase := value.record.Phase
	gestures := [3]string{}
	surrendered := [3]bool{}
	for seat := range value.record.Seats {
		persisted := &value.record.Seats[seat]
		if persisted.GestureEnvelope == nil || persisted.GesturePhaseSeq == nil {
			return ErrInvariant
		}
		gesture, err := openGesture(value.service.keys.gesture, value.record.ID, seat, *persisted.GesturePhaseSeq, value.record.RulesVersion, persisted.GestureEnvelope)
		if err != nil {
			return err
		}
		gestures[seat] = gesture
		switch gesture {
		case GestureRock:
			persisted.RockCount, err = addCounter(persisted.RockCount, 1)
		case GestureScissors:
			persisted.ScissorsCount, err = addCounter(persisted.ScissorsCount, 1)
		case GesturePaper:
			persisted.PaperCount, err = addCounter(persisted.PaperCount, 1)
		}
		if err != nil {
			return err
		}
		if persisted.FollowerAction != nil && *persisted.FollowerAction == FollowerSurrender {
			surrendered[seat] = true
		}
	}
	evaluation, err := evaluateReveal(gestures, surrendered)
	if err != nil {
		return err
	}
	payload := revealPayload{ResultCode: evaluation.Code}
	for seat, gesture := range gestures {
		payload.Gestures = append(payload.Gestures, revealGesturePayload{SeatNo: seat, Gesture: gesture})
	}
	if err := appendEvent(value.record, EventReveal, payload); err != nil {
		return err
	}
	baseRound := revealedPhase == PhaseGesture || revealedPhase == PhaseDealerRaise || revealedPhase == PhaseFollowers || revealedPhase == PhaseUltimateGesture
	ultimate := revealedPhase == PhaseUltimateGesture || revealedPhase == PhaseFreePoolGesture && value.record.DealerSeat == nil
	if baseRound {
		value.record.BaseRoundCount, err = addCounter(value.record.BaseRoundCount, 1)
		if err != nil {
			return err
		}
		permanent, err := permanentMultiplier(value.record.Mode, value.record.BaseRoundCount.Big())
		if err != nil {
			return err
		}
		value.record.PermanentMultiplier, err = u128(permanent)
		if err != nil {
			return err
		}
	}
	deltas := [3]*big.Int{new(big.Int), new(big.Int), new(big.Int)}
	if value.record.Mode == game.RPSModeQuick || !evaluation.Tie {
		deltas, err = value.applyPayout(evaluation.Weights)
		if err != nil {
			return err
		}
	}
	if err := value.appendSettlement(deltas); err != nil {
		return err
	}
	clearCurrentActions(value.record)
	if value.record.Mode == game.RPSModeQuick {
		return value.finish(TerminalQuickResolved)
	}
	if evaluation.Tie {
		switch revealedPhase {
		case PhaseGesture, PhaseDealerRaise, PhaseFollowers:
			value.record.PaidPoolStreak, value.record.FreePoolStreak = db.U128{}, db.U128{}
			return value.enterPaidPool()
		case PhasePaidPoolGesture:
			value.record.PaidTieCount, err = addCounter(value.record.PaidTieCount, 1)
			if err == nil {
				value.record.PaidPoolStreak, err = addCounter(value.record.PaidPoolStreak, 1)
			}
			if err != nil {
				return err
			}
			return value.enterPaidPool()
		case PhaseUltimateGesture:
			value.record.FreePoolStreak = db.U128{}
			return value.enterFreePool(true)
		case PhaseFreePoolGesture:
			value.record.FreeTieCount, err = addCounter(value.record.FreeTieCount, 1)
			if err == nil {
				value.record.FreePoolStreak, err = addCounter(value.record.FreePoolStreak, 1)
			}
			if err != nil {
				return err
			}
			if value.record.FreePoolStreak.Big().Cmp(big.NewInt(game.RPSFreeTieLimit)) >= 0 {
				return value.finish(TerminalFreeTieLimit)
			}
			if value.record.FreePoolStreak.Big().Cmp(big.NewInt(game.RPSFreeTieReminder)) >= 0 {
				value.record.ReminderState = "active"
				if err := appendEvent(value.record, EventReminder, reminderPayload{FreeTieCount: value.record.FreePoolStreak.Decimal()}); err != nil {
					return err
				}
			}
			clearCurrentActions(value.record)
			resetRoundInputs(value.record)
			if err := value.advancePhase(PhaseFreePoolGesture); err != nil {
				return err
			}
			return value.persist()
		default:
			return ErrInvariant
		}
	}
	value.record.PaidPoolStreak, value.record.FreePoolStreak = db.U128{}, db.U128{}
	value.record.ReminderState = "none"
	if ultimate {
		return value.finish(TerminalUltimateResolved)
	}
	if value.record.Mode == game.RPSModeStandard {
		if value.record.BaseRoundCount.Big().Cmp(big.NewInt(9)) >= 0 {
			return value.finish(TerminalStandardRoundLimit)
		}
		if anyBalanceBelow(value.record, big.NewInt(value.record.BaseMilli)) {
			return value.finish(TerminalStandardInsufficient)
		}
	} else if anyBalanceZero(value.record) {
		return value.finish(TerminalDeathmatchExhausted)
	}
	return value.nextBase()
}

func (value *reducer) finish(reason string) error {
	if err := value.countTransition(); err != nil {
		return err
	}
	if err := value.service.transitionTerminal(value.record, reason, value.now); err != nil {
		return err
	}
	if err := value.persist(); err != nil {
		return err
	}
	var result terminalResult
	var err error
	if value.service.beforeTerminalCommit != nil {
		err = value.service.beforeTerminalCommit()
	}
	if err == nil {
		result, err = value.service.finalizeTerminalTx(value.ctx, value.tx, value.record, value.now)
	}
	if err != nil {
		if retryErr := value.service.scheduleTerminalRetryTx(value.ctx, value.tx, value.record, value.now, err); retryErr != nil {
			return retryErr
		}
		return nil
	}
	value.terminal = &result
	value.facts = mergePublishFacts(value.facts, result.Facts)
	return nil
}
