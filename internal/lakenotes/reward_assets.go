package lakenotes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
	"sort"
	"strconv"
	"strings"
)

func rewardCatalog() rules.Catalog {
	var c rules.Catalog
	if err := json.Unmarshal(rules.CatalogJSON(), &c); err != nil {
		panic(err)
	}
	return c
}
func RewardAssets() []rewardasset.Asset {
	out := []rewardasset.Asset{{ID: "lake:coins", Name: "垂钓金币", NameEN: "Lake coins", Group: "lake", Maximum: "9007199254740991"}, {ID: "lake:xp", Name: "垂钓经验", NameEN: "Lake experience", Group: "lake", Maximum: "9007199254740991"}}
	c := rewardCatalog()
	for id, g := range c.Gear {
		limit := "2"
		if g.Slot == "rod" {
			limit = "1"
		}
		out = append(out, rewardasset.Asset{ID: "lake:gear:" + id, Name: g.Name, NameEN: g.Name + " (Chinese)", Group: "lake", Maximum: limit})
	}
	for id, b := range c.Baits {
		out = append(out, rewardasset.Asset{ID: "lake:bait:" + id, Name: b.Name, NameEN: b.Name + " (Chinese)", Group: "lake", Maximum: "999"})
	}
	for id, d := range c.Debris {
		out = append(out, rewardasset.Asset{ID: "lake:debris:" + id, Name: d.Name, NameEN: d.Name + " (Chinese)", Group: "lake", Maximum: "9999"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func RewardBalancesTx(ctx context.Context, tx *sql.Tx, user int64) (map[string]string, error) {
	row, err := profileTx(ctx, tx, user, false, 0)
	if err != nil {
		return nil, err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM lake_notes_profiles WHERE user_id=?)", user).Scan(&exists); err != nil {
		return nil, err
	}
	out := map[string]string{}
	if !exists {
		return out, nil
	}
	out["lake:coins"] = string(row.profile.Coins)
	out["lake:xp"] = string(row.profile.XP)
	for id, n := range row.profile.BaitStock {
		out["lake:bait:"+id] = strconv.Itoa(n)
	}
	for id, n := range row.profile.DebrisStock {
		out["lake:debris:"+id] = strconv.Itoa(n)
	}
	for _, id := range row.profile.OwnedGear {
		out["lake:gear:"+id] = strconv.Itoa(row.profile.Copies(id))
	}
	return out, nil
}
func GrantRewardTx(ctx context.Context, tx *sql.Tx, user, at int64, item rewardasset.Item) (rewardasset.Result, error) {
	out := rewardasset.Result{Item: item, Granted: item.Quantity}
	n, err := strconv.ParseUint(item.Quantity, 10, 53)
	if err != nil || n == 0 {
		return out, ErrInvalid
	}
	row, err := profileTx(ctx, tx, user, true, at)
	if err != nil {
		return out, err
	}
	p := &row.profile
	switch {
	case item.Asset == "lake:coins":
		p.Coins, err = p.Coins.Add(rules.NewAmount(n))
	case item.Asset == "lake:xp":
		p.XP, err = p.XP.Add(rules.NewAmount(n))
	case strings.HasPrefix(item.Asset, "lake:gear:"):
		id := strings.TrimPrefix(item.Asset, "lake:gear:")
		g, ok := rules.Gear(id)
		if !ok {
			return out, ErrInvalid
		}
		cap := 2
		if g.Slot == "rod" {
			cap = 1
		}
		if n > uint64(cap) {
			return out, ErrInvalid
		}
		added := min(int(n), max(0, cap-p.Copies(id)))
		for range added {
			p.OwnedGear = append(p.OwnedGear, id)
		}
		out.Granted = strconv.Itoa(added)
		if added < int(n) {
			out.Skipped = strconv.Itoa(int(n) - added)
		}
	case strings.HasPrefix(item.Asset, "lake:bait:"):
		if n > rules.MaxBait {
			return out, ErrInvalid
		}
		added, converted, e := p.AwardBait(strings.TrimPrefix(item.Asset, "lake:bait:"), int(n))
		err = e
		out.Granted = strconv.Itoa(added)
		if converted > 0 {
			out.Converted = &rewardasset.Item{Asset: "lake:coins", Quantity: strconv.Itoa(converted)}
		} else if added < int(n) {
			out.Skipped = strconv.Itoa(int(n) - added)
		}
	case strings.HasPrefix(item.Asset, "lake:debris:"):
		id := strings.TrimPrefix(item.Asset, "lake:debris:")
		if _, ok := rewardCatalog().Debris[id]; !ok || n > rules.MaxDebris {
			return out, ErrInvalid
		}
		if p.DebrisStock[id] > rules.MaxDebris-int(n) {
			return out, ErrCapacity
		}
		p.DebrisStock[id] += int(n)
	default:
		return out, ErrInvalid
	}
	if err != nil {
		if errors.Is(err, rules.ErrOverflow) {
			return out, ErrCapacity
		}
		return out, err
	}
	return out, saveProfileTx(ctx, tx, user, &row, at)
}
