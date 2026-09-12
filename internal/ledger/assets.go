package ledger

import "github.com/waiting-here/NonbiriAPI/internal/db"

// AccountPair names the separate accounts for the two assets.
type AccountPair struct {
	General int64
	Game    int64
}

func (p AccountPair) valid() bool { return p.General > 0 && p.Game > 0 && p.General != p.Game }

// Payment records the original funding split; refunds use these amounts.
type Payment struct {
	General Amount
	Game    Amount
}

// SplitGamePayment spends positive game credits first. Debt in one wallet
// does not consume the other wallet's available balance.
func SplitGamePayment(cost, generalBalance, gameBalance Amount) (Payment, error) {
	if !nonnegative(cost) {
		return Payment{}, ErrInvalidAmount
	}
	game := Amount{}
	if gameBalance.Sign() > 0 {
		game = gameBalance
		if game.Big().Cmp(cost.Big()) > 0 {
			game = cost
		}
	}
	general, err := subtractAmounts(cost, game)
	if err != nil {
		return Payment{}, err
	}
	if general.Sign() > 0 && general.Big().Cmp(generalBalance.Big()) > 0 {
		return Payment{}, ErrInsufficientBalance
	}
	return Payment{General: general, Game: game}, nil
}

func roleForAsset(role accountRole, asset Asset) accountRole {
	role.asset = asset
	return role
}

func NewGameCheckinAward(meta Meta, userAccountID, externalAccountID int64, award Amount) (Plan, error) {
	plan, err := NewCheckinAward(meta, userAccountID, externalAccountID, award)
	if err != nil {
		return Plan{}, err
	}
	for i := range plan.spec.entries {
		plan.spec.entries[i].role.asset = Game
	}
	return plan, nil
}

// NewAdminGameAdjustment changes only game credits, without donation credit.
func NewAdminGameAdjustment(meta Meta, userAccountID, externalAccountID int64, delta Amount, reason string) (Plan, error) {
	plan, err := NewAdminUserAdjustment(meta, userAccountID, externalAccountID, delta, 0, Amount{}, reason)
	if err != nil {
		return Plan{}, err
	}
	for i := range plan.spec.entries {
		plan.spec.entries[i].role.asset = Game
	}
	return plan, nil
}

// NewWalletsDeleteZero clears both user assets in one operation.
func NewWalletsDeleteZero(meta Meta, wallets, external AccountPair) (Plan, error) {
	if !wallets.valid() || !external.valid() {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := NewAccountDeleteZero(meta, wallets.General, external.General)
	if err != nil {
		return Plan{}, err
	}
	plan = plan.add(roleForAsset(userRole(wallets.Game), Game), Amount{}).
		add(roleForAsset(externalRole(external.Game), Game), Amount{})
	return plan, nil
}

// NewGameWelfareClaim exchanges the general pool allocation into game
// credits through the corresponding external accounts.
func NewGameWelfareClaim(meta Meta, poolAccountID, gameWalletID int64, external AccountPair, award Amount) (Plan, error) {
	if poolAccountID <= 0 || gameWalletID <= 0 || !external.valid() || !positive(award) || !validPrimitive(award) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := operationPlan(meta, KindWelfareClaim)
	if err != nil {
		return Plan{}, err
	}
	return plan.add(poolRole(poolAccountID), negate(award)).
		add(externalRole(external.General), award).
		add(roleForAsset(externalRole(external.Game), Game), negate(award)).
		add(roleForAsset(userRole(gameWalletID), Game), award), nil
}

// NewGameOnboardingReward consumes the independent accepted reward hold.
// The domain adapter verifies task eligibility and the completion unique key
// in the same transaction; rewards always go to the general wallet.
func NewGameOnboardingReward(meta Meta, holdID string, userAccountID, externalAccountID int64, award Amount) (Plan, error) {
	if userAccountID <= 0 || externalAccountID <= 0 || !positive(award) || !validPrimitive(award) {
		return Plan{}, ErrInvalidPlan
	}
	ref, err := GameOnboardingHold(holdID)
	if err != nil {
		return Plan{}, err
	}
	plan, err := newPlan(meta, KindGameOnboardingReward, sourceOperation, meta.OperationID, db.U128{})
	if err != nil {
		return Plan{}, err
	}
	return plan.add(externalRole(externalAccountID), negate(award)).
		add(userRole(userAccountID), award).consume(ref), nil
}
