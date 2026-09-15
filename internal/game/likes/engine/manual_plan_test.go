package engine

import "testing"

func TestManualPlanRequiresCastAfterShoppingUnlessStillStunned(t *testing.T) {
	for _, mode := range []string{"quick", "standard"} {
		for seat := range 2 {
			e, s := fixture(t, mode)
			if _, err := e.ValidateManualPlan(s, seat, EmptyPlan()); err == nil {
				t.Fatal("normal player skipped casting")
			}
			if _, err := e.ValidateManualPlan(s, seat, plan("PUB01")); err != nil {
				t.Fatal(err)
			}
			var stuns []string
			for _, b := range e.Catalog().Buffs {
				if b.Kind == "STUN" {
					stuns = append(stuns, b.ID)
				}
			}
			if len(stuns) == 0 {
				t.Fatal("stun missing")
			}
			initialBuff(e, &s, seat, stuns[0], 1)
			if _, err := e.ValidateManualPlan(s, seat, EmptyPlan()); err != nil {
				t.Fatal("stunned skip rejected", err)
			}
			if _, err := e.ValidateManualPlan(s, seat, plan("PUB01")); err == nil {
				t.Fatal("stunned cast accepted")
			}
			cleanse := EmptyPlan()
			cleanse.Purchases = []Purchase{{Item: "cleanse", Target: stuns[0]}}
			if _, err := e.ValidateManualPlan(s, seat, cleanse); err == nil {
				t.Fatal("cleansed player skipped casting")
			}
			cleanse.Main = &Choice{SkillID: "PUB01"}
			if _, err := e.ValidateManualPlan(s, seat, cleanse); err != nil {
				t.Fatal("cleanse and cast rejected", err)
			}
			gold := s.Players[seat].Gold
			s.Players[seat].Gold = 0
			if _, err := e.ValidateManualPlan(s, seat, cleanse); err == nil {
				t.Fatal("unpaid cleansing accepted")
			}
			s.Players[seat].Gold = gold
			var other string
			for _, b := range e.Catalog().Buffs {
				if b.Kind == "SUPPRESS" {
					other = b.ID
					break
				}
			}
			initialBuff(e, &s, seat, other, 1)
			cleanse.Purchases[0].Target = other
			cleanse.Main = nil
			if _, err := e.ValidateManualPlan(s, seat, cleanse); err != nil {
				t.Fatal("remaining stun must allow skip", err)
			}
		}
	}
}
