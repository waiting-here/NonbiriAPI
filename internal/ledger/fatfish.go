package ledger

// NewFatFishUnlock charges a node unlock in general credits. The caller commits
// the unique node entitlement and its operation receipt in the same transaction.
func NewFatFishUnlock(meta Meta, userAccountID, platformAccountID int64, amount Amount) (Plan, error) {
	return fatFishCharge(meta, KindFatFishUnlock, userAccountID, platformAccountID, amount)
}

// NewFatFishTicket charges the frozen ticket price when a challenge starts.
func NewFatFishTicket(meta Meta, userAccountID, platformAccountID int64, amount Amount) (Plan, error) {
	return fatFishCharge(meta, KindFatFishTicket, userAccountID, platformAccountID, amount)
}

func fatFishCharge(meta Meta, kind Kind, userAccountID, platformAccountID int64, amount Amount) (Plan, error) {
	if meta.ActorUserID <= 0 || userAccountID <= 0 || platformAccountID <= 0 || !nonnegative(amount) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := operationPlan(meta, kind)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(userRole(userAccountID), negate(amount)).add(platformRole(platformAccountID), amount)
	if positive(amount) {
		plan = plan.requireAvailable(userAccountID)
	}
	return plan, nil
}

// NewFatFishReward issues one independently deduplicated reward tier. The
// identity claim and this operation must be committed together by the caller.
func NewFatFishReward(meta Meta, externalAccountID, userAccountID int64, amount Amount) (Plan, error) {
	return fatFishCredit(meta, KindFatFishReward, externalRole(externalAccountID), userAccountID, amount)
}

// NewFatFishRefund returns a cancelled challenge's original ticket price. The
// permanent refund receipt must share the transaction with the terminal CAS.
func NewFatFishRefund(meta Meta, platformAccountID, userAccountID int64, amount Amount) (Plan, error) {
	return fatFishCredit(meta, KindFatFishRefund, platformRole(platformAccountID), userAccountID, amount)
}

func fatFishCredit(meta Meta, kind Kind, source accountRole, userAccountID int64, amount Amount) (Plan, error) {
	if meta.ActorUserID != 0 || source.id <= 0 || userAccountID <= 0 || !nonnegative(amount) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := operationPlan(meta, kind)
	if err != nil {
		return Plan{}, err
	}
	return plan.add(source, negate(amount)).add(userRole(userAccountID), amount), nil
}
