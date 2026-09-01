package activities

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestExportUserTxUsesCallerSnapshotAndWrapper(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_100_000)
	userID, _ := fixture.seedUser("activity-export-tx", false)
	fixture.fundUser(userID, -1)
	var welfarePool string
	if err := fixture.store.DB().QueryRow(`SELECT id FROM shared_pools WHERE pool_type='welfare'`).Scan(&welfarePool); err != nil {
		t.Fatal(err)
	}
	fixture.fundPool(welfarePool, 1000)
	fixture.setActivityConfig(true, true, false, 0, 100)
	if _, _, err := fixture.repository.ClaimWelfare(context.Background(), userID,
		fixture.control(http.MethodPost, routeWelfareClaims, nil)); err != nil {
		t.Fatal(err)
	}

	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE welfare_claims SET cap_milli=200 WHERE user_id=?`, userID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	inside, err := fixture.repository.ExportUserTx(context.Background(), tx, userID, 10)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if len(inside.WelfareClaims) != 1 || inside.WelfareClaims[0].Cap != "0.2" {
		_ = tx.Rollback()
		t.Fatalf("caller snapshot export = %+v", inside.WelfareClaims)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	outside, err := fixture.repository.ExportUser(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(outside.WelfareClaims) != 1 || outside.WelfareClaims[0].Cap != "0.1" {
		t.Fatalf("standalone wrapper observed rolled-back value: %+v", outside.WelfareClaims)
	}
}

func TestExportUserTxRejectsLimitPlusOne(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_200_000)
	userID, _ := fixture.seedUser("activity-export-limit", false)
	fixture.fundUser(userID, -1)
	var welfarePool string
	if err := fixture.store.DB().QueryRow(`SELECT id FROM shared_pools WHERE pool_type='welfare'`).Scan(&welfarePool); err != nil {
		t.Fatal(err)
	}
	fixture.fundPool(welfarePool, 1000)
	fixture.setActivityConfig(true, true, false, 0, 100)
	for day := 0; day < 2; day++ {
		fixture.clock.Store(1_800_200_000 + int64(day*86400))
		if day > 0 {
			fixture.fundUser(userID, -100)
		}
		if _, _, err := fixture.repository.ClaimWelfare(context.Background(), userID,
			fixture.control(http.MethodPost, routeWelfareClaims, map[string]any{"day": day})); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := fixture.repository.ExportUserTx(context.Background(), tx, userID, 1); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("limit+1 error = %v, want resource limit", err)
	}
}

func TestExportUserTxIncludesSettledThursdayFacts(t *testing.T) {
	opensAt := beijingThursday(2027, 5, 6)
	fixture := newActivityFixture(t, opensAt-1)
	userID, _ := fixture.seedUser("activity-export-settled", false)
	fixture.fundUser(userID, 1000)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"export": "settled"}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-05-06", OpensAt: opensAt,
			Entry: "0.1", PerUserLimit: 1,
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.setActivityConfig(true, false, true, 0, 0)
	fixture.clock.Store(opensAt)
	if _, _, err := fixture.repository.ContributeThursday(context.Background(), userID,
		fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"export": "settled"}),
		ThursdayContributionInput{PeriodID: created.Value.ID, ExpectedRevision: fixture.period(created.Value.ID).revision}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(opensAt + 86400)
	if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
		t.Fatal(err)
	}

	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exported, err := fixture.repository.ExportUserTx(context.Background(), tx, userID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Thursday) != 1 {
		t.Fatalf("Thursday export = %+v", exported.Thursday)
	}
	item := exported.Thursday[0]
	if !item.Eligible || !item.Settled || item.Count != "1" || item.Contributed != "0.1" ||
		item.Payout != "0.1" || item.UnpaidReason != nil {
		t.Fatalf("settled Thursday fact = %+v", item)
	}
}
