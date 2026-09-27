package activities

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
)

func prepareContinuityWelfare(t *testing.T, fixture *activityFixture) {
	t.Helper()
	var pool string
	if err := fixture.store.DB().QueryRow(`SELECT id FROM shared_pools WHERE pool_type='welfare'`).Scan(&pool); err != nil {
		t.Fatal(err)
	}
	fixture.fundPool(pool, 10000)
	fixture.setActivityConfig(true, true, false, 1000, 100)
}

// Account turnover is represented by freeing the provider ID on the old test
// account. The new account is then bound from that exact ID by the real service.
func reregisterActivityDiscord(t *testing.T, fixture *activityFixture, oldID int64, label string) int64 {
	t.Helper()
	if _, err := fixture.store.DB().Exec(`UPDATE users SET discord_id=? WHERE id=?`, "retired-"+label, oldID); err != nil {
		t.Fatal(err)
	}
	newID, _ := fixture.seedUser(label, false)
	return newID
}

func TestWelfareContinuityAcrossAccountTurnoverAndOriginalExpiry(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_000_000)
	first, _ := fixture.seedUser("welfare-continuity", false)
	prepareContinuityWelfare(t, fixture)
	claim := func(user int64) error {
		_, _, err := fixture.repository.ClaimWelfare(context.Background(), user,
			fixture.control(http.MethodPost, routeWelfareClaims, nil))
		return err
	}
	if err := claim(first); err != nil {
		t.Fatal(err)
	}
	next := reregisterActivityDiscord(t, fixture, first, "welfare-continuity")
	view, err := fixture.repository.GetActivities(context.Background(), next)
	if err != nil || !view.Welfare.ClaimedToday || view.Welfare.State != WelfareStateClaimed {
		t.Fatalf("new account welfare state=%+v err=%v", view.Welfare, err)
	}
	if err := claim(next); !errors.Is(err, ErrConflict) {
		t.Fatalf("same Discord claimed twice: %v", err)
	}
	other, _ := fixture.seedUser("welfare-other-discord", false)
	if err := claim(other); err != nil {
		t.Fatalf("different Discord denied: %v", err)
	}
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, expiresAt, err := continuity.DailyPeriodTx(context.Background(), tx, fixture.clock.Load())
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(expiresAt - 1)
	if err := claim(next); !errors.Is(err, ErrConflict) {
		t.Fatalf("claim before original expiry: %v", err)
	}
	fixture.clock.Store(expiresAt)
	if err := claim(next); err != nil {
		t.Fatalf("new site day denied: %v", err)
	}
	var claims, awards int
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM welfare_claims`).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM credit_operations WHERE kind='welfare_claim'`).Scan(&awards); err != nil {
		t.Fatal(err)
	}
	if claims != 3 || awards != 3 {
		t.Fatalf("claims=%d awards=%d", claims, awards)
	}
}

func TestWelfareContinuityZeroAndFailedAwardDoNotConsume(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_000_100)
	first, _ := fixture.seedUser("welfare-zero-continuity", false)
	prepareContinuityWelfare(t, fixture)
	fixture.setActivityConfig(true, true, false, 1000, 0)
	claim := func(user int64) error {
		_, _, err := fixture.repository.ClaimWelfare(context.Background(), user,
			fixture.control(http.MethodPost, routeWelfareClaims, nil))
		return err
	}
	if err := claim(first); err != nil {
		t.Fatal(err)
	}
	next := reregisterActivityDiscord(t, fixture, first, "welfare-zero-continuity")
	fixture.setActivityConfig(true, true, false, 1000, 100)
	if _, err := fixture.store.DB().Exec(`CREATE TRIGGER fail_welfare_receipt BEFORE INSERT ON welfare_claims BEGIN SELECT RAISE(ABORT,'receipt failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := claim(next); err == nil {
		t.Fatal("injected receipt failure succeeded")
	}
	var facts, awards int
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM identity_continuity_facts WHERE kind='welfare'`).Scan(&facts); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM credit_operations WHERE kind='welfare_claim'`).Scan(&awards); err != nil {
		t.Fatal(err)
	}
	if facts != 0 || awards != 0 {
		t.Fatalf("failed award consumed eligibility: facts=%d awards=%d", facts, awards)
	}
	if _, err := fixture.store.DB().Exec(`DROP TRIGGER fail_welfare_receipt`); err != nil {
		t.Fatal(err)
	}
	if err := claim(next); err != nil {
		t.Fatalf("claim after failed receipt: %v", err)
	}
}

func TestWelfareContinuityConcurrentAccountsOneAward(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_500_000)
	first, _ := fixture.seedUser("welfare-race", false)
	next := reregisterActivityDiscord(t, fixture, first, "welfare-race")
	prepareContinuityWelfare(t, fixture)
	users := [2]int64{first, next}
	mutations := [2]ControlMutation{
		fixture.control(http.MethodPost, routeWelfareClaims, nil),
		fixture.control(http.MethodPost, routeWelfareClaims, nil),
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var result [2]error
	for i := range users {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, result[i] = fixture.repository.ClaimWelfare(context.Background(), users[i], mutations[i])
		}(i)
	}
	close(start)
	wg.Wait()
	successes := 0
	for i, err := range result {
		if errors.Is(err, ErrRetryable) {
			_, _, err = fixture.repository.ClaimWelfare(context.Background(), users[i], mutations[i])
		}
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatalf("claim[%d]: %v", i, err)
		}
	}
	var awards int
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM credit_operations WHERE kind='welfare_claim'`).Scan(&awards); err != nil {
		t.Fatal(err)
	}
	if successes != 1 || awards != 1 {
		t.Fatalf("successes=%d awards=%d", successes, awards)
	}
}

func TestWelfareContinuityMissingBindingFailsClosed(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_000_000)
	user, _ := fixture.seedUser("unbound-welfare", false)
	prepareContinuityWelfare(t, fixture)
	if _, err := fixture.store.DB().Exec(`DELETE FROM user_continuity_identities WHERE user_id=?`, user); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.repository.ClaimWelfare(context.Background(), user,
		fixture.control(http.MethodPost, routeWelfareClaims, nil)); !errors.Is(err, continuity.ErrNotFound) {
		t.Fatalf("unbound claim: %v", err)
	}
	if _, err := fixture.repository.GetActivities(context.Background(), user); !errors.Is(err, continuity.ErrNotFound) {
		t.Fatalf("unbound welfare projection: %v", err)
	}
	var awards int
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM credit_operations WHERE kind='welfare_claim'`).Scan(&awards); err != nil || awards != 0 {
		t.Fatalf("unbound user award=%d err=%v", awards, err)
	}
}
