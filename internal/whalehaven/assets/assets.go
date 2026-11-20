// Package assets stores Whale Haven currencies for all accounts, including
// players who have received a site reward before adopting a whale.
package assets

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
)

const Pearls = "whale:pearls"
const Shards = "whale:conch_shards"
const MaxBalance int64 = 9_000_000_000_000

var ErrCapacity = errors.New("whale currency balance outside supported range")

type Balance struct {
	Pearls int64
	Shards int64
}

func Catalog() []rewardasset.Asset {
	const max = "9000000000000"
	return []rewardasset.Asset{
		{ID: Pearls, Name: "小鲸币", NameEN: "Whale coins", Group: "whale", Maximum: max},
		{ID: Shards, Name: "海螺碎片", NameEN: "Conch shards", Group: "whale", Maximum: max},
	}
}

func Read(ctx context.Context, tx *sql.Tx, user int64) (Balance, error) {
	var b Balance
	err := tx.QueryRowContext(ctx, "SELECT pearls,shards FROM whale_haven_assets WHERE user_id=?", user).Scan(&b.Pearls, &b.Shards)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return b, err
}

func RewardBalancesTx(ctx context.Context, tx *sql.Tx, user int64) (map[string]string, error) {
	b, err := Read(ctx, tx, user)
	if err != nil {
		return nil, err
	}
	return map[string]string{Pearls: strconv.FormatInt(b.Pearls, 10), Shards: strconv.FormatInt(b.Shards, 10)}, nil
}

func validChange(balance, delta int64) bool { return delta >= -balance && delta <= MaxBalance-balance }

// MoveTx records one atomic two-currency movement with the transaction's other effects.
func MoveTx(ctx context.Context, tx *sql.Tx, user, atMS int64, delta Balance, reason, operation string) error {
	if delta == (Balance{}) {
		return nil
	}
	b, err := Read(ctx, tx, user)
	if err != nil {
		return err
	}
	if !validChange(b.Pearls, delta.Pearls) || !validChange(b.Shards, delta.Shards) {
		return ErrCapacity
	}
	b.Pearls += delta.Pearls
	b.Shards += delta.Shards
	_, err = tx.ExecContext(ctx, `INSERT INTO whale_haven_assets(user_id,pearls,shards) VALUES(?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET pearls=excluded.pearls,shards=excluded.shards`, user, b.Pearls, b.Shards)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO whale_haven_movements(user_id,created_at,reason,operation_id,pearls_delta,shards_delta,pearls_after,shards_after)
 VALUES(?,?,?,?,?,?,?,?)`, user, atMS, reason, operation, delta.Pearls, delta.Shards, b.Pearls, b.Shards)
	return err
}

func ExternalChange(ctx context.Context, tx *sql.Tx, user, at int64, asset string, n int64, reason, sourceID string) error {
	var delta Balance
	switch asset {
	case Pearls:
		delta.Pearls = n
	case Shards:
		delta.Shards = n
	default:
		return ErrCapacity
	}
	if err := MoveTx(ctx, tx, user, at*1000, delta, reason, sourceID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE whale_haven_profiles SET revision=revision+1 WHERE user_id=?", user)
	return err
}

func GrantRewardTx(ctx context.Context, tx *sql.Tx, user, at int64, item rewardasset.Item, reason, sourceID string) (rewardasset.Result, error) {
	result := rewardasset.Result{Item: item, Granted: item.Quantity}
	n, err := strconv.ParseInt(item.Quantity, 10, 64)
	if err != nil || n <= 0 {
		return result, ErrCapacity
	}
	return result, ExternalChange(ctx, tx, user, at, item.Asset, n, reason, sourceID)
}
