package ledger

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// SketchAccounts deliberately cannot be passed to the existing game APIs.
type SketchAccounts struct{ Paper, Brush int64 }

func (p SketchAccounts) valid() bool { return p.Paper > 0 && p.Brush > 0 && p.Paper != p.Brush }

// SketchPayment contains independent integer-currency amounts in milliunits.
// The two values are never summed or exchanged with each other.
type SketchPayment struct{ Paper, Brush Amount }

func (p SketchPayment) valid() bool {
	return p.Paper.Sign() >= 0 && p.Brush.Sign() >= 0 &&
		(p.Paper.Sign() > 0 || p.Brush.Sign() > 0) &&
		validAssetAmount(SketchPaper, p.Paper) && validAssetAmount(SketchBrush, p.Brush)
}

func validAssetAmount(asset Asset, amount Amount) bool {
	if !asset.valid() {
		return false
	}
	if _, err := amountFromScalar(amount.value); err != nil {
		return false
	}
	return !asset.IsActivity() || new(big.Int).Mod(amount.Big(), big.NewInt(1000)).Sign() == 0
}

func assetTotals() map[Asset]*big.Int {
	out := make(map[Asset]*big.Int, 4)
	for _, asset := range Assets() {
		out[asset] = new(big.Int)
	}
	return out
}

func conserved(totals map[Asset]*big.Int) bool {
	for _, value := range totals {
		if value.Sign() != 0 {
			return false
		}
	}
	return true
}

// CreateSketchAccounts lazily creates both zero wallets in the caller's first
// exchange transaction. It does not grant funds or affect general/game wallets.
func CreateSketchAccounts(ctx context.Context, tx *sql.Tx, userID, at int64) (SketchAccounts, error) {
	paper, err := CreateUserAssetAccount(ctx, tx, userID, SketchPaper, at)
	if err != nil {
		return SketchAccounts{}, err
	}
	brush, err := CreateUserAssetAccount(ctx, tx, userID, SketchBrush, at)
	if err != nil {
		return SketchAccounts{}, err
	}
	return SketchAccounts{Paper: paper.ID, Brush: brush.ID}, nil
}

