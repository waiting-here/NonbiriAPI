package rewards

import (
	"context"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/gacha"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
	"math/big"
	"strings"
)

var assets = func() []rewardasset.Asset {
	out := []rewardasset.Asset{
		{ID: "general", Name: "通用积分", NameEN: "General credits", Group: "credits", Precision: 3, Maximum: "9000000000000"},
		{ID: "game", Name: "游戏积分", NameEN: "Game credits", Group: "credits", Precision: 3, Maximum: "9000000000000"},
		{ID: "sketch_paper", Name: "草稿纸", NameEN: "Sketch paper", Group: "drawing", Maximum: "9000000000000"},
		{ID: "sketch_brush", Name: "画笔", NameEN: "Paintbrush", Group: "drawing", Maximum: "9000000000000"},
	}
	out = append(out, gacha.RewardAssets()...)
	return append(out, lakenotes.RewardAssets()...)
}()
var assetByID = func() map[string]rewardasset.Asset {
	out := map[string]rewardasset.Asset{}
	for _, a := range assets {
		out[a.ID] = a
	}
	return out
}()

func Assets() []rewardasset.Asset { return append([]rewardasset.Asset(nil), assets...) }

// Catalog metadata must not reveal cards the account has never obtained.
func userAssets(ctx context.Context, tx *sql.Tx, user int64) ([]rewardasset.Asset, error) {
	owned, err := gacha.RewardBalancesTx(ctx, tx, user)
	if err != nil {
		return nil, err
	}
	out := []rewardasset.Asset{}
	for _, asset := range assets {
		if strings.HasPrefix(asset.ID, "gacha:card:") && owned[asset.ID] != "1" {
			continue
		}
		out = append(out, asset)
	}
	return out, nil
}
func decimal(s string, precision int, signed bool) (*big.Rat, error) {
	if len(s) < 1 || len(s) > 64 {
		return nil, ErrInvalid
	}
	raw := s
	if signed && strings.HasPrefix(raw, "-") {
		raw = raw[1:]
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || len(parts[0]) == 0 || len(parts[0]) > 1 && parts[0][0] == '0' {
		return nil, ErrInvalid
	}
	for _, p := range parts {
		if len(p) == 0 {
			return nil, ErrInvalid
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil, ErrInvalid
			}
		}
	}
	if len(parts) == 2 && (precision == 0 || len(parts[1]) > precision) {
		return nil, ErrInvalid
	}
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, ErrInvalid
	}
	return v, nil
}
func validateRewards(items []rewardasset.Item) error {
	if len(items) < 1 || len(items) > 32 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, item := range items {
		a, ok := assetByID[item.Asset]
		if !ok || seen[item.Asset] {
			return ErrInvalid
		}
		seen[item.Asset] = true
		v, err := decimal(item.Quantity, a.Precision, false)
		if err != nil || v.Sign() <= 0 {
			return ErrInvalid
		}
		maximum, _ := new(big.Rat).SetString(a.Maximum)
		if v.Cmp(maximum) > 0 {
			return ErrInvalid
		}
		if !strings.Contains(item.Asset, ":") {
			m := new(big.Rat).Mul(v, big.NewRat(1000, 1))
			if !m.IsInt() || m.Num().Cmp(big.NewInt(db.MaxMoneyMilli)) > 0 {
				return ErrInvalid
			}
		}
	}
	return nil
}
func grant(ctx context.Context, tx *sql.Tx, user, actor, at int64, title string, items []rewardasset.Item) ([]rewardasset.Result, error) {
	out := []rewardasset.Result{}
	for _, item := range items {
		var r rewardasset.Result
		var err error
		switch {
		case strings.HasPrefix(item.Asset, "gacha:"):
			r, err = gacha.GrantRewardTx(ctx, tx, user, at, item)
		case strings.HasPrefix(item.Asset, "lake:"):
			r, err = lakenotes.GrantRewardTx(ctx, tx, user, at, item)
		default:
			asset := ledger.Asset(item.Asset)
			wallet, e := ledger.CreateUserAssetAccount(ctx, tx, user, asset, at)
			if e != nil {
				return nil, e
			}
			external, e := ledger.CodedAssetAccount(ctx, tx, "external", asset)
			if e != nil {
				return nil, e
			}
			quantity, e := decimal(item.Quantity, assetByID[item.Asset].Precision, false)
			if e != nil {
				return nil, e
			}
			milli := new(big.Rat).Mul(quantity, big.NewRat(1000, 1))
			amount, e := ledger.AmountFromBig(milli.Num())
			if e != nil {
				return nil, e
			}
			id, e := db.GenerateOpaqueID("op_")
			if e != nil {
				return nil, e
			}
			plan, e := ledger.NewRewardGrant(ledger.Meta{OperationID: id, ActorUserID: actor, CreatedAt: at}, wallet.ID, external.ID, asset, amount, title)
			if e != nil {
				return nil, e
			}
			_, err = ledger.Apply(ctx, tx, plan)
			r = rewardasset.Result{Item: item, Granted: item.Quantity}
		}
		if err != nil {
			if errors.Is(err, gacha.ErrCapacity) || errors.Is(err, lakenotes.ErrCapacity) || errors.Is(err, ledger.ErrCapacityExhausted) {
				return nil, ErrCapacity
			}
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
