package rewards

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/gacha"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
)

type ManagedResource struct {
	rewardasset.Asset
	Quantity string `json:"quantity"`
	Minimum  string `json:"minimum"`
	Capacity string `json:"capacity"`
}

type ResourceAdjustment struct {
	Asset            string `json:"asset"`
	Direction        string `json:"direction"`
	Quantity         string `json:"quantity"`
	ExpectedQuantity string `json:"expected_quantity"`
	Reason           string `json:"reason"`
}

type ResourceAdjustmentResult struct {
	Resources []ManagedResource    `json:"resources"`
	Changes   []rewardasset.Change `json:"changes"`
}

func managedAsset(id string) (rewardasset.Asset, bool) {
	a, ok := assetByID[id]
	if id == "general" || id == "game" {
		return rewardasset.Asset{}, false
	}
	if strings.HasPrefix(id, "gacha:card:") {
		a.Maximum = "1"
	}
	return a, ok
}

func wholeQuantity(raw string) (*big.Int, bool) {
	if raw == "" || len(raw) > 40 || len(raw) > 1 && raw[0] == '0' {
		return nil, false
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(raw, 10)
	return n, ok
}

func ValidateResourceAdjustment(input ResourceAdjustment) error {
	a, ok := managedAsset(input.Asset)
	quantity, valid := wholeQuantity(input.Quantity)
	_, expectedValid := wholeQuantity(input.ExpectedQuantity)
	if !ok || !valid || !expectedValid || quantity.Sign() <= 0 || input.Direction != "increase" && input.Direction != "decrease" {
		return ErrInvalid
	}
	maximum, _ := new(big.Int).SetString(a.Maximum, 10)
	if quantity.Cmp(maximum) > 0 {
		return ErrInvalid
	}
	return nil
}

// ManagedResourcesTx is a bounded catalog projection, never a reward delivery.
// It deliberately excludes core credit assets, whose authority is unchanged.
func ManagedResourcesTx(ctx context.Context, tx *sql.Tx, user int64) ([]ManagedResource, error) {
	balances := map[string]string{}
	for _, asset := range []ledger.Asset{ledger.SketchPaper, ledger.SketchBrush} {
		wallet, err := ledger.UserAssetAccount(ctx, tx, user, asset)
		if errors.Is(err, ledger.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		quantity, remainder := new(big.Int), new(big.Int)
		quantity.QuoRem(wallet.Balance.Big(), big.NewInt(1000), remainder)
		if quantity.Sign() < 0 || remainder.Sign() != 0 {
			return nil, ledger.ErrInvariant
		}
		balances[string(asset)] = quantity.String()
	}
	for _, read := range []func(context.Context, *sql.Tx, int64) (map[string]string, error){gacha.RewardBalancesTx, lakenotes.RewardBalancesTx} {
		values, err := read(ctx, tx, user)
		if err != nil {
			return nil, err
		}
		for id, quantity := range values {
			balances[id] = quantity
		}
	}
	out := make([]ManagedResource, 0, len(assets)-2)
	for _, original := range assets {
		a, ok := managedAsset(original.ID)
		if !ok {
			continue
		}
		quantity := balances[a.ID]
		if quantity == "" {
			quantity = "0"
		}
		minimum, capacity := "0", a.Maximum
		if a.Group == "drawing" {
			capacity = new(big.Int).Quo(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1)), big.NewInt(1000)).String()
		}
		if a.ID == "lake:coins" || a.ID == "lake:xp" {
			capacity = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1)).String()
		}
		if a.ID == "lake:gear:bambooPole" && quantity != "0" {
			minimum = "1"
		}
		out = append(out, ManagedResource{Asset: a, Quantity: quantity, Minimum: minimum, Capacity: capacity})
	}
	return out, nil
}

// AdjustResourceTx is called only in an authorized management transaction. It
// compares the actual inventory before mutation, including on concurrent play.
// The caller must roll back all changes if any validation or write fails.
func AdjustResourceTx(ctx context.Context, tx *sql.Tx, user, actor, at int64, input ResourceAdjustment) (ResourceAdjustmentResult, error) {
	var result ResourceAdjustmentResult
	if err := ValidateResourceAdjustment(input); err != nil {
		return result, err
	}
	before, err := ManagedResourcesTx(ctx, tx, user)
	if err != nil {
		return result, err
	}
	var selected ManagedResource
	for _, item := range before {
		if item.ID == input.Asset {
			selected = item
			break
		}
	}
	if selected.Quantity != input.ExpectedQuantity {
		return result, ErrConflict
	}
	current, _ := wholeQuantity(selected.Quantity)
	delta, _ := wholeQuantity(input.Quantity)
	if input.Direction == "decrease" {
		delta.Neg(delta)
	}
	next := new(big.Int).Add(current, delta)
	minimum, _ := wholeQuantity(selected.Minimum)
	capacity, _ := wholeQuantity(selected.Capacity)
	if next.Cmp(minimum) < 0 || next.Cmp(capacity) > 0 {
		return result, ErrCapacity
	}
	var dependent []rewardasset.Change
	switch {
	case strings.HasPrefix(input.Asset, "gacha:"):
		err = gacha.SetManagedResourceTx(ctx, tx, user, at, input.Asset, next.String())
	case strings.HasPrefix(input.Asset, "lake:"):
		dependent, err = lakenotes.SetManagedResourceTx(ctx, tx, user, at, input.Asset, next.String())
	default:
		asset := ledger.Asset(input.Asset)
		wallet, e := ledger.CreateUserAssetAccount(ctx, tx, user, asset, at)
		if e != nil {
			return result, e
		}
		external, e := ledger.CodedAssetAccount(ctx, tx, "external", asset)
		if e != nil {
			return result, e
		}
		amount, e := ledger.AmountFromBig(new(big.Int).Mul(delta, big.NewInt(1000)))
		if e != nil {
			return result, e
		}
		id, e := db.GenerateOpaqueID("op_")
		if e != nil {
			return result, e
		}
		plan, e := ledger.NewAdminDrawingAdjustment(ledger.Meta{OperationID: id, ActorUserID: actor, CreatedAt: at}, wallet.ID, external.ID, asset, amount, input.Reason)
		if e != nil {
			return result, e
		}
		_, err = ledger.Apply(ctx, tx, plan)
	}
	if err != nil {
		if errors.Is(err, lakenotes.ErrConflict) {
			return result, ErrConflict
		}
		if errors.Is(err, gacha.ErrInvalid) || errors.Is(err, lakenotes.ErrInvalid) {
			return result, ErrInvalid
		}
		if errors.Is(err, gacha.ErrCapacity) || errors.Is(err, lakenotes.ErrCapacity) || errors.Is(err, ledger.ErrCapacityExhausted) {
			return result, ErrCapacity
		}
		return result, err
	}
	result.Resources, err = ManagedResourcesTx(ctx, tx, user)
	if err != nil {
		return ResourceAdjustmentResult{}, err
	}
	result.Changes = make([]rewardasset.Change, 0, 1+len(dependent))
	for i, item := range result.Resources {
		if item.Quantity != before[i].Quantity {
			result.Changes = append(result.Changes, rewardasset.Change{Field: item.ID, Before: before[i].Quantity, After: item.Quantity})
		}
	}
	result.Changes = append(result.Changes, dependent...)
	return result, nil
}
