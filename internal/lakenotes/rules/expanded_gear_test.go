package rules

import "testing"

func TestBulkBaitPurchaseStopsAtStockAndAvailableCoins(t *testing.T) {
	quantity := 50
	for _, tc := range []struct {
		coins       Amount
		stock, want int
		remaining   Amount
	}{
		{"13", 0, 2, "3"}, {"1000", 997, 999, "990"},
	} {
		p := InitialProfile()
		p.Coins = tc.coins
		p.BaitStock["basic"] = tc.stock
		got, err := ApplyAction(p, Action{Name: "buy_bait", ID: "basic", Quantity: &quantity})
		if err != nil || got.Profile.BaitStock["basic"] != tc.want || got.Profile.Coins != tc.remaining {
			t.Fatal(got, err)
		}
	}
}
func TestThirdTackleSlotCannotDuplicateUnownedGear(t *testing.T) {
	p := InitialProfile()
	p.OwnedGear = append(p.OwnedGear, "legendRod", "corkBobber", "corkBobber", "spinner")
	p.Equipped = Loadout{Rod: "legendRod", Tackle1: "corkBobber", Tackle2: "corkBobber"}
	if _, err := ApplyAction(p, Action{Name: "equip_gear", Slot: "tackle3", ID: "corkBobber"}); err == nil {
		t.Fatal("third copy accepted")
	}
	got, err := ApplyAction(p, Action{Name: "equip_gear", Slot: "tackle3", ID: "spinner"})
	if err != nil || got.Profile.Equipped.Tackle3 != "spinner" {
		t.Fatal(got, err)
	}
	got, err = ApplyAction(got.Profile, Action{Name: "equip_gear", Slot: "rod", ID: "bambooPole"})
	if err != nil || got.Profile.Equipped.Tackle3 != "" {
		t.Fatal("slot retained on incompatible rod", err)
	}
}
