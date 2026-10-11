package ledger

// NewLakeEntry retires the exact general-credit fee through the external
// account. The caller commits the period entitlement in the same transaction.
func NewLakeEntry(meta Meta, wallet, external int64, fee Amount) (Plan, error) {
	if meta.ActorUserID <= 0 || wallet <= 0 || external <= 0 || !positive(fee) || !validPrimitive(fee) {
		return Plan{}, ErrInvalidPlan
	}
	p, e := operationPlan(meta, KindLakeEntry)
	if e != nil {
		return Plan{}, e
	}
	return p.add(userRole(wallet), negate(fee)).add(externalRole(external), fee).requireAvailable(wallet), nil
}

// NewLakeExchange records only the credit side of an exact coin exchange.
// The domain atomically validates and posts the corresponding coin change.
func NewLakeExchange(meta Meta, wallet, external int64, asset Asset, delta Amount) (Plan, error) {
	if meta.ActorUserID <= 0 || wallet <= 0 || external <= 0 || !asset.credit() || delta.IsZero() || !validPrimitive(delta) {
		return Plan{}, ErrInvalidPlan
	}
	p, e := operationPlan(meta, KindLakeExchange)
	if e != nil {
		return Plan{}, e
	}
	p = p.add(roleForAsset(userRole(wallet), asset), delta).add(roleForAsset(externalRole(external), asset), negate(delta))
	if delta.Sign() < 0 {
		p = p.requireAvailable(wallet)
	}
	return p, nil
}

// NewLakeCollectionReward grants game credits and cumulative donation credit.
// Location eligibility and its permanent claim belong to the caller's transaction.
func NewLakeCollectionReward(meta Meta, wallets AccountPair, external int64) (Plan, error) {
	if meta.ActorUserID <= 0 || !wallets.valid() || external <= 0 {
		return Plan{}, ErrInvalidPlan
	}
	p, err := operationPlan(meta, KindLakeCollectionReward)
	if err != nil {
		return Plan{}, err
	}
	credits := AmountFromMilli(100000000)
	p = p.add(roleForAsset(userRole(wallets.Game), Game), credits).
		add(roleForAsset(externalRole(external), Game), negate(credits))
	p.spec.donation = &donationChange{userID: meta.ActorUserID, accountID: wallets.General, delta: AmountFromMilli(50000)}
	return p, nil
}
