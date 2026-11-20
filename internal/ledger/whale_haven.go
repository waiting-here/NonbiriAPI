package ledger

// NewWhaleFeedPurchase spends only the player's game credits.
func NewWhaleFeedPurchase(meta Meta, wallet, platform int64, cost Amount) (Plan, error) {
	if meta.ActorUserID <= 0 || wallet <= 0 || platform <= 0 || !positive(cost) || !validPrimitive(cost) {
		return Plan{}, ErrInvalidPlan
	}
	p, err := operationPlan(meta, KindWhaleFeedPurchase)
	if err != nil {
		return Plan{}, err
	}
	return p.add(roleForAsset(userRole(wallet), Game), negate(cost)).
		add(roleForAsset(platformRole(platform), Game), cost).requireAvailable(wallet), nil
}

// NewWhaleExchange issues the exact gross and pays the configured cuts.
func NewWhaleExchange(meta Meta, wallet, external, platform, welfare, thursday int64, net, platformCut, welfareCut, thursdayCut Amount) (Plan, error) {
	if meta.ActorUserID <= 0 || wallet <= 0 || external <= 0 || !positive(net) || !validPrimitive(net) {
		return Plan{}, ErrInvalidPlan
	}
	gross, err := addAmounts(net, platformCut, welfareCut, thursdayCut)
	if err != nil || !validPrimitive(gross) {
		return Plan{}, ErrInvalidPlan
	}
	p, err := operationPlan(meta, KindWhaleExchange)
	if err != nil {
		return Plan{}, err
	}
	p = p.add(externalRole(external), negate(gross)).add(userRole(wallet), net)
	for _, item := range []struct {
		id   int64
		role accountRole
		cut  Amount
	}{{platform, platformRole(platform), platformCut}, {welfare, poolRole(welfare), welfareCut}, {thursday, poolRole(thursday), thursdayCut}} {
		if item.cut.Sign() < 0 || !validPrimitive(item.cut) {
			return Plan{}, ErrInvalidPlan
		}
		if !item.cut.IsZero() {
			if item.id <= 0 {
				return Plan{}, ErrInvalidPlan
			}
			p = p.add(item.role, item.cut)
		}
	}
	return p, nil
}
