package gacha

import (
	"errors"
	"testing"
)

func spRules() Rules {
	r := DefaultRules()
	r.SSRFeatured, r.SSRGuaranteeMisses = 1500, 2
	return r
}

func TestSPUsesSSRTierAndThirdHitGuarantee(t *testing.T) {
	r := spRules()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	baseRates, _ := ConsolidatedRates(DefaultRules())
	rates, _ := ConsolidatedRates(r)
	if rates != baseRates || r.Weights[3] != 100 || r.SSRPity != 60 {
		t.Fatal("SP changed initial tier odds or hard pity")
	}
	plain := DefaultRules()
	explicit := plain
	explicit.SSRGuaranteeMisses = 1
	if plain.Group() != explicit.Group() || plain.Group() == r.Group() {
		t.Fatal("rule group identity changed")
	}
	miss := func(n int) (int, error) { return n - 1, nil }
	p := freshPity(r)
	p.SRFeatured = true
	for hit := 1; hit <= 3; hit++ {
		p.SSR = 59
		item, next, err := drawOne(r, p, []string{"l-chatgpt", "h-chatgpt"}, miss, false)
		if err != nil {
			t.Fatal(err)
		}
		if hit < 3 {
			if cardsByID[item.CardID].Rarity != SSR || next.SSRMisses != hit || next.SSRFeatured != (hit == 2) {
				t.Fatal(hit, item, next)
			}
		} else if item.CardID != "l-chatgpt" || next.SSRMisses != 0 || next.SSRFeatured {
			t.Fatal(item, next)
		}
		if next.SSR != 0 || !next.SRFeatured {
			t.Fatal("tier progress crossed", next)
		}
		p = next
	}
	// Exact boundary is 15%; each SR event retains the SSR miss count.
	for _, roll := range []int{1499, 1500} {
		p = freshPity(r)
		p.SSR = 59
		p.SSRMisses = 1
		item, next, err := drawOne(r, p, []string{"l-chatgpt"}, func(n int) (int, error) {
			if n == 10000 {
				return roll, nil
			}
			return 0, nil
		}, false)
		if err != nil || (item.CardID == "l-chatgpt") != (roll == 1499) {
			t.Fatal(roll, item, err)
		}
		if roll == 1499 && (next.SSRMisses != 0 || next.SSRFeatured) {
			t.Fatal(next)
		}
	}
	p = freshPity(r)
	p.SSRMisses = 1
	p.SR = 9
	_, next, err := drawOne(r, p, []string{"h-chatgpt"}, miss, false)
	if err != nil || next.SSRMisses != 1 {
		t.Fatal("SR consumed SSR misses", next, err)
	}
	p.SSR = 59
	p.SSRMisses = 2
	p.SSRFeatured = true
	_, next, err = drawOne(r, p, []string{"h-chatgpt"}, miss, false)
	if err != nil || next.SSRMisses != 2 || !next.SSRFeatured {
		t.Fatal("absent SSR feature consumed guarantee", next, err)
	}
}

func TestSPProgressPersistsAcrossPoolsAndPeriods(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.exec(t, "UPDATE gacha_profiles SET limited=10 WHERE user_id=?", f.user)
	r := spRules()
	a := f.pool(t, "Stars", r, "l-chatgpt")
	b := f.pool(t, "Clouds", r, "m-deepseek")
	normal := f.pool(t, "Fireworks", DefaultRules(), "i-chatgpt")
	if a.Group != b.Group || a.Group == normal.Group {
		t.Fatal("SP group isolation")
	}
	f.s.random = func(n int) (int, error) { return n - 1, nil }
	for n, pool := range []Pool{a, b} {
		f.exec(t, "INSERT INTO gacha_pity(user_id,group_id,sr,ssr,sr_limit,ssr_limit,sr_featured,ssr_featured) VALUES(?,?,0,59,10,60,0,0) ON CONFLICT(user_id,group_id) DO UPDATE SET ssr=59", f.user, pool.Group)
		out, err := f.act(f.user, pool.Name, Action{Action: "draw", PoolID: pool.ID, Quantity: 1})
		if err != nil || out.Snapshot.Pity[a.Group].SSRMisses != n+1 {
			t.Fatal(out, err)
		}
	}
	f.at.Add(2 * 86400)
	rerun := f.pool(t, "Next stars", r, "l-chatgpt")
	f.exec(t, "UPDATE gacha_pity SET ssr=59 WHERE user_id=? AND group_id=?", f.user, a.Group)
	out, err := f.act(f.user, "next-period", Action{Action: "draw", PoolID: rerun.ID, Quantity: 1})
	if err != nil || out.Draw.Items[0].CardID != "l-chatgpt" || out.Snapshot.Pity[a.Group].SSRMisses != 0 {
		t.Fatal(out, err)
	}
	// An early repeat SP gives one SSR shard and cannot produce an SP currency.
	before := out.Snapshot.Fragments[SSR]
	f.exec(t, "UPDATE gacha_pity SET ssr=59,ssr_misses=2,ssr_featured=1 WHERE user_id=? AND group_id=?", f.user, a.Group)
	out, err = f.act(f.user, "duplicate-sp", Action{Action: "draw", PoolID: rerun.ID, Quantity: 1})
	if err != nil || !out.Draw.Items[0].Duplicate || out.Snapshot.Fragments[SSR] != before+1 || len(out.Snapshot.Fragments) != 4 {
		t.Fatal(out, err)
	}
}

func TestSPShopCostAndSingletonReward(t *testing.T) {
	f := newFixture(t)
	f.enter(t)
	f.pool(t, "Stars", spRules(), "l-chatgpt")
	f.exec(t, "UPDATE gacha_profiles SET ssr_shards=8 WHERE user_id=?", f.user)
	if _, err := f.act(f.user, "too-few", Action{Action: "redeem", Product: "l-chatgpt", Revision: 1}); !errors.Is(err, ErrFragments) {
		t.Fatal(err)
	}
	f.exec(t, "UPDATE gacha_profiles SET ssr_shards=9 WHERE user_id=?", f.user)
	out, err := f.act(f.user, "redeem-sp", Action{Action: "redeem", Product: "l-chatgpt", Revision: 1})
	if err != nil || out.Snapshot.Fragments[SSR] != 0 || out.Snapshot.Owned["l-chatgpt"].Source != "shop" {
		t.Fatal(out, err)
	}
	out, err = f.act(f.user, "claim-sp", Action{Action: "claim", Theme: "l"})
	if err != nil || out.Snapshot.Donation != "10" || out.Snapshot.Tickets["limited"] != 1 {
		t.Fatal(out, err)
	}
	replay, err := f.act(f.user, "claim-sp", Action{Action: "claim", Theme: "l"})
	if err != nil || replay.Snapshot.Donation != "10" || replay.Snapshot.Tickets["limited"] != 1 {
		t.Fatal(replay, err)
	}
	if _, err = f.act(f.user, "second-claim", Action{Action: "claim", Theme: "l"}); !errors.Is(err, ErrClaimed) {
		t.Fatal(err)
	}
}