// NewActivityExchange retires the general payment and independently issues
// the purchased activity asset. The domain owns cap and receipt updates in tx.
func NewActivityExchange(meta Meta, generalWallet, generalExternal, activityWallet, activityExternal int64, asset Asset, cost, quantity Amount) (Plan, error) {
	if meta.ActorUserID <= 0 || !asset.IsActivity() || !positive(cost) || !validAssetAmount(General, cost) ||
		!positive(quantity) || !validAssetAmount(asset, quantity) ||
		generalWallet <= 0 || generalExternal <= 0 || activityWallet <= 0 || activityExternal <= 0 {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := operationPlan(meta, KindActivityExchange)
	if err != nil {
		return Plan{}, err
	}
	return plan.add(userRole(generalWallet), negate(cost)).add(externalRole(generalExternal), cost).
		add(roleForAsset(externalRole(activityExternal), asset), negate(quantity)).
		add(roleForAsset(userRole(activityWallet), asset), quantity).requireAvailable(generalWallet), nil
}

func imagePlan(meta Meta, kind Kind, taskID string, payment SketchPayment) (Plan, ReservationRef, error) {
	if !payment.valid() {
		return Plan{}, ReservationRef{}, ErrInvalidPlan
	}
	ref, err := ImageTaskReservation(taskID)
	if err != nil {
		return Plan{}, ReservationRef{}, err
	}
	plan, err := newPlan(meta, kind, sourceImageTask, taskID, db.U128{})
	return plan, ref, err
}

func imageReserveRole(id int64, asset Asset) accountRole {
	return roleForAsset(reserveRole(id, "image_activity_reserve"), asset)
}

// NewImageReserve precharges both prices atomically; the caller also reserves
// one future ledger row and inserts the task in the same outer transaction.
func NewImageReserve(meta Meta, taskID string, wallets, escrow SketchAccounts, price SketchPayment) (Plan, error) {
	if !wallets.valid() || !escrow.valid() || meta.ActorUserID <= 0 {
		return Plan{}, ErrInvalidPlan
	}
	plan, _, err := imagePlan(meta, KindImageReserve, taskID, price)
	if err != nil {
		return Plan{}, err
	}
	return plan.add(roleForAsset(userRole(wallets.Paper), SketchPaper), negate(price.Paper)).
		add(imageReserveRole(escrow.Paper, SketchPaper), price.Paper).
		add(roleForAsset(userRole(wallets.Brush), SketchBrush), negate(price.Brush)).
		add(imageReserveRole(escrow.Brush, SketchBrush), price.Brush).
		requireAvailable(wallets.Paper).requireAvailable(wallets.Brush), nil
}

func imageTerminal(meta Meta, kind Kind, taskID string, escrow, destination SketchAccounts, price SketchPayment, refund bool) (Plan, error) {
	if !escrow.valid() || !destination.valid() || meta.ActorUserID != 0 {
		return Plan{}, ErrInvalidPlan
	}
	plan, ref, err := imagePlan(meta, kind, taskID, price)
	if err != nil {
		return Plan{}, err
	}
	paper, brush := externalRole(destination.Paper), externalRole(destination.Brush)
	if refund {
		paper, brush = userRole(destination.Paper), userRole(destination.Brush)
	}
	return plan.add(imageReserveRole(escrow.Paper, SketchPaper), negate(price.Paper)).
		add(roleForAsset(paper, SketchPaper), price.Paper).
		add(imageReserveRole(escrow.Brush, SketchBrush), negate(price.Brush)).
		add(roleForAsset(brush, SketchBrush), price.Brush).consume(ref), nil
}

// NewImageSettle retires the full accepted price, including partial success.
func NewImageSettle(meta Meta, taskID string, escrow, external SketchAccounts, price SketchPayment) (Plan, error) {
	return imageTerminal(meta, KindImageSettle, taskID, escrow, external, price, false)
}

func NewImageRefund(meta Meta, taskID string, escrow, wallets SketchAccounts, price SketchPayment) (Plan, error) {
	return imageTerminal(meta, KindImageRefund, taskID, escrow, wallets, price, true)
}

// NewImageDeleteFinalize retires outstanding funds before removing the owner.
// A later unowned upstream callback must not post another financial operation.
func NewImageDeleteFinalize(meta Meta, taskID string, escrow, external SketchAccounts, price SketchPayment) (Plan, error) {
	return imageTerminal(meta, KindImageDeleteFinalize, taskID, escrow, external, price, false)
}

// NewInactivityDecay only accepts the original general/game wallet pair.
// A zero result is handled by the policy cursor without an empty operation.
func NewInactivityDecay(meta Meta, wallets, external AccountPair, amount Payment) (Plan, error) {
	if meta.ActorUserID != 0 || !wallets.valid() || !external.valid() || amount.General.Sign() < 0 || amount.Game.Sign() < 0 ||
		(amount.General.IsZero() && amount.Game.IsZero()) || !validAssetAmount(General, amount.General) || !validAssetAmount(Game, amount.Game) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := operationPlan(meta, KindInactivityDecay)
	if err != nil {
		return Plan{}, err
	}
	if amount.General.Sign() > 0 {
		plan = plan.add(userRole(wallets.General), negate(amount.General)).add(externalRole(external.General), amount.General).requireAvailable(wallets.General)
	}
	if amount.Game.Sign() > 0 {
		plan = plan.add(roleForAsset(userRole(wallets.Game), Game), negate(amount.Game)).add(roleForAsset(externalRole(external.Game), Game), amount.Game).requireAvailable(wallets.Game)
	}
	return plan, nil
}

// AssetWallet names one existing wallet and its matching external account.
type AssetWallet struct {
	Asset                Asset
	WalletID, ExternalID int64
}

// NewAssetWalletsDeleteZero clears every supplied wallet at its actual signed
// balance. Missing, never-created activity wallets need not be constructed.
func NewAssetWalletsDeleteZero(meta Meta, wallets []AssetWallet) (Plan, error) {
	if len(wallets) == 0 || len(wallets) > len(Assets()) {
		return Plan{}, ErrInvalidPlan
	}
	plan, err := operationPlan(meta, KindAccountDeleteZero)
	if err != nil {
		return Plan{}, err
	}
	seen := make(map[Asset]bool, len(wallets))
	for _, wallet := range wallets {
		if !wallet.Asset.valid() || seen[wallet.Asset] || wallet.WalletID <= 0 || wallet.ExternalID <= 0 || wallet.WalletID == wallet.ExternalID {
			return Plan{}, ErrInvalidPlan
		}
		seen[wallet.Asset] = true
		plan = plan.add(roleForAsset(userRole(wallet.WalletID), wallet.Asset), Amount{}).
			add(roleForAsset(externalRole(wallet.ExternalID), wallet.Asset), Amount{}).requireZero(wallet.WalletID)
	}
	plan.spec.dynamic = dynamicAccountDelete
	return plan, nil
}
