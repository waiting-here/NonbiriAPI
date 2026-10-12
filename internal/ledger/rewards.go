package ledger

// NewRewardGrant credits an asset without changing cumulative donation credit.
// The caller commits the complete bundle and its unique receipt together.
func NewRewardGrant(meta Meta, userAccountID, externalAccountID int64, asset Asset, amount Amount, reason string) (Plan, error) {
	if userAccountID <= 0 || externalAccountID <= 0 || !asset.valid() || !positive(amount) || !validPrimitive(amount) || !validAssetAmount(asset, amount) || !validReason(reason) {
		return Plan{}, ErrInvalidPlan
	}
	p, err := operationPlan(meta, KindRewardGrant)
	if err != nil {
		return Plan{}, err
	}
	p.spec.reason = reason
	return p.add(roleForAsset(userRole(userAccountID), asset), amount).add(roleForAsset(externalRole(externalAccountID), asset), negate(amount)), nil
}
