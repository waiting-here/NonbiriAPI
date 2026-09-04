package activities

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
)

func TestConcurrentWelfareClaimsHaveOneWinner(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_500_000)
	userID, _ := fixture.seedUser("concurrent-welfare", false)
	fixture.fundUser(userID, -1)
	var welfarePool string
	_ = fixture.store.DB().QueryRow(`SELECT id FROM shared_pools WHERE pool_type='welfare'`).Scan(&welfarePool)
	fixture.fundPool(welfarePool, 1000)
	fixture.setActivityConfig(true, true, false, 0, 100)
	mutations := []ControlMutation{
		fixture.control(http.MethodPost, routeWelfareClaims, nil),
		fixture.control(http.MethodPost, routeWelfareClaims, nil),
	}
	start := make(chan struct{})
	errorsSeen := make([]error, 2)
	var wait sync.WaitGroup
	for index := range mutations {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, _, errorsSeen[index] = fixture.repository.ClaimWelfare(context.Background(), userID, mutations[index])
		}(index)
	}
	close(start)
	wait.Wait()
	successes := 0
	for index, err := range errorsSeen {
		if err == nil {
			successes++
			continue
		}
		if errors.Is(err, ErrRetryable) {
			_, _, err = fixture.repository.ClaimWelfare(context.Background(), userID, mutations[index])
			errorsSeen[index] = err
		}
		if !errors.Is(errorsSeen[index], ErrConflict) {
			t.Fatalf("concurrent error[%d]=%v", index, errorsSeen[index])
		}
	}
	if successes != 1 {
		t.Fatalf("successes=%d errors=%v", successes, errorsSeen)
	}
	var claims, operations int
	_ = fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM welfare_claims WHERE user_id=?`, userID).Scan(&claims)
	_ = fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='welfare_claim'`).Scan(&operations)
	if claims != 1 || operations != 1 {
		t.Fatalf("claims=%d operations=%d", claims, operations)
	}
}

func TestConcurrentContributionCannotPierceLimitOrRevision(t *testing.T) {
	opensAt := beijingThursday(2027, 2, 11)
	fixture := newActivityFixture(t, opensAt-1)
	userID, _ := fixture.seedUser("concurrent-thursday", false)
	fixture.fundUser(userID, 10)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"concurrent": true}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-02-11", OpensAt: opensAt,
			Entry: "0.001", PerUserLimit: 1, PumpsBP: PumpsBP{},
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.setActivityConfig(true, false, true, 0, 0)
	fixture.clock.Store(opensAt)
	revision := fixture.period(created.Value.ID).revision
	mutations := []ControlMutation{
		fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"attempt": 1}),
		fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"attempt": 2}),
	}
	start := make(chan struct{})
	errorsSeen := make([]error, 2)
	var wait sync.WaitGroup
	for index := range mutations {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, _, errorsSeen[index] = fixture.repository.ContributeThursday(context.Background(), userID, mutations[index],
				ThursdayContributionInput{PeriodID: created.Value.ID, ExpectedRevision: revision})
		}(index)
	}
	close(start)
	wait.Wait()
	successes := 0
	for index, err := range errorsSeen {
		if err == nil {
			successes++
			continue
		}
		if errors.Is(err, ErrRetryable) {
			_, _, err = fixture.repository.ContributeThursday(context.Background(), userID, mutations[index],
				ThursdayContributionInput{PeriodID: created.Value.ID, ExpectedRevision: revision})
			errorsSeen[index] = err
		}
		if !errors.Is(errorsSeen[index], ErrConflict) {
			t.Fatalf("concurrent error[%d]=%v", index, errorsSeen[index])
		}
	}
	if successes != 1 {
		t.Fatalf("successes=%d errors=%v", successes, errorsSeen)
	}
	var countRaw []byte
	if err := fixture.store.DB().QueryRow(`SELECT contribution_count FROM thursday_participants WHERE period_id=?`, created.Value.ID).Scan(&countRaw); err != nil {
		t.Fatal(err)
	}
	count, _ := decodeU128(countRaw)
	if count.Decimal() != "1" {
		t.Fatalf("contribution count=%s", count.Decimal())
	}
	assertReservedRows(t, fixture.store.DB(), "2")
}
