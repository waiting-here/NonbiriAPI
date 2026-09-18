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

func TestNineSeatMaximumStakeSummaryPreservesWideAmounts(t *testing.T) {
	// Two doubled winning hands per player reach the table's largest return.
	// The aggregate exceeds the per-operation money limit and must stay exact.
	detail := AdminDetail{Record: AnonymousFact{Phase: "result"}}
	for seat := range engine.MaxSeats {
		settled := SeatSettlement{Seat: seat}
		for range engine.MaxHands {
			hand, err := SettleHand(config.MaxStakeMilli, engine.Hand{Units: 2, Stood: true, Outcome: "win"}, config.Rates{})
			if err != nil {
				t.Fatal(err)
			}
			settled.Hands = append(settled.Hands, hand)
		}
		detail.Record.Fact.Seats = append(detail.Record.Fact.Seats, SeatTerms{Seat: seat})
		detail.Record.Fact.Settlements = append(detail.Record.Fact.Settlements, settled)
	}
	summary := summarizeAdmin(detail)
	if summary.Seats != 9 || summary.TotalStake != "5062500000000" || summary.Net != "10125000000000" {
		t.Fatalf("nine-seat maximum return was truncated: %+v", summary)
	}
}
