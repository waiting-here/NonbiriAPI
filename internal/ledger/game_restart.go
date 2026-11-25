package ledger

import "github.com/waiting-here/NonbiriAPI/internal/db"

// NewLinkLinkRefund returns the ticket to the original assets.
func NewLinkLinkRefund(meta Meta, id string, wallets, platforms AccountPair, payment Payment) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !validPrimitive(total) || !positive(total) || meta.ActorUserID != 0 || !wallets.valid() || !platforms.valid() {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := newPlan(meta, KindLinkLinkRefund, sourceLinkLinkSession, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	return plan.add(platformRole(platforms.General), negate(payment.General)).add(userRole(wallets.General), payment.General).
		add(roleForAsset(platformRole(platforms.Game), Game), negate(payment.Game)).add(roleForAsset(userRole(wallets.Game), Game), payment.Game), nil
}

type RPSRefundSeat struct {
	Wallets AccountPair
	Payment Payment
}

// Matched rounds post their cuts immediately, while players receive no wallet
// payout until final settlement. Release the actual escrow and restore the
// original buy-ins. Clearing absorbs previously posted cuts and conversions.
func NewRPSRefund(meta Meta, id string, escrow, external AccountPair, balance Payment, seats []RPSRefundSeat) (Plan, error) {
	if meta.ActorUserID != 0 || escrow.General <= 0 || !external.valid() || len(seats) > 3 || !nonnegative(balance.General) || !nonnegative(balance.Game) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := newPlan(meta, KindRPSRefund, sourceRPSSession, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	code := "rps-session:" + id
	plan = plan.add(reserveRole(escrow.General, code), negate(balance.General)).requireZero(escrow.General)
	if escrow.Game > 0 {
		plan = plan.add(roleForAsset(reserveRole(escrow.Game, code), Game), negate(balance.Game)).requireZero(escrow.Game)
	} else if !balance.Game.IsZero() {
		return Plan{}, ErrInvalidPlan
	}
	clearing := balance
	for _, s := range seats {
		if !s.Wallets.valid() || !nonnegative(s.Payment.General) || !nonnegative(s.Payment.Game) || escrow.Game == 0 && !s.Payment.Game.IsZero() {
			return Plan{}, ErrInvalidPlan
		}
		plan = plan.add(userRole(s.Wallets.General), s.Payment.General).add(roleForAsset(userRole(s.Wallets.Game), Game), s.Payment.Game)
		clearing.General, err = subtractAmounts(clearing.General, s.Payment.General)
		if err != nil {
			return Plan{}, err
		}
		clearing.Game, err = subtractAmounts(clearing.Game, s.Payment.Game)
		if err != nil {
			return Plan{}, err
		}
	}
	plan = plan.add(externalRole(external.General), clearing.General).add(roleForAsset(externalRole(external.Game), Game), clearing.Game)
	ref, err := RPSSessionReservation(id)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.consume(ref)
	plan.spec.capacity.consumeAll = true
	return plan, nil
}
