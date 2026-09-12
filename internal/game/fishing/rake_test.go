package fishing

import (
	"math/big"
	"testing"
)

func TestRakePerOutcomeFloorAndMaximum(t *testing.T) {
	for _, tc := range []struct {
		gross         int64
		bp            RakeBasisPoints
		net, p, w, th int64
	}{
		{0, RakeBasisPoints{100, 100, 100}, 0, 0, 0, 0},
		{99, RakeBasisPoints{100, 100, 100}, 99, 0, 0, 0},
		{100, RakeBasisPoints{100, 100, 100}, 97, 1, 1, 1},
		{101, RakeBasisPoints{100, 100, 100}, 98, 1, 1, 1},
		{10001, RakeBasisPoints{3333, 3333, 3333}, 2, 3333, 3333, 3333},
		{9000000000000000, RakeBasisPoints{9999, 0, 0}, 900000000000, 8999100000000000, 0, 0},
		{9000000000000000, RakeBasisPoints{}, 9000000000000000, 0, 0, 0},
	} {
		got := PayoutAfterRake(tc.gross, tc.bp)
		if got.NetMilli != tc.net || got.PlatformMilli != tc.p || got.WelfareMilli != tc.w || got.ThursdayMilli != tc.th {
			t.Fatalf("gross=%d bp=%+v: %+v", tc.gross, tc.bp, got)
		}
	}
	for _, bp := range []RakeBasisPoints{{-1, 0, 0}, {10000, 0, 0}, {9999, 1, 0}, {3334, 3333, 3333}} {
		cfg := DefaultConfig()
		cfg.RakeBP = bp
		if _, err := Compile(cfg); err == nil {
			t.Fatalf("accepted invalid schedule %+v", bp)
		}
	}
}

func TestNetRTPAccountsForEveryOutcomeAndFloor(t *testing.T) {
	for _, rtp := range [][2]int{{100, 100}, {90, 88}} {
		cfg := DefaultConfig()
		cfg.StandardRTPPercent, cfg.PremiumRTPPercent = rtp[0], rtp[1]
		rules, err := Compile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, bait := range baitOrder {
			compiled := rules.baits[bait]
			grossEV, netEV := new(big.Rat), new(big.Rat)
			add := func(gross int64, probability *big.Rat) {
				net := PayoutAfterRake(gross, cfg.RakeBP).NetMilli
				grossEV.Add(grossEV, new(big.Rat).Mul(new(big.Rat).SetInt64(gross), probability))
				netEV.Add(netEV, new(big.Rat).Mul(new(big.Rat).SetInt64(net), probability))
			}
			for _, outcome := range compiled.weighted {
				probability := new(big.Rat).SetFrac(new(big.Int).SetUint64(outcome.weight*4), new(big.Int).SetUint64(compiled.sampleSpace))
				if outcome.tier == TierTreasure {
					add(compiled.treasurePayout[outcome.key], probability)
					continue
				}
				species := rules.speciesByTier[outcome.tier]
				for _, fish := range species {
					perSize := new(big.Rat).Quo(probability, new(big.Rat).SetInt64(int64(len(species)*(fish.MaxCentimetre-fish.MinCentimetre+1))))
					for size := fish.MinCentimetre; size <= fish.MaxCentimetre; size++ {
						add(compiled.fishPayoutByTierCM[outcome.tier][size], perSize)
					}
				}
			}
			if new(big.Rat).Quo(grossEV, new(big.Rat).SetInt64(compiled.entry)).Cmp(compiled.roundedRTP) != 0 {
				t.Fatal("gross expectation changed")
			}
			idealNet := new(big.Rat).Mul(grossEV, new(big.Rat).SetFrac64(97, 100))
			floorBenefit := new(big.Rat).Sub(netEV, idealNet)
			if floorBenefit.Sign() < 0 || floorBenefit.Cmp(new(big.Rat).SetInt64(3)) >= 0 {
				t.Fatalf("%s floor bound=%s", bait, floorBenefit.RatString())
			}
		}
	}
}
