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

// NewGachaThemeReward grants theme credits and cumulative donation credit.
// Theme eligibility and the claim record belong to the caller's transaction.
func NewGachaThemeReward(meta Meta, wallets AccountPair, external int64, asset Asset) (Plan, error) {
	if meta.ActorUserID <= 0 || !wallets.valid() || external <= 0 || !asset.credit() {
		return Plan{}, ErrInvalidPlan
	}
	p, err := operationPlan(meta, KindGachaThemeReward)
	if err != nil {
		return Plan{}, err
	}
	wallet := wallets.General
	if asset == Game {
		wallet = wallets.Game
	}
	credits := AmountFromMilli(10000000)
	p = p.add(roleForAsset(userRole(wallet), asset), credits).
		add(roleForAsset(externalRole(external), asset), negate(credits))
	p.spec.donation = &donationChange{userID: meta.ActorUserID, accountID: wallets.General, delta: AmountFromMilli(10000)}
	return p, nil
}
