package rules

import (
	"reflect"
	"testing"
)

func TestManagedResourcesRepairDependentSelectionsWithoutResettingHistory(t *testing.T) {
	p := InitialProfile()
	p.XP = "15000"
	p.First, p.Second, p.Third, p.Fourth = "steady", "control", "reader", "master"
	p.OwnedGear = append(p.OwnedGear, "legendRod", "corkBobber", "corkBobber", "spinner")
	p.Equipped = Loadout{Rod: "legendRod", Tackle1: "corkBobber", Tackle2: "corkBobber", Tackle3: "spinner"}
	loadout := p.Equipped
	p.SavedLoadouts[0] = &loadout
	p.BaitStock["basic"], p.SelectedBait = 1, "basic"
	p.EnsureContractBoard()
	before := CloneProfile(p)
	q, err := SetManagedResource(p, "lake:gear:corkBobber", "1")
	if err != nil || q.Equipped.Tackle2 != "" || q.SavedLoadouts[0].Tackle2 != "" {
		t.Fatal(q, err)
	}
	q, err = SetManagedResource(q, "lake:gear:legendRod", "0")
	if err != nil || q.Equipped.Rod != "bambooPole" || q.Equipped.Tackle3 != "" || q.SavedLoadouts[0].Rod != "bambooPole" {
		t.Fatal(q, err)
	}
	q, err = SetManagedResource(q, "lake:xp", "0")
	if err != nil || q.First != "" || q.Second != "" || q.Third != "" || q.Fourth != "" {
		t.Fatal(q, err)
	}
	q, err = SetManagedResource(q, "lake:bait:basic", "0")
	if err != nil || q.SelectedBait != "" {
		t.Fatal(q, err)
	}
	if !reflect.DeepEqual(p, before) || !reflect.DeepEqual(q.Contracts, before.Contracts) {
		t.Fatal("history/input changed")
	}
	if _, err := SetManagedResource(q, "lake:gear:bambooPole", "0"); err == nil {
		t.Fatal("starter rod removed")
	}
	if _, err := SetManagedResource(q, "lake:bait:basic", "1000"); err == nil {
		t.Fatal("bait capacity bypassed")
	}
}
