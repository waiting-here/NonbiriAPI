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
