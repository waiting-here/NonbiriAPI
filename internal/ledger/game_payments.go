package ledger

import (
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func paymentTotal(payment Payment) (Amount, error) {
	if !nonnegative(payment.General) || !nonnegative(payment.Game) {
		return Amount{}, ErrInvalidPlan
	}
	return addAmounts(payment.General, payment.Game)
}

func requirePaymentAvailable(plan Plan, wallets AccountPair, payment Payment) Plan {
	delete(plan.spec.requireNonnegative, wallets.General)
	if positive(payment.General) {
		plan = plan.requireAvailable(wallets.General)
	}
	if positive(payment.Game) {
		plan = plan.requireAvailable(wallets.Game)
	}
	return plan
}

func NewFishingReserveWithPayment(meta Meta, batchID string, wallets, reserves AccountPair, payment Payment) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !validPrimitive(total) || !wallets.valid() || !reserves.valid() {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := NewFishingReserve(meta, batchID, wallets.General, reserves.General, payment.General)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(roleForAsset(userRole(wallets.Game), Game), negate(payment.Game)).
		add(roleForAsset(reserveRole(reserves.Game, "game_fishing_reserve"), Game), payment.Game)
	return requirePaymentAvailable(plan, wallets, payment), nil
}

// FishingPayout contains the already frozen general-currency proceeds.
// Entry fees remain separate and are retired in their original currencies.
type FishingPayout struct {
	Net, Platform, Welfare, Thursday    Amount
	WelfareAccountID, ThursdayAccountID int64
}

func NewFishingSettleWithPayment(meta Meta, batchID string, reserves, platforms AccountPair, externalAccountID, userAccountID int64, payment Payment, payout FishingPayout) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !validPrimitive(total) || !reserves.valid() || !platforms.valid() || externalAccountID <= 0 || userAccountID <= 0 ||
		!validNonnegativePrimitive(payout.Net) || !validNonnegativePrimitive(payout.Platform) || !validNonnegativePrimitive(payout.Welfare) || !validNonnegativePrimitive(payout.Thursday) ||
		positive(payout.Welfare) && payout.WelfareAccountID <= 0 || positive(payout.Thursday) && payout.ThursdayAccountID <= 0 {
		return Plan{}, ErrInvalidPlan
	}
	gross, err := addAmounts(payout.Net, payout.Platform, payout.Welfare, payout.Thursday)
	if err != nil || !validPrimitive(gross) {
		return Plan{}, ErrInvalidPlan
	}
	platformGeneral, err := addAmounts(payment.General, payout.Platform)
	if err != nil {
		return Plan{}, err
	}
	ref, err := FishingReservation(batchID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := fishingPlan(meta, KindFishingSettle, batchID)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(reserveRole(reserves.General, "game_fishing_reserve"), negate(payment.General)).
		add(platformRole(platforms.General), platformGeneral).
		add(externalRole(externalAccountID), negate(gross)).
		add(userRole(userAccountID), payout.Net).
		add(roleForAsset(reserveRole(reserves.Game, "game_fishing_reserve"), Game), negate(payment.Game)).
		add(roleForAsset(platformRole(platforms.Game), Game), payment.Game)
	if positive(payout.Welfare) {
		plan = plan.add(poolRole(payout.WelfareAccountID), payout.Welfare)
	}
	if positive(payout.Thursday) {
		plan = plan.add(poolRole(payout.ThursdayAccountID), payout.Thursday)
	}
	return plan.consume(ref), nil
}

func NewFishingReleaseWithPayment(meta Meta, batchID string, reserves, wallets AccountPair, payment Payment) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !validPrimitive(total) || !reserves.valid() || !wallets.valid() {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := NewFishingRelease(meta, batchID, reserves.General, wallets.General, payment.General)
	if err != nil {
		return Plan{}, err
	}
	return plan.add(roleForAsset(reserveRole(reserves.Game, "game_fishing_reserve"), Game), negate(payment.Game)).
		add(roleForAsset(userRole(wallets.Game), Game), payment.Game), nil
}

func NewLinkLinkEntryWithPayment(meta Meta, sessionID string, wallets, platforms AccountPair, payment Payment) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !validPrimitive(total) || !wallets.valid() || !platforms.valid() {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := NewLinkLinkEntry(meta, sessionID, wallets.General, platforms.General, payment.General)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(roleForAsset(userRole(wallets.Game), Game), negate(payment.Game)).
		add(roleForAsset(platformRole(platforms.Game), Game), payment.Game)
	return requirePaymentAvailable(plan, wallets, payment), nil
}

func NewRPSQueueReserveWithPayment(meta Meta, queueID string, wallets, reserves AccountPair, payment Payment) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !positive(total) || !wallets.valid() || !reserves.valid() {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := newPlan(meta, KindRPSQueueReserve, sourceRPSQueue, queueID, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(userRole(wallets.General), negate(payment.General)).
		add(reserveRole(reserves.General, "rps-queue:"+queueID), payment.General).
		add(roleForAsset(userRole(wallets.Game), Game), negate(payment.Game)).
		add(roleForAsset(reserveRole(reserves.Game, "rps-queue:"+queueID), Game), payment.Game)
	return requirePaymentAvailable(plan, wallets, payment), nil
}

func NewRPSQueueReleaseWithPayment(meta Meta, queueID string, reserves, wallets AccountPair, payment Payment) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !positive(total) || !reserves.valid() || !wallets.valid() {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := RPSQueueReservation(queueID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := newPlan(meta, KindRPSQueueRelease, sourceRPSQueue, queueID, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(reserveRole(reserves.General, "rps-queue:"+queueID), negate(payment.General)).
		add(userRole(wallets.General), payment.General).
		add(roleForAsset(reserveRole(reserves.Game, "rps-queue:"+queueID), Game), negate(payment.Game)).
		add(roleForAsset(userRole(wallets.Game), Game), payment.Game)
	return plan.consume(ref).requireZero(reserves.General).requireZero(reserves.Game), nil
}

// RPSQueuePayment preserves each queue's two reserves when players match.
type RPSQueuePayment struct {
	QueueID  string
	Accounts AccountPair
	Payment  Payment
}

func NewRPSSessionStartWithPayments(meta Meta, sessionID string, accounts AccountPair, futureRows db.U128, queues [3]RPSQueuePayment) (Plan, error) {
	if !accounts.valid() || futureRows.Big().Sign() <= 0 {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := RPSSessionReservation(sessionID)
	if err != nil {
		return Plan{}, err
	}
	sort.Slice(queues[:], func(i, j int) bool { return queues[i].QueueID < queues[j].QueueID })
	plan, err := newPlan(meta, KindRPSSessionStart, sourceRPSSession, sessionID, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	total := Payment{}
	for i, queue := range queues {
		amount, err := paymentTotal(queue.Payment)
		if err != nil || !positive(amount) || !queue.Accounts.valid() || i > 0 && queues[i-1].QueueID == queue.QueueID {
			return Plan{}, ErrInvalidPlan
		}
		queueRef, err := RPSQueueReservation(queue.QueueID)
		if err != nil {
			return Plan{}, err
		}
		total.General, err = addAmounts(total.General, queue.Payment.General)
		if err != nil {
			return Plan{}, err
		}
		total.Game, err = addAmounts(total.Game, queue.Payment.Game)
		if err != nil {
			return Plan{}, err
		}
		plan = plan.add(reserveRole(queue.Accounts.General, "rps-queue:"+queue.QueueID), negate(queue.Payment.General)).
			add(roleForAsset(reserveRole(queue.Accounts.Game, "rps-queue:"+queue.QueueID), Game), negate(queue.Payment.Game)).
			requireZero(queue.Accounts.General).requireZero(queue.Accounts.Game)
		if i == 0 {
			plan = plan.consume(queueRef)
		} else {
			plan.spec.capacity.releaseAll = append(plan.spec.capacity.releaseAll, queueRef)
		}
	}
	if _, err := paymentTotal(total); err != nil {
		return Plan{}, err
	}
	plan = plan.add(reserveRole(accounts.General, "rps-session:"+sessionID), total.General).
		add(roleForAsset(reserveRole(accounts.Game, "rps-session:"+sessionID), Game), total.Game)
	plan.spec.capacity.reserve = &futureReservation{ref: ref, rows: futureRows}
	return plan, nil
}

// NewRPSRoundCutWithGame consumes only this round's game input. The general
// reserve receives the conversion and pays the frozen cuts in one operation.
func NewRPSRoundCutWithGame(meta Meta, sessionID string, cutSeq db.U128, accounts, external AccountPair, platformAccountID, welfarePoolAccountID, thursdayPoolAccountID int64, converted Amount, cuts RPSCutAmounts) (Plan, error) {
	if !accounts.valid() || !converted.IsZero() && !external.valid() ||
		!nonnegative(converted) || !nonnegative(cuts.Platform) || !nonnegative(cuts.Welfare) || !nonnegative(cuts.Thursday) ||
		!cuts.Platform.IsZero() && platformAccountID <= 0 || !cuts.Welfare.IsZero() && welfarePoolAccountID <= 0 || !cuts.Thursday.IsZero() && thursdayPoolAccountID <= 0 {
		return Plan{}, ErrInvalidPlan
	}
	total, err := addAmounts(cuts.Platform, cuts.Welfare, cuts.Thursday)
	if err != nil || total.IsZero() && converted.IsZero() {
		return Plan{}, ErrInvalidPlan
	}
	generalDelta, err := subtractAmounts(converted, total)
	if err != nil {
		return Plan{}, err
	}
	ref, err := RPSSessionReservation(sessionID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := newPlan(meta, KindRPSRoundCut, sourceRPSSession, sessionID, cutSeq)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(reserveRole(accounts.General, "rps-session:"+sessionID), generalDelta)
	if !cuts.Platform.IsZero() {
		plan = plan.add(platformRole(platformAccountID), cuts.Platform)
	}
	if !cuts.Welfare.IsZero() {
		plan = plan.add(poolRole(welfarePoolAccountID), cuts.Welfare)
	}
	if !cuts.Thursday.IsZero() {
		plan = plan.add(poolRole(thursdayPoolAccountID), cuts.Thursday)
	}
	if !converted.IsZero() {
		plan = plan.add(externalRole(external.General), negate(converted)).
			add(roleForAsset(reserveRole(accounts.Game, "rps-session:"+sessionID), Game), negate(converted)).
			add(roleForAsset(externalRole(external.Game), Game), converted)
	}
	return plan.consume(ref), nil
}

// NewRPSTerminalWithGame converts the remaining game principal and pays all
// terminal entitlements in general credits, including deidentified seats.
func NewRPSTerminalWithGame(meta Meta, sessionID string, accounts, external AccountPair, welfarePoolAccountID int64, payouts []RPSTerminalPayout, deletedAmount, carry, gameRemaining Amount) (Plan, error) {
	if !accounts.valid() || !external.valid() || !nonnegative(gameRemaining) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := NewRPSTerminal(meta, sessionID, accounts.General, external.General, welfarePoolAccountID, payouts, deletedAmount, carry)
	if err != nil {
		return Plan{}, err
	}
	generalDelta, err := addAmounts(plan.spec.entries[0].delta, gameRemaining)
	if err != nil || generalDelta.Sign() > 0 {
		return Plan{}, ErrInvalidPlan
	}
	plan.spec.entries[0].delta = generalDelta
	externalDelta, err := subtractAmounts(deletedAmount, gameRemaining)
	if err != nil {
		return Plan{}, err
	}
	if deletedAmount.IsZero() {
		plan = plan.add(externalRole(external.General), externalDelta)
	} else {
		// The original terminal plan already contains the deleted-seat sink.
		for i := range plan.spec.entries {
			if plan.spec.entries[i].role.id == external.General {
				plan.spec.entries[i].delta = externalDelta
			}
		}
	}
	return plan.add(roleForAsset(reserveRole(accounts.Game, "rps-session:"+sessionID), Game), negate(gameRemaining)).
		add(roleForAsset(externalRole(external.Game), Game), gameRemaining).requireZero(accounts.Game), nil
}
