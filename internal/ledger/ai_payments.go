package ledger

import "github.com/waiting-here/NonbiriAPI/internal/db"

// AI entry fees keep their original currencies until a terminal decision.
func NewAITicket(meta Meta, id string, wallets, escrow AccountPair, payment Payment) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !positive(total) || !validPrimitive(total) || !wallets.valid() || !escrow.valid() || duelIDGame(id, false) != "bidding" {
		return Plan{}, ErrInvalidPlan
	}
	p, err := newPlan(meta, KindAITicket, sourceDuelSession, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	p = p.add(userRole(wallets.General), negate(payment.General)).add(reserveRole(escrow.General, "duel-session:"+id), payment.General).
		add(roleForAsset(userRole(wallets.Game), Game), negate(payment.Game)).add(roleForAsset(reserveRole(escrow.Game, "duel-session:"+id), Game), payment.Game)
	return requirePaymentAvailable(p, wallets, payment), nil
}

// A terminal operation either refunds the original assets or retires the fee
// and issues a first-clear game reward. A wholly free result needs no ledger row.
func NewAITerminal(meta Meta, id string, escrow, wallets, platforms AccountPair, externalGame int64, payment Payment, reward Amount, cancelled bool) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !validNonnegativePrimitive(total) || !validNonnegativePrimitive(reward) || total.IsZero() && reward.IsZero() || cancelled && !reward.IsZero() || !escrow.valid() || !wallets.valid() || meta.ActorUserID != 0 || duelIDGame(id, false) != "bidding" {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := DuelSessionReservation(id)
	if err != nil {
		return Plan{}, err
	}
	p, err := newPlan(meta, KindAITerminal, sourceDuelSession, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	if positive(total) {
		general, game := userRole(wallets.General), roleForAsset(userRole(wallets.Game), Game)
		if !cancelled {
			if !platforms.valid() {
				return Plan{}, ErrInvalidPlan
			}
			general, game = platformRole(platforms.General), roleForAsset(platformRole(platforms.Game), Game)
		}
		p = p.add(reserveRole(escrow.General, "duel-session:"+id), negate(payment.General)).add(general, payment.General).
			add(roleForAsset(reserveRole(escrow.Game, "duel-session:"+id), Game), negate(payment.Game)).add(game, payment.Game)
		p = p.requireZero(escrow.General).requireZero(escrow.Game)
	}
	if positive(reward) {
		if externalGame <= 0 {
			return Plan{}, ErrInvalidPlan
		}
		p = p.add(roleForAsset(externalRole(externalGame), Game), negate(reward)).add(roleForAsset(userRole(wallets.Game), Game), reward)
	}
	return p.consume(ref), nil
}
