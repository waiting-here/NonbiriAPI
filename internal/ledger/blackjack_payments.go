package ledger

import "github.com/waiting-here/NonbiriAPI/internal/db"

func NewBlackjackReserve(meta Meta, id string, wallets, escrow AccountPair, payment Payment) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !positive(total) || !validPrimitive(total) || !wallets.valid() || !escrow.valid() || meta.ActorUserID <= 0 {
		return Plan{}, ErrInvalidPlan
	}
	p, err := newPlan(meta, KindBlackjackReserve, sourceBlackjackPayment, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	p = p.add(userRole(wallets.General), negate(payment.General)).add(reserveRole(escrow.General, "blackjack-payment:"+id), payment.General).
		add(roleForAsset(userRole(wallets.Game), Game), negate(payment.Game)).add(roleForAsset(reserveRole(escrow.Game, "blackjack-payment:"+id), Game), payment.Game)
	return requirePaymentAvailable(p, wallets, payment), nil
}

// NewBlackjackRelease returns the exact original payment. A detached identity
// receives no wallet: each asset returns to its corresponding external account.
func NewBlackjackRelease(meta Meta, id string, escrow, destination AccountPair, payment Payment, deleted bool) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !positive(total) || !validPrimitive(total) || !destination.valid() || !escrow.valid() || meta.ActorUserID != 0 {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := BlackjackReservation(id)
	if err != nil {
		return Plan{}, err
	}
	p, err := newPlan(meta, KindBlackjackRelease, sourceBlackjackPayment, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	general, game := userRole(destination.General), userRole(destination.Game)
	if deleted {
		general, game = externalRole(destination.General), externalRole(destination.Game)
	}
	p = p.add(reserveRole(escrow.General, "blackjack-payment:"+id), negate(payment.General)).add(general, payment.General).
		add(roleForAsset(reserveRole(escrow.Game, "blackjack-payment:"+id), Game), negate(payment.Game)).add(roleForAsset(game, Game), payment.Game)
	return p.consume(ref).requireZero(escrow.General).requireZero(escrow.Game), nil
}

// NewBlackjackSettle retires the stake in its original assets, then pays the
// frozen per-hand proceeds entirely in general credits. One payment can carry
// the seat's combined proceeds; its other payments retire with zero proceeds.
func NewBlackjackSettle(meta Meta, id string, escrow, platforms AccountPair, externalID int64, destination SettlementDestination, payment Payment, payout FishingPayout) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !positive(total) || !validPrimitive(total) || !escrow.valid() || !platforms.valid() || externalID <= 0 || meta.ActorUserID != 0 || destination.role.id <= 0 || (destination.role.kind != AccountUser && destination.role.kind != AccountExternal) || destination.role.asset != General ||
		!validNonnegativePrimitive(payout.Net) || !validNonnegativePrimitive(payout.Platform) || !validNonnegativePrimitive(payout.Welfare) || !validNonnegativePrimitive(payout.Thursday) || positive(payout.Welfare) && payout.WelfareAccountID <= 0 || positive(payout.Thursday) && payout.ThursdayAccountID <= 0 {
		return Plan{}, ErrInvalidPlan
	}
	gross, err := addAmounts(payout.Net, payout.Platform, payout.Welfare, payout.Thursday)
	if err != nil || !validPrimitive(gross) {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := BlackjackReservation(id)
	if err != nil {
		return Plan{}, err
	}
	p, err := newPlan(meta, KindBlackjackSettle, sourceBlackjackPayment, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	platformGeneral, err := addAmounts(payment.General, payout.Platform)
	if err != nil || !validPrimitive(platformGeneral) {
		return Plan{}, ErrInvalidPlan
	}
	p = p.add(reserveRole(escrow.General, "blackjack-payment:"+id), negate(payment.General)).add(platformRole(platforms.General), platformGeneral).
		add(roleForAsset(reserveRole(escrow.Game, "blackjack-payment:"+id), Game), negate(payment.Game)).add(roleForAsset(platformRole(platforms.Game), Game), payment.Game).
		add(externalRole(externalID), negate(gross)).add(destination.role, payout.Net)
	if positive(payout.Welfare) {
		p = p.add(poolRole(payout.WelfareAccountID), payout.Welfare)
	}
	if positive(payout.Thursday) {
		p = p.add(poolRole(payout.ThursdayAccountID), payout.Thursday)
	}
	// Deidentified proceeds and external funding share one account.
	entries := make([]entrySpec, 0, len(p.spec.entries))
	for _, entry := range p.spec.entries {
		found := false
		for i := range entries {
			if entries[i].role.id != entry.role.id {
				continue
			}
			if entries[i].role != entry.role {
				return Plan{}, ErrInvalidPlan
			}
			entries[i].delta, err = addAmounts(entries[i].delta, entry.delta)
			if err != nil {
				return Plan{}, err
			}
			found = true
			break
		}
		if !found {
			entries = append(entries, entry)
		}
	}
	p.spec.entries = entries
	return p.consume(ref).requireZero(escrow.General).requireZero(escrow.Game), nil
}
