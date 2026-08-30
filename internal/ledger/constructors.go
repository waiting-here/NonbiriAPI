package ledger

import "github.com/waiting-here/NonbiriAPI/internal/db"

func NewAdminUserAdjustment(meta Meta, userAccountID, externalAccountID int64, creditDelta Amount, donationCreditUserID int64, donationDelta Amount, reason string) (Plan, error) {
	if meta.ActorUserID <= 0 || userAccountID <= 0 || externalAccountID <= 0 || !validPrimitive(creditDelta) || !validPrimitive(donationDelta) || !validReason(reason) {
		return Plan{}, ErrInvalidPlan
	}
	if donationDelta.Sign() != 0 && donationCreditUserID <= 0 || donationCreditUserID < 0 {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := operationPlan(meta, KindAdminUserAdjustment)
	if err != nil {
		return Plan{}, err
	}
	plan.spec.reason = reason
	if donationCreditUserID > 0 {
		plan.spec.donation = &donationChange{userID: donationCreditUserID, accountID: userAccountID, delta: donationDelta}
	}
	if creditDelta.Sign() > 0 {
		plan = plan.add(externalRole(externalAccountID), negate(creditDelta)).add(userRole(userAccountID), creditDelta)
	} else if creditDelta.Sign() < 0 {
		plan = plan.add(userRole(userAccountID), creditDelta).add(externalRole(externalAccountID), negate(creditDelta))
	}
	return plan, nil
}

// NewAdminPoolAdjustment interprets poolDelta as the pool-side signed change.
func NewAdminPoolAdjustment(meta Meta, poolAccountID, externalAccountID int64, poolDelta Amount, reason string) (Plan, error) {
	if meta.ActorUserID <= 0 || poolAccountID <= 0 || externalAccountID <= 0 || poolDelta.IsZero() || !validPrimitive(poolDelta) || !validReason(reason) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := operationPlan(meta, KindAdminPoolAdjustment)
	if err != nil {
		return Plan{}, err
	}
	plan.spec.reason = reason
	if poolDelta.Sign() > 0 {
		plan = plan.add(externalRole(externalAccountID), negate(poolDelta)).add(poolRole(poolAccountID), poolDelta)
	} else {
		plan = plan.add(poolRole(poolAccountID), poolDelta).add(externalRole(externalAccountID), negate(poolDelta))
	}
	return plan, nil
}

func NewAccountDeleteZero(meta Meta, userAccountID, externalAccountID int64) (Plan, error) {
	if userAccountID <= 0 || externalAccountID <= 0 {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := operationPlan(meta, KindAccountDeleteZero)
	if err != nil {
		return Plan{}, err
	}
	plan.spec.dynamic = dynamicAccountDelete
	// Zero placeholders retain the exact account roles until Apply reads the
	// authoritative wallet balance. A zero wallet materializes to zero lines.
	plan = plan.add(userRole(userAccountID), Amount{}).add(externalRole(externalAccountID), Amount{})
	return plan, nil
}

func NewCheckinAward(meta Meta, userAccountID, externalAccountID int64, award Amount) (Plan, error) {
	if userAccountID <= 0 || externalAccountID <= 0 || !validNonnegativePrimitive(award) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := operationPlan(meta, KindCheckinAward)
	if err != nil {
		return Plan{}, err
	}
	return plan.add(externalRole(externalAccountID), negate(award)).add(userRole(userAccountID), award), nil
}

func NewAntiAbusePenalty(meta Meta, userAccountID, externalAccountID int64, penalty Amount, reason string) (Plan, error) {
	if userAccountID <= 0 || externalAccountID <= 0 || !validNonnegativePrimitive(penalty) || !validReason(reason) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := operationPlan(meta, KindAntiAbusePenalty)
	if err != nil {
		return Plan{}, err
	}
	plan.spec.reason = reason
	if penalty.IsZero() {
		return plan, nil
	}
	return plan.add(userRole(userAccountID), negate(penalty)).add(externalRole(externalAccountID), penalty), nil
}

func NewWelfareClaim(meta Meta, poolAccountID, userAccountID int64, award Amount) (Plan, error) {
	if poolAccountID <= 0 || userAccountID <= 0 || !positive(award) || !validPrimitive(award) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := operationPlan(meta, KindWelfareClaim)
	if err != nil {
		return Plan{}, err
	}
	return plan.add(poolRole(poolAccountID), negate(award)).add(userRole(userAccountID), award), nil
}

func NewThursdayContribution(meta Meta, userAccountID, currentPoolAccountID int64, entry Amount) (Plan, error) {
	if userAccountID <= 0 || currentPoolAccountID <= 0 || !positive(entry) || !validPrimitive(entry) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := operationPlan(meta, KindThursdayContribution)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(userRole(userAccountID), negate(entry)).add(poolRole(currentPoolAccountID), entry)
	return plan.requireAvailable(userAccountID), nil
}

func NewThursdayPayout(meta Meta, periodID, participantID string, currentPoolAccountID, userAccountID int64, payout Amount) (Plan, error) {
	if currentPoolAccountID <= 0 || userAccountID <= 0 || !nonnegative(payout) {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := ThursdayParticipantReservation(periodID, participantID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := operationPlan(meta, KindThursdayPayout)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(poolRole(currentPoolAccountID), negate(payout)).add(userRole(userAccountID), payout)
	return plan.consume(ref), nil
}

func NewForwardReserve(meta Meta, requestID string, userAccountID, reserveAccountID int64, reserved Amount) (Plan, error) {
	if userAccountID <= 0 || reserveAccountID <= 0 || !validNonnegativePrimitive(reserved) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := logicalPlan(meta, KindForwardReserve, requestID)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(userRole(userAccountID), negate(reserved)).add(reserveRole(reserveAccountID, "forward_reserve"), reserved)
	return plan.requireAvailable(userAccountID), nil
}

func NewForwardSettle(meta Meta, requestID string, reserveAccountID, platformAccountID int64, destination SettlementDestination, reserved, actual Amount) (Plan, error) {
	if reserveAccountID <= 0 || platformAccountID <= 0 || destination.role.id <= 0 || !validNonnegativePrimitive(reserved) || !validNonnegativePrimitive(actual) {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := LogicalRequestReservation(requestID)
	if err != nil {
		return Plan{}, err
	}
	refund, err := subtractAmounts(reserved, actual)
	if err != nil {
		return Plan{}, err
	}
	plan, err := logicalPlan(meta, KindForwardSettle, requestID)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(reserveRole(reserveAccountID, "forward_reserve"), negate(reserved)).add(destination.role, refund).add(platformRole(platformAccountID), actual)
	return plan.consume(ref), nil
}

func NewForwardRelease(meta Meta, requestID string, reserveAccountID, userAccountID int64, reserved Amount) (Plan, error) {
	if reserveAccountID <= 0 || userAccountID <= 0 || !validNonnegativePrimitive(reserved) {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := LogicalRequestReservation(requestID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := logicalPlan(meta, KindForwardRelease, requestID)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(reserveRole(reserveAccountID, "forward_reserve"), negate(reserved)).add(userRole(userAccountID), reserved)
	return plan.consume(ref), nil
}

func NewCharityReserve(meta Meta, requestID string, userAccountID, reserveAccountID int64, reserved Amount) (Plan, error) {
	if userAccountID <= 0 || reserveAccountID <= 0 || !validNonnegativePrimitive(reserved) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := logicalPlan(meta, KindCharityReserve, requestID)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(userRole(userAccountID), negate(reserved)).add(reserveRole(reserveAccountID, "charity_reserve"), reserved)
	return plan.requireAvailable(userAccountID), nil
}

func NewCharitySettle(meta Meta, requestID string, reserveAccountID, platformAccountID int64, destination SettlementDestination, reserved, charge Amount) (Plan, error) {
	if reserveAccountID <= 0 || platformAccountID <= 0 || destination.role.id <= 0 || !validNonnegativePrimitive(reserved) || !validNonnegativePrimitive(charge) {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := LogicalRequestReservation(requestID)
	if err != nil {
		return Plan{}, err
	}
	refund, err := subtractAmounts(reserved, charge)
	if err != nil {
		return Plan{}, err
	}
	plan, err := logicalPlan(meta, KindCharitySettle, requestID)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(reserveRole(reserveAccountID, "charity_reserve"), negate(reserved)).add(destination.role, refund).add(platformRole(platformAccountID), charge)
	return plan.consume(ref), nil
}

func NewCharityRelease(meta Meta, requestID string, reserveAccountID, userAccountID int64, reserved Amount) (Plan, error) {
	if reserveAccountID <= 0 || userAccountID <= 0 || !validNonnegativePrimitive(reserved) {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := LogicalRequestReservation(requestID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := logicalPlan(meta, KindCharityRelease, requestID)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(reserveRole(reserveAccountID, "charity_reserve"), negate(reserved)).add(userRole(userAccountID), reserved)
	return plan.consume(ref), nil
}

func NewDonorReward(meta Meta, requestID, claimID string, externalAccountID, donorAccountID, donorUserID int64, reward Amount) (Plan, error) {
	if externalAccountID <= 0 || donorAccountID <= 0 || donorUserID <= 0 || !positive(reward) || !validPrimitive(reward) {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := LogicalRequestReservation(requestID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := newPlan(meta, KindDonorReward, sourceDispatchClaim, claimID, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	plan.spec.donation = &donationChange{userID: donorUserID, accountID: donorAccountID, delta: reward}
	plan = plan.add(externalRole(externalAccountID), negate(reward)).add(userRole(donorAccountID), reward)
	return plan.consume(ref), nil
}

// ThursdayFinalizeAmounts are the frozen aggregate transfers from the closed
// current pool. Total must equal Platform+Welfare+Next+Rollover.
type ThursdayFinalizeAmounts struct {
	Total    Amount
	Platform Amount
	Welfare  Amount
	Next     Amount
	Rollover Amount
}

func NewThursdayFinalize(meta Meta, periodID string, currentPoolAccountID, platformAccountID, welfarePoolAccountID, nextPoolAccountID int64, amounts ThursdayFinalizeAmounts) (Plan, error) {
	if currentPoolAccountID <= 0 || platformAccountID <= 0 || welfarePoolAccountID <= 0 || nextPoolAccountID <= 0 ||
		!nonnegative(amounts.Total) || !nonnegative(amounts.Platform) || !nonnegative(amounts.Welfare) || !nonnegative(amounts.Next) || !nonnegative(amounts.Rollover) {
		return Plan{}, ErrInvalidPlan
	}
	sum, err := addAmounts(amounts.Platform, amounts.Welfare, amounts.Next, amounts.Rollover)
	if err != nil || sum.Big().Cmp(amounts.Total.Big()) != 0 {
		return Plan{}, ErrInvalidPlan
	}
	nextTotal, err := addAmounts(amounts.Next, amounts.Rollover)
	if err != nil {
		return Plan{}, err
	}
	ref, err := ThursdayPeriodReservation(periodID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := newPlan(meta, KindThursdayFinalize, sourcePeriod, periodID, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(poolRole(currentPoolAccountID), negate(amounts.Total)).add(platformRole(platformAccountID), amounts.Platform).add(poolRole(welfarePoolAccountID), amounts.Welfare).add(poolRole(nextPoolAccountID), nextTotal)
	return plan.consume(ref).requireZero(currentPoolAccountID), nil
}

func NewFishingReserve(meta Meta, batchID string, userAccountID, reserveAccountID int64, entry Amount) (Plan, error) {
	if userAccountID <= 0 || reserveAccountID <= 0 || !validNonnegativePrimitive(entry) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := fishingPlan(meta, KindFishingReserve, batchID)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(userRole(userAccountID), negate(entry)).add(reserveRole(reserveAccountID, "game_fishing_reserve"), entry)
	return plan.requireAvailable(userAccountID), nil
}

func NewFishingSettle(meta Meta, batchID string, reserveAccountID, platformAccountID, externalAccountID, userAccountID int64, entry, payout Amount) (Plan, error) {
	if reserveAccountID <= 0 || platformAccountID <= 0 || externalAccountID <= 0 || userAccountID <= 0 || !validNonnegativePrimitive(entry) || !validNonnegativePrimitive(payout) {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := FishingReservation(batchID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := fishingPlan(meta, KindFishingSettle, batchID)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(reserveRole(reserveAccountID, "game_fishing_reserve"), negate(entry)).add(platformRole(platformAccountID), entry).add(externalRole(externalAccountID), negate(payout)).add(userRole(userAccountID), payout)
	return plan.consume(ref), nil
}

func NewFishingRelease(meta Meta, batchID string, reserveAccountID, userAccountID int64, entry Amount) (Plan, error) {
	if reserveAccountID <= 0 || userAccountID <= 0 || !validNonnegativePrimitive(entry) {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := FishingReservation(batchID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := fishingPlan(meta, KindFishingRelease, batchID)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(reserveRole(reserveAccountID, "game_fishing_reserve"), negate(entry)).add(userRole(userAccountID), entry)
	return plan.consume(ref), nil
}

func NewLinkLinkEntry(meta Meta, sessionID string, userAccountID, platformAccountID int64, entry Amount) (Plan, error) {
	if userAccountID <= 0 || platformAccountID <= 0 || !validNonnegativePrimitive(entry) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := newPlan(meta, KindLinkLinkEntry, sourceLinkLinkSession, sessionID, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(userRole(userAccountID), negate(entry)).add(platformRole(platformAccountID), entry)
	return plan.requireAvailable(userAccountID), nil
}

func NewRPSQueueReserve(meta Meta, queueID string, userAccountID, queueAccountID int64, reserved Amount) (Plan, error) {
	if userAccountID <= 0 || queueAccountID <= 0 || !positive(reserved) || !validPrimitive(reserved) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := newPlan(meta, KindRPSQueueReserve, sourceRPSQueue, queueID, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(userRole(userAccountID), negate(reserved)).add(reserveRole(queueAccountID, "rps-queue:"+queueID), reserved)
	return plan.requireAvailable(userAccountID), nil
}

func NewRPSQueueRelease(meta Meta, queueID string, queueAccountID, userAccountID int64, reserved Amount) (Plan, error) {
	if queueAccountID <= 0 || userAccountID <= 0 || !positive(reserved) || !validPrimitive(reserved) {
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
	plan = plan.add(reserveRole(queueAccountID, "rps-queue:"+queueID), negate(reserved)).add(userRole(userAccountID), reserved)
	return plan.consume(ref).requireZero(queueAccountID), nil
}

func NewRPSSessionStart(meta Meta, sessionID string, sessionAccountID int64, sessionFutureRows db.U128, queues [3]RPSQueueInput) (Plan, error) {
	if sessionAccountID <= 0 || sessionFutureRows.Big().Sign() <= 0 {
		return Plan{}, ErrInvalidPlan
	}
	sessionRef, err := RPSSessionReservation(sessionID)
	if err != nil {
		return Plan{}, err
	}
	queues, err = sortedQueueInputs(queues)
	if err != nil {
		return Plan{}, err
	}
	plan, err := newPlan(meta, KindRPSSessionStart, sourceRPSSession, sessionID, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	total := Amount{}
	for i, queue := range queues {
		plan = plan.add(reserveRole(queue.AccountID, "rps-queue:"+queue.QueueID), negate(queue.Amount))
		plan = plan.requireZero(queue.AccountID)
		total, err = addAmounts(total, queue.Amount)
		if err != nil {
			return Plan{}, err
		}
		ref, refErr := RPSQueueReservation(queue.QueueID)
		if refErr != nil {
			return Plan{}, refErr
		}
		if i == 0 {
			plan = plan.consume(ref)
		} else {
			plan.spec.capacity.releaseAll = append(plan.spec.capacity.releaseAll, ref)
		}
	}
	plan = plan.add(reserveRole(sessionAccountID, "rps-session:"+sessionID), total)
	plan.spec.capacity.reserve = &futureReservation{ref: sessionRef, rows: sessionFutureRows}
	return plan, nil
}

// RPSCutAmounts are one non-zero phase cut into the three frozen sinks.
type RPSCutAmounts struct {
	Platform Amount
	Welfare  Amount
	Thursday Amount
}

func NewRPSRoundCut(meta Meta, sessionID string, cutSeq db.U128, sessionAccountID, platformAccountID, welfarePoolAccountID, thursdayPoolAccountID int64, amounts RPSCutAmounts) (Plan, error) {
	if sessionAccountID <= 0 || platformAccountID <= 0 || welfarePoolAccountID <= 0 || thursdayPoolAccountID <= 0 ||
		!nonnegative(amounts.Platform) || !nonnegative(amounts.Welfare) || !nonnegative(amounts.Thursday) {
		return Plan{}, ErrInvalidPlan
	}
	total, err := addAmounts(amounts.Platform, amounts.Welfare, amounts.Thursday)
	if err != nil || !positive(total) {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := RPSSessionReservation(sessionID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := newPlan(meta, KindRPSRoundCut, sourceRPSSession, sessionID, cutSeq)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(reserveRole(sessionAccountID, "rps-session:"+sessionID), negate(total)).add(platformRole(platformAccountID), amounts.Platform).add(poolRole(welfarePoolAccountID), amounts.Welfare).add(poolRole(thursdayPoolAccountID), amounts.Thursday)
	return plan.consume(ref), nil
}

// RPSTerminalPayout is one surviving seat's authoritative entitlement.
type RPSTerminalPayout struct {
	UserAccountID int64
	Amount        Amount
}

func NewRPSTerminal(meta Meta, sessionID string, sessionAccountID, externalAccountID, welfarePoolAccountID int64, payouts []RPSTerminalPayout, deletedAmount, carry Amount) (Plan, error) {
	if sessionAccountID <= 0 || externalAccountID <= 0 || welfarePoolAccountID <= 0 || !nonnegative(deletedAmount) || !nonnegative(carry) || len(payouts) > 3 {
		return Plan{}, ErrInvalidPlan
	}
	seen := make(map[int64]struct{}, len(payouts))
	total, err := addAmounts(deletedAmount, carry)
	if err != nil {
		return Plan{}, err
	}
	for _, payout := range payouts {
		if payout.UserAccountID <= 0 || !nonnegative(payout.Amount) {
			return Plan{}, ErrInvalidPlan
		}
		if _, exists := seen[payout.UserAccountID]; exists {
			return Plan{}, ErrInvalidPlan
		}
		seen[payout.UserAccountID] = struct{}{}
		total, err = addAmounts(total, payout.Amount)
		if err != nil {
			return Plan{}, err
		}
	}
	ref, err := RPSSessionReservation(sessionID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := newPlan(meta, KindRPSTerminal, sourceRPSSession, sessionID, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(reserveRole(sessionAccountID, "rps-session:"+sessionID), negate(total))
	for _, payout := range payouts {
		plan = plan.add(userRole(payout.UserAccountID), payout.Amount)
	}
	if !deletedAmount.IsZero() {
		plan = plan.add(externalRole(externalAccountID), deletedAmount)
	}
	plan = plan.add(poolRole(welfarePoolAccountID), carry)
	plan = plan.consume(ref).requireZero(sessionAccountID)
	plan.spec.capacity.consumeAll = true
	return plan, nil
}
