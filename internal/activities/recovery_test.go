package activities

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"
)

func TestAcceptedPeriodFinalizesAtCapacityMaximum(t *testing.T) {
	opensAt := beijingThursday(2027, 1, 28)
	fixture := newActivityFixture(t, opensAt-1)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"capacity": true}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-01-28", OpensAt: opensAt,
			Entry: "0.001", PerUserLimit: 1, PumpsBP: PumpsBP{},
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1`, int64(math.MaxInt64-1)); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(opensAt + 86400)
	if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
		t.Fatalf("freeze at MAX: %v", err)
	}
	if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
		t.Fatalf("finalize at MAX: %v", err)
	}
	period := fixture.period(created.Value.ID)
	if period.state != PeriodStateSettled || period.ledgerRowsRemaining.Big().Sign() != 0 {
		t.Fatalf("period did not finalize at MAX: %+v", period)
	}
	assertReservedRows(t, fixture.store.DB(), "0")
}

func TestSettlementCheckpointRollbackReplaysExactlyOnce(t *testing.T) {
	opensAt := beijingThursday(2027, 2, 4)
	fixture := newActivityFixture(t, opensAt-1)
	user, _ := fixture.seedUser("checkpoint", false)
	fixture.fundUser(user, 10)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"checkpoint": true}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-02-04", OpensAt: opensAt,
			Entry: "0.001", PerUserLimit: 1, PumpsBP: PumpsBP{},
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.setActivityConfig(true, false, true, 0, 0)
	fixture.clock.Store(opensAt)
	period := fixture.period(created.Value.ID)
	if _, _, err := fixture.repository.ContributeThursday(context.Background(), user,
		fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"checkpoint": true}),
		ThursdayContributionInput{PeriodID: period.id, ExpectedRevision: period.revision}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(opensAt + 86400)
	if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
		t.Fatal(err)
	}

	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := readPeriodRecordTx(context.Background(), tx, created.Value.ID)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, _, _, err := fixture.repository.processThursdayBatchTx(context.Background(), tx, frozen, fixture.clock.Load()); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var payouts int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='thursday_payout'`).Scan(&payouts); err != nil || payouts != 0 {
		t.Fatalf("rolled-back payouts=%d err=%v", payouts, err)
	}
	if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='thursday_payout'`).Scan(&payouts); err != nil || payouts != 1 {
		t.Fatalf("replayed payouts=%d err=%v", payouts, err)
	}
	validateLedgerRecovery(t, fixture.store.DB())
}

func TestSettlementUsesFixedParticipantBatchesAndClearsCheckpoint(t *testing.T) {
	opensAt := beijingThursday(2027, 3, 25)
	fixture := newActivityFixture(t, opensAt-1)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"batch": true}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-03-25", OpensAt: opensAt,
			Entry: "0.001", PerUserLimit: 1, PumpsBP: PumpsBP{},
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.setActivityConfig(true, false, true, 0, 0)
	fixture.clock.Store(opensAt)
	for index := 0; index < SettlementBatchSize+1; index++ {
		userID, _ := fixture.seedUser(fmt.Sprintf("batch-%02d", index), false)
		fixture.fundUser(userID, 1)
		period := fixture.period(created.Value.ID)
		if _, _, err := fixture.repository.ContributeThursday(context.Background(), userID,
			fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"index": index}),
			ThursdayContributionInput{PeriodID: period.id, ExpectedRevision: period.revision}); err != nil {
			t.Fatalf("contribution %d: %v", index, err)
		}
	}
	assertReservedRows(t, fixture.store.DB(), fmt.Sprintf("%d", SettlementBatchSize+2))
	fixture.clock.Store(opensAt + 86400)
	if result, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil || !result.Changed || !result.More {
		t.Fatalf("freeze result=%+v err=%v", result, err)
	}
	first, _, err := fixture.repository.RunSettlementStep(context.Background())
	if err != nil || first.ProcessedRows != SettlementBatchSize || !first.More {
		t.Fatalf("first batch=%+v err=%v", first, err)
	}
	checkpoint := fixture.period(created.Value.ID)
	if checkpoint.state != PeriodStateSettling || !checkpoint.settlementCursor.Valid {
		t.Fatalf("checkpoint period=%+v", checkpoint)
	}
	assertReservedRows(t, fixture.store.DB(), "2")
	second, _, err := fixture.repository.RunSettlementStep(context.Background())
	if err != nil || second.ProcessedRows != 1 || second.More {
		t.Fatalf("second batch=%+v err=%v", second, err)
	}
	terminal := fixture.period(created.Value.ID)
	if terminal.state != PeriodStateSettled || terminal.settlementCursor.Valid ||
		terminal.frozenContributionCount.Decimal() != fmt.Sprintf("%d", SettlementBatchSize+1) ||
		terminal.payoutTotal.Decimal() != fmt.Sprintf("%d", SettlementBatchSize+1) || terminal.rollover.Decimal() != "0" {
		t.Fatalf("terminal period=%+v", terminal)
	}
	assertReservedRows(t, fixture.store.DB(), "0")
	validateLedgerRecovery(t, fixture.store.DB())
}

func TestSettlementWorkerCloseIsIdempotent(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_300_000)
	service, _ := NewService(ServiceConfig{Repository: fixture.repository})
	worker, err := NewSettlementWorker(service, 1)
	if err != nil {
		t.Fatal(err)
	}
	worker.Close()
	worker.Close()
	if err := worker.RecoverBeforeListener(context.Background()); err != ErrClosed {
		t.Fatalf("closed recovery error=%v", err)
	}
}

func TestSettlementWorkerRetryBackoffAndCloseAreBounded(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_300_100)
	service, _ := NewService(ServiceConfig{Repository: fixture.repository})
	worker, err := NewSettlementWorker(service, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if nextSettlementRetryDelay(0) != 30*time.Second ||
		nextSettlementRetryDelay(30*time.Second) != time.Minute ||
		nextSettlementRetryDelay(32*time.Minute) != time.Hour ||
		nextSettlementRetryDelay(time.Hour) != time.Hour {
		t.Fatal("settlement retry backoff does not clamp to [30s,1h]")
	}
	worker.Close()
	started := time.Now()
	if err := worker.wait(context.Background(), time.Hour); err != ErrClosed {
		t.Fatalf("closed retry wait error=%v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("closed retry wait took %s", elapsed)
	}
}

func TestSettlementResumeReplaysSameCheckpoint(t *testing.T) {
	opensAt := beijingThursday(2027, 4, 1)
	fixture := newActivityFixture(t, opensAt-1)
	userID, _ := fixture.seedUser("resume", false)
	fixture.fundUser(userID, 1)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"resume": true}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-04-01", OpensAt: opensAt,
			Entry: "0.001", PerUserLimit: 1, PumpsBP: PumpsBP{},
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.setActivityConfig(true, false, true, 0, 0)
	fixture.clock.Store(opensAt)
	if _, _, err := fixture.repository.ContributeThursday(context.Background(), userID,
		fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"resume": "entry"}),
		ThursdayContributionInput{PeriodID: created.Value.ID, ExpectedRevision: fixture.period(created.Value.ID).revision}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(opensAt + 86400)
	if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	frozen := fixture.period(created.Value.ID)
	mutation := fixture.control(http.MethodPost, routeAdminThursdayResume, map[string]any{"expected_revision": frozen.revision}, frozen.id)
	first, _, err := fixture.repository.ResumeThursday(context.Background(), fixture.adminID, frozen.id, mutation, frozen.revision)
	if err != nil || first.Value.State != PeriodStateSettled || first.Value.Entry != "0.001" ||
		first.Value.Settlement == nil || first.Value.Settlement.PayoutTotal != "0.001" {
		t.Fatalf("resume result=%+v err=%v", first, err)
	}
	replayed, facts, err := fixture.repository.ResumeThursday(context.Background(), fixture.adminID, frozen.id, mutation, frozen.revision)
	if err != nil || !replayed.Replayed || string(replayed.Body) != string(first.Body) || !facts.empty() {
		t.Fatalf("resume replay=%+v facts=%+v err=%v", replayed, facts, err)
	}
	assertReservedRows(t, fixture.store.DB(), "0")
	validateLedgerRecovery(t, fixture.store.DB())
}

func TestSQLiteBusyIsClassifiedRetryableWithoutWriting(t *testing.T) {
	fixture := newActivityFixture(t, 1_805_000_000)
	var sequence int
	var name, path string
	if err := fixture.store.DB().QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil || sequence != 0 || name != "main" || path == "" {
		t.Fatalf("database path sequence=%d name=%q path=%q err=%v", sequence, name, path, err)
	}
	locker, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	contender, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()
	if _, err := locker.Exec(`PRAGMA busy_timeout=1; PRAGMA foreign_keys=ON;`); err != nil {
		t.Fatal(err)
	}
	if _, err := contender.Exec(`PRAGMA busy_timeout=1; PRAGMA foreign_keys=ON;`); err != nil {
		t.Fatal(err)
	}
	lockTx, err := locker.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback()
	if _, err := lockTx.Exec(`UPDATE site_config SET updated_at=updated_at WHERE key='activities_enabled'`); err != nil {
		t.Fatal(err)
	}
	_, busyErr := contender.Exec(`UPDATE site_config SET updated_at=updated_at WHERE key='activities_enabled'`)
	if busyErr == nil || !errors.Is(classifyDatabaseError("busy probe", busyErr), ErrRetryable) {
		t.Fatalf("busy error=%v classified=%v", busyErr, classifyDatabaseError("busy probe", busyErr))
	}
}
