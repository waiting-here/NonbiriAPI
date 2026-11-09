package rules

import (
	"math"
	"testing"
)

func TestBarbedFollowOnlyCommonAndPreservesHitSlowdown(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range catalog.Fish {
		if seen[f.Rarity] {
			continue
		}
		seen[f.Rarity] = true
		for _, count := range []int{1, 2} {
			for _, legacy := range []bool{false, true} {
				p := InitialProfile()
				c := Cast{RulesID: RulesID, Phase: "playing", WasHit: true, BarY: .5, Progress: .5,
					Snapshot: Snapshot{LegacyBarbedFollow: legacy, BarHeight: .2, Effects: Effects{BarbedCount: count, Acceleration: 1, ProgressGain: 1, ProgressLoss: 1, BottomBounce: 2.0 / 3}},
					Plan:     EncounterPlan{FishKind: f.Kind, Challenge: MakeChallenge(f, f.Length[0])},
					Fish:     &FishState{Position: .55 * 568, Y: .55, Target: 200, Drift: .1}, Motion: MotionRandom{State: 123},
				}
				if err := step(&p, &c, false); err != nil {
					t.Fatal(err)
				}
				// The independent on-hit reduction remains active for every rarity.
				want := float64(float64(float64(float64(.25*60)*60)/568)*.3) * TickSeconds
				if f.Rarity == "common" || legacy {
					want = float64(float64(float64(float64(float64(count)*.2)*60)/568) + want)
				}
				if math.Float64bits(c.BarVelocity) != math.Float64bits(want) {
					t.Fatalf("rarity=%s count=%d legacy=%v got %.18g want %.18g", f.Rarity, count, legacy, c.BarVelocity, want)
				}
			}
		}
	}
	for _, rarity := range []string{"common", "uncommon", "rare", "epic", "legendary"} {
		if !seen[rarity] {
			t.Fatalf("missing rarity %s", rarity)
		}
	}
	p, c, err := Start(InitialProfile(), &sequenceRandom{Values: []float64{.5, .5, .5, .5, .5, .5}}, 123)
	if err != nil || c.Snapshot.LegacyBarbedFollow || ValidateProfile(p) != nil {
		t.Fatal("new cast uses legacy behavior", err)
	}
}
