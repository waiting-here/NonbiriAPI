package ledger

import "github.com/waiting-here/NonbiriAPI/internal/db"

// Catch tickets use game credits first, with the exact split frozen by the session.
func NewCatchTicket(meta Meta, id string, wallets, platforms AccountPair, payment Payment) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !validPrimitive(total) || !positive(total) || meta.ActorUserID <= 0 || !wallets.valid() || !platforms.valid() {
		return Plan{}, ErrInvalidPlan
	}
	p, err := newPlan(meta, KindCatchTicket, sourceCatchSession, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	p = p.add(userRole(wallets.General), negate(payment.General)).add(platformRole(platforms.General), payment.General).
		add(roleForAsset(userRole(wallets.Game), Game), negate(payment.Game)).add(roleForAsset(platformRole(platforms.Game), Game), payment.Game)
	return requirePaymentAvailable(p, wallets, payment), nil
}

func NewCatchRefund(meta Meta, id string, wallets, platforms AccountPair, payment Payment) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !validPrimitive(total) || !positive(total) || meta.ActorUserID != 0 || !wallets.valid() || !platforms.valid() {
		return Plan{}, ErrInvalidPlan
	}
	p, err := newPlan(meta, KindCatchRefund, sourceCatchSession, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	ref, err := CatchSessionReservation(id)
	if err != nil {
		return Plan{}, err
	}
	p = p.add(platformRole(platforms.General), negate(payment.General)).add(userRole(wallets.General), payment.General).
		add(roleForAsset(platformRole(platforms.Game), Game), negate(payment.Game)).add(roleForAsset(userRole(wallets.Game), Game), payment.Game)
	return p.consume(ref), nil
}

func NewCatchReward(meta Meta, id string, wallet, external int64, reward Amount) (Plan, error) {
	if meta.ActorUserID != 0 || wallet <= 0 || external <= 0 || !validPrimitive(reward) || !positive(reward) {
		return Plan{}, ErrInvalidPlan
	}
	p, err := newPlan(meta, KindCatchReward, sourceCatchSession, id, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	ref, err := CatchSessionReservation(id)
	if err != nil {
		return Plan{}, err
	}
	p = p.add(roleForAsset(externalRole(external), Game), negate(reward)).add(roleForAsset(userRole(wallet), Game), reward)
	return p.consume(ref), nil
}
