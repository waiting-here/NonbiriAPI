package rewards

import (
	"context"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/gacha"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"math/big"
	"strconv"
)

func validateCondition(c Condition) error { nodes := 0; return checkCondition(c, 0, &nodes) }
func checkCondition(c Condition, depth int, nodes *int) error {
	*nodes++
	if depth > 8 || *nodes > 64 {
		return ErrInvalid
	}
	group := c.Field == "" && c.Asset == "" && c.Compare == "" && c.Value == ""
	switch c.Op {
	case "all":
		if !group || len(c.Children) != 0 {
			return ErrInvalid
		}
	case "and", "or", "not":
		if !group || len(c.Children) < 1 || len(c.Children) > 16 || c.Op == "not" && len(c.Children) != 1 {
			return ErrInvalid
		}
		for _, child := range c.Children {
			if err := checkCondition(child, depth+1, nodes); err != nil {
				return err
			}
		}
	case "compare":
		if len(c.Children) > 0 {
			return ErrInvalid
		}
		switch c.Compare {
		case "eq", "ne", "gt", "gte", "lt", "lte":
		default:
			return ErrInvalid
		}
		precision := 0
		signed := false
		switch c.Field {
		case "level":
			n, e := strconv.Atoi(c.Value)
			if e != nil || n < 1 || n > 6 || c.Asset != "" {
				return ErrInvalid
			}
		case "banned":
			if c.Asset != "" || c.Value != "true" && c.Value != "false" || c.Compare != "eq" && c.Compare != "ne" {
				return ErrInvalid
			}
			return nil
		case "donation":
			precision = 3
			if c.Asset != "" {
				return ErrInvalid
			}
		case "registered_at", "registered_days":
			if c.Asset != "" {
				return ErrInvalid
			}
		case "asset":
			a, ok := assetByID[c.Asset]
			if !ok {
				return ErrInvalid
			}
			precision = a.Precision
			signed = c.Asset == "general" || c.Asset == "game"
		default:
			return ErrInvalid
		}
		if _, err := decimal(c.Value, precision, signed); err != nil {
			return err
		}
	default:
		return ErrInvalid
	}
	return nil
}

type facts struct {
	user, created, at int64
	level             int
	banned            bool
	donation          *big.Rat
	assets            map[string]*big.Rat
}

func loadFacts(ctx context.Context, tx *sql.Tx, user, at int64, withAssets bool) (facts, error) {
	f := facts{user: user, at: at, assets: map[string]*big.Rat{}}
	var raw []byte
	err := tx.QueryRowContext(ctx, "SELECT COALESCE(level,auto_level),created_at,donation_credit_mag,(is_banned=1 AND (banned_until IS NULL OR banned_until>?)) FROM users WHERE id=? AND is_admin=0 AND NOT EXISTS(SELECT 1 FROM user_deletion_markers WHERE user_id=users.id)", at, user).Scan(&f.level, &f.created, &raw, &f.banned)
	if err != nil {
		return f, err
	}
	n, err := db.DecodeU128(raw)
	if err != nil {
		return f, err
	}
	f.donation = new(big.Rat).SetFrac(n.Big(), big.NewInt(1000))
	if !withAssets {
		return f, nil
	}
	for _, asset := range ledger.Assets() {
		a, e := ledger.UserAssetAccount(ctx, tx, user, asset)
		if e != nil {
			if errors.Is(e, ledger.ErrNotFound) {
				continue
			}
			return f, e
		}
		f.assets[string(asset)] = new(big.Rat).SetFrac(a.Balance.Big(), big.NewInt(1000))
	}
	for _, read := range []func(context.Context, *sql.Tx, int64) (map[string]string, error){gacha.RewardBalancesTx, lakenotes.RewardBalancesTx} {
		balances, e := read(ctx, tx, user)
		if e != nil {
			return f, e
		}
		for id, s := range balances {
			n, ok := new(big.Rat).SetString(s)
			if !ok {
				return f, ErrInvalid
			}
			f.assets[id] = n
		}
	}
	return f, nil
}
func usesAssets(c Condition) bool {
	if c.Field == "asset" {
		return true
	}
	for _, child := range c.Children {
		if usesAssets(child) {
			return true
		}
	}
	return false
}
func matches(c Condition, f facts) bool {
	switch c.Op {
	case "all":
		return true
	case "not":
		return !matches(c.Children[0], f)
	case "and":
		for _, child := range c.Children {
			if !matches(child, f) {
				return false
			}
		}
		return true
	case "or":
		for _, child := range c.Children {
			if matches(child, f) {
				return true
			}
		}
		return false
	}
	if c.Field == "banned" {
		eq := f.banned == (c.Value == "true")
		if c.Compare == "ne" {
			return !eq
		}
		return eq
	}
	actual := new(big.Rat)
	switch c.Field {
	case "level":
		actual.SetInt64(int64(f.level))
	case "registered_at":
		actual.SetInt64(f.created)
	case "registered_days":
		actual.SetInt64(max(0, (f.at-f.created)/86400))
	case "donation":
		actual = f.donation
	case "asset":
		if n, ok := f.assets[c.Asset]; ok {
			actual = n
		}
	}
	value, _ := new(big.Rat).SetString(c.Value)
	n := actual.Cmp(value)
	switch c.Compare {
	case "eq":
		return n == 0
	case "ne":
		return n != 0
	case "gt":
		return n > 0
	case "gte":
		return n >= 0
	case "lt":
		return n < 0
	case "lte":
		return n <= 0
	}
	return false
}
