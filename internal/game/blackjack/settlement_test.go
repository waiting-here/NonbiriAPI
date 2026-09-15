package blackjack

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
)

func TestAllStakeReturnsAndIndependentRounding(t *testing.T) {
	for tier := int64(1); tier <= 50; tier++ {
		for outcome, expected := range map[string]int64{"loss": 0, "push": 970_000, "win": 1_940_000, "natural": 2_425_000} {
			for units := 1; units <= 2; units++ {
				if outcome == "natural" && units == 2 {
					continue
				}
				r, err := SettleHand(tier*1_000_000, engine.Hand{Units: units, Stood: true, Outcome: outcome}, config.Rates{Platform: 100, Welfare: 100, Thursday: 100})
				if err != nil || r.Net != tier*expected*int64(units) || r.Net+r.Platform+r.Welfare+r.Thursday != r.Gross {
					t.Fatalf("tier %d %s: %+v %v", tier, outcome, r, err)
				}
			}
		}
	}
	r, err := SettleHand(101, engine.Hand{Units: 1, Stood: true, Outcome: "natural"}, config.Rates{Platform: 123, Welfare: 234, Thursday: 345})
	if err != nil || r.Gross != 252 || r.Platform != 3 || r.Welfare != 5 || r.Thursday != 8 || r.Net != 236 {
		t.Fatalf("individual rounding: %+v %v", r, err)
	}
}
