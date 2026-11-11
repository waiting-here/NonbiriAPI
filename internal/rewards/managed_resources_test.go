package rewards

import (
	"errors"
	"math/big"
	"strings"
	"testing"
)

func TestManagedResourcesCatalogRoundTripAndBoundaries(t *testing.T) {
	f := setup(t)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	resources, err := ManagedResourcesTx(t.Context(), tx, f.user)
	if err != nil || len(resources) != len(Assets())-2 {
		t.Fatal(len(resources), err)
	}
	for _, resource := range resources {
		if resource.ID == "lake:gear:bambooPole" {
			continue
		}
		in := ResourceAdjustment{Asset: resource.ID, Direction: "increase", Quantity: "1", ExpectedQuantity: "0", Reason: "inventory correction"}
		up, err := AdjustResourceTx(t.Context(), tx, f.user, f.admin, f.at.Load(), in)
		if err != nil {
			t.Fatalf("%s increase: %v", resource.ID, err)
		}
		if len(up.Changes) == 0 {
			t.Fatal("missing exact changes", resource.ID)
		}
		in.Direction, in.ExpectedQuantity = "decrease", "1"
		if _, err := AdjustResourceTx(t.Context(), tx, f.user, f.admin, f.at.Load(), in); err != nil {
			t.Fatalf("%s decrease: %v", resource.ID, err)
		}
		in.ExpectedQuantity = "0"
		if _, err := AdjustResourceTx(t.Context(), tx, f.user, f.admin, f.at.Load(), in); !errors.Is(err, ErrCapacity) {
			t.Fatal("negative inventory admitted", resource.ID, err)
		}
	}
	if _, err := AdjustResourceTx(t.Context(), tx, f.user, f.admin, f.at.Load(), ResourceAdjustment{Asset: "lake:gear:bambooPole", Direction: "decrease", Quantity: "1", ExpectedQuantity: "1", Reason: "test"}); !errors.Is(err, ErrCapacity) {
		t.Fatal("starter rod removed", err)
	}
	for _, asset := range []string{"general", "game", "donation_credit", "missing"} {
		if ValidateResourceAdjustment(ResourceAdjustment{Asset: asset, Direction: "increase", Quantity: "1", ExpectedQuantity: "0"}) == nil {
			t.Fatal("unsupported resource", asset)
		}
	}
	for _, amount := range []string{"0", "-1", "01", "1.5", "1e3", strings.Repeat("9", 41)} {
		if ValidateResourceAdjustment(ResourceAdjustment{Asset: "lake:coins", Direction: "increase", Quantity: amount, ExpectedQuantity: "0"}) == nil {
			t.Fatal("invalid amount", amount)
		}
	}
	// Reward per-grant limits are not holding capacities: wide inventories must
	// remain editable beyond JavaScript's safe integer domain.
	max := "9007199254740991"
	in := ResourceAdjustment{Asset: "lake:coins", Direction: "increase", Quantity: max, ExpectedQuantity: "0", Reason: "wide inventory"}
	if _, err := AdjustResourceTx(t.Context(), tx, f.user, f.admin, f.at.Load(), in); err != nil {
		t.Fatal(err)
	}
	in.Quantity, in.ExpectedQuantity = "1", max
	out, err := AdjustResourceTx(t.Context(), tx, f.user, f.admin, f.at.Load(), in)
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).Add(big.NewInt(9007199254740991), big.NewInt(1)).String()
	if len(out.Changes) != 1 || out.Changes[0].After != want {
		t.Fatal(out.Changes)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
