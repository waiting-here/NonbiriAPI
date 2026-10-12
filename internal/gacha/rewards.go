package gacha

import (
	"context"
	"database/sql"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
	"strconv"
	"strings"
)

var rewardWallets = []string{"gacha:permanent", "gacha:limited", "gacha:shard:N", "gacha:shard:R", "gacha:shard:SR", "gacha:shard:SSR"}

func RewardAssets() []rewardasset.Asset {
	names := []string{"常驻抽奖券", "限定抽奖券", "N卡牌碎片", "R卡牌碎片", "SR卡牌碎片", "SSR卡牌碎片"}
	en := []string{"Permanent ticket", "Limited ticket", "N shard", "R shard", "SR shard", "SSR shard"}
	out := []rewardasset.Asset{}
	for i, id := range rewardWallets {
		out = append(out, rewardasset.Asset{ID: id, Name: names[i], NameEN: en[i], Group: "gacha", Maximum: strconv.FormatInt(maxBalance, 10)})
	}
	for _, c := range catalog.Cards {
		out = append(out, rewardasset.Asset{ID: "gacha:card:" + c.ID, Name: c.Name, NameEN: c.NameEN, Group: "cards", Maximum: "1000"})
	}
	return out
}
func RewardBalancesTx(ctx context.Context, tx *sql.Tx, user int64) (map[string]string, error) {
	p, _, err := readProfile(ctx, tx, user)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for i, id := range rewardWallets {
		out[id] = strconv.FormatInt(p.Balance[i], 10)
	}
	owned, err := readOwned(ctx, tx, user)
	for id := range owned {
		out["gacha:card:"+id] = "1"
	}
	return out, err
}

// GrantRewardTx preserves welcome, check-in, pity and shop eligibility.
func GrantRewardTx(ctx context.Context, tx *sql.Tx, user, at int64, item rewardasset.Item) (rewardasset.Result, error) {
	out := rewardasset.Result{Item: item, Granted: item.Quantity}
	n, err := strconv.ParseInt(item.Quantity, 10, 64)
	if err != nil || n <= 0 || n > maxBalance {
		return out, ErrInvalid
	}
	p, exists, err := readProfile(ctx, tx, user)
	if err != nil {
		return out, err
	}
	if !exists {
		_, err = tx.ExecContext(ctx, "INSERT INTO gacha_profiles(user_id,month,entered_at,updated_at,welcome_granted) VALUES(?,'1970-01',0,?,0)", user, at)
		if err != nil {
			return out, err
		}
		p.Month = "1970-01"
	}
	index := -1
	for i, id := range rewardWallets {
		if item.Asset == id {
			index = i
			break
		}
	}
	if index >= 0 {
		if p.Balance[index] > maxBalance-n {
			return out, ErrCapacity
		}
		p.Balance[index] += n
	} else {
		id := strings.TrimPrefix(item.Asset, "gacha:card:")
		card, ok := cardsByID[id]
		if !ok || !strings.HasPrefix(item.Asset, "gacha:card:") || n > 1000 {
			return out, ErrInvalid
		}
		owned, err := readOwned(ctx, tx, user)
		if err != nil {
			return out, err
		}
		duplicate := n
		if _, ok := owned[id]; !ok {
			if _, err = tx.ExecContext(ctx, "INSERT INTO gacha_owned(user_id,card_id,obtained_at,source) VALUES(?,?,?,'reward')", user, id, at); err != nil {
				return out, err
			}
			duplicate--
		}
		out.Granted = strconv.FormatInt(n-duplicate, 10)
		if duplicate > 0 {
			for i, r := range rarities {
				if r == card.Rarity.Tier() {
					if p.Balance[i+2] > maxBalance-duplicate {
						return out, ErrCapacity
					}
					p.Balance[i+2] += duplicate
					out.Converted = &rewardasset.Item{Asset: rewardWallets[i+2], Quantity: strconv.FormatInt(duplicate, 10)}
				}
			}
		}
	}
	return out, saveProfile(ctx, tx, user, at, p)
}
