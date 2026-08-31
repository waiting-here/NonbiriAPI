package activities

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestWelfareZeroValuesThresholdBoundaryAndDailySlot(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_000_000)
	user, _ := fixture.seedUser("welfare-negative", false)
	equalUser, _ := fixture.seedUser("welfare-equal", false)
	fixture.fundUser(user, -1)
	var welfarePool string
	if err := fixture.store.DB().QueryRow(`SELECT id FROM shared_pools WHERE pool_type='welfare'`).Scan(&welfarePool); err != nil {
		t.Fatal(err)
	}
	fixture.fundPool(welfarePool, 1000)
	fixture.setActivityConfig(true, true, false, 0, 0)

	zero, facts, err := fixture.repository.ClaimWelfare(context.Background(), user,
		fixture.control(http.MethodPost, routeWelfareClaims, nil))
	if err != nil {
		t.Fatalf("zero-cap claim: %v", err)
	}
	if zero.Value.Awarded != "0" || !facts.empty() {
		t.Fatalf("zero-cap result = %+v, facts=%+v", zero.Value, facts)
	}
	var claims int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM welfare_claims WHERE user_id=?`, user).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("zero award consumed claim: count=%d err=%v", claims, err)
	}
	if _, _, err := fixture.repository.ClaimWelfare(context.Background(), equalUser,
		fixture.control(http.MethodPost, routeWelfareClaims, nil)); !errors.Is(err, ErrConflict) {
		t.Fatalf("assets == threshold error = %v", err)
	}

	fixture.setActivityConfig(true, true, false, 0, 100)
	paid, facts, err := fixture.repository.ClaimWelfare(context.Background(), user,
		fixture.control(http.MethodPost, routeWelfareClaims, nil))
	if err != nil {
		t.Fatalf("paid claim: %v", err)
	}
	if paid.Value.Awarded != "0.1" || paid.Value.Balance != "0.099" || paid.Value.PoolBalance != "0.9" || !facts.Global {
		t.Fatalf("paid claim = %+v facts=%+v", paid.Value, facts)
	}
	exported, err := fixture.repository.ExportUser(context.Background(), user)
	if err != nil || len(exported.WelfareClaims) != 1 || exported.WelfareClaims[0].Threshold != "0" ||
		exported.WelfareClaims[0].Cap != "0.1" || exported.WelfareClaims[0].Awarded != "0.1" {
		t.Fatalf("welfare export=%+v err=%v", exported.WelfareClaims, err)
	}
	if _, _, err := fixture.repository.ClaimWelfare(context.Background(), user,
		fixture.control(http.MethodPost, routeWelfareClaims, nil)); !errors.Is(err, ErrConflict) {
		t.Fatalf("second successful daily claim error = %v", err)
	}
}

func TestWelfareControlMutationExactReplay(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_000_100)
	user, _ := fixture.seedUser("welfare-replay", false)
	fixture.fundUser(user, -1)
	var welfarePool string
	_ = fixture.store.DB().QueryRow(`SELECT id FROM shared_pools WHERE pool_type='welfare'`).Scan(&welfarePool)
	fixture.fundPool(welfarePool, 1000)
	fixture.setActivityConfig(true, true, false, 0, 100)
	mutation := fixture.control(http.MethodPost, routeWelfareClaims, nil)
	first, _, err := fixture.repository.ClaimWelfare(context.Background(), user, mutation)
	if err != nil {
		t.Fatal(err)
	}
	second, facts, err := fixture.repository.ClaimWelfare(context.Background(), user, mutation)
	if err != nil {
		t.Fatal(err)
	}
	if first.Value.Awarded != "0.1" || second.Value.Awarded != "0.1" || !second.Replayed ||
		string(first.Body) != string(second.Body) || !facts.empty() {
		t.Fatalf("replay mismatch first=%+v second=%+v facts=%+v", first, second, facts)
	}
}
