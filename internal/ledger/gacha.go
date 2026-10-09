package ledger

// NewGachaTicketPurchase records the exact credit funding split. The caller
// grants the purchased activity tickets in this same transaction.
func NewGachaTicketPurchase(meta Meta, wallets, external AccountPair, payment Payment) (Plan, error) {
	total, err := paymentTotal(payment)
	if err != nil || !positive(total) || !validPrimitive(total) || meta.ActorUserID <= 0 || !wallets.valid() || !external.valid() {
		return Plan{}, ErrInvalidPlan
	}
	p, err := operationPlan(meta, KindGachaTicketPurchase)
	if err != nil {
		return Plan{}, err
	}
	p = p.add(userRole(wallets.General), negate(payment.General)).add(externalRole(external.General), payment.General).
		add(roleForAsset(userRole(wallets.Game), Game), negate(payment.Game)).
		add(roleForAsset(externalRole(external.Game), Game), payment.Game)
	return requirePaymentAvailable(p, wallets, payment), nil
}

// NewGachaThemeReward changes cumulative donation credit without creating
// spendable credits. Theme eligibility belongs to the caller's transaction.
func NewGachaThemeReward(meta Meta, wallet int64) (Plan, error) {
	if meta.ActorUserID <= 0 || wallet <= 0 {
		return Plan{}, ErrInvalidPlan
	}
	p, err := operationPlan(meta, KindGachaThemeReward)
	if err != nil {
		return Plan{}, err
	}
	p.spec.donation = &donationChange{userID: meta.ActorUserID, accountID: wallet, delta: AmountFromMilli(10000)}
	return p, nil
}
