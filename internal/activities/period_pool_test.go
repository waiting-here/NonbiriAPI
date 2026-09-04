package activities

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"testing"
)

type advancingAdminAuth struct{ advance func() }

func (authorizer advancingAdminAuth) AuthorizeAdmin(context.Context, *sql.Tx, int64) error {
	authorizer.advance()
	return nil
}

func TestThursdayPeriodCASCapacityAndReplayAfterOpen(t *testing.T) {
	opensAt := beijingThursday(2027, 2, 25)
	fixture := newActivityFixture(t, opensAt-60)
	input := ThursdayNextMutation{
		ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-02-25", OpensAt: opensAt,
		Literature: "first", Entry: "0.05", PerUserLimit: 2,
		PumpsBP: PumpsBP{Platform: 100, Welfare: 100, NextPool: 100},
	}
	mutation := fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"period": "replay"})
	first, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID, mutation, input)
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(opensAt)
	replayed, facts, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID, mutation, input)
	if err != nil || !replayed.Replayed || replayed.Value.ID != first.Value.ID || !facts.empty() {
		t.Fatalf("replay=%+v facts=%+v err=%v", replayed, facts, err)
	}
	stale := input
	stale.Literature = "stale"
	if _, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"period": "stale"}), stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("natural-open reconfiguration error=%v", err)
	}
}

func TestThursdayDestinationUsesExclusiveCloseAndRejectsConflictingDuplicate(t *testing.T) {
	opensAt := beijingThursday(2027, 3, 18)
	fixture := newActivityFixture(t, opensAt-1)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"destination": true}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-03-18", OpensAt: opensAt,
			Entry: "0.001", PerUserLimit: 1, PumpsBP: PumpsBP{},
		})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := fixture.store.DB().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	current, err := fixture.repository.ThursdayDestination(context.Background(), tx, opensAt+86400-1)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	next, err := fixture.repository.ThursdayDestination(context.Background(), tx, opensAt+86400)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if current.PoolID != created.Value.CurrentPoolID || next.PoolID != created.Value.NextPoolID || current == next {
		t.Fatalf("current=%+v next=%+v period=%+v", current, next, created.Value)
	}

	tx, err = fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	conflicting := current
	conflicting.AccountID++
	if _, err := fixture.repository.RecordPoolTransfers(context.Background(), tx, opensAt, current, conflicting); !errors.Is(err, ErrConflict) {
		tx.Rollback()
		t.Fatalf("conflicting duplicate destination error=%v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestPoolAdjustmentSamplesDeadlineAfterFinalAuthorization(t *testing.T) {
	opensAt := beijingThursday(2027, 4, 8)
	fixture := newActivityFixture(t, opensAt-1)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"deadline": true}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-04-08", OpensAt: opensAt,
			Entry: "0.001", PerUserLimit: 1, PumpsBP: PumpsBP{},
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(opensAt + 86400 - 1)
	fixture.repository.adminFinalAuth = advancingAdminAuth{advance: func() { fixture.clock.Store(opensAt + 86400) }}
	var revision int64
	if err := fixture.store.DB().QueryRow(`SELECT revision FROM shared_pools WHERE id=?`, created.Value.NextPoolID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	adjusted, _, err := fixture.repository.AdjustPool(context.Background(), fixture.adminID, created.Value.NextPoolID,
		fixture.control(http.MethodPost, routeAdminPoolAdjustment, map[string]any{"deadline": "next"}, created.Value.NextPoolID),
		PoolAdjustment{Direction: DirectionIncrease, Amount: "0.001", Reason: "deadline boundary", ExpectedRevision: revision})
	if err != nil || adjusted.Value.Balance != "0.001" {
		t.Fatalf("deadline adjustment=%+v err=%v", adjusted.Value, err)
	}
	assertPoolBalance(t, fixture.store.DB(), created.Value.CurrentPoolID, "0")
}

func TestThursdayPeriodCapacityFailureLeavesNoDomainRows(t *testing.T) {
	opensAt := beijingThursday(2027, 3, 4)
	fixture := newActivityFixture(t, opensAt-1)
	if _, err := fixture.store.DB().Exec(`UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1`, int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	_, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"capacity": "reject"}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-03-04", OpensAt: opensAt,
			Entry: "0.001", PerUserLimit: 1, PumpsBP: PumpsBP{},
		})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("capacity error=%v", err)
	}
	var periods, pools int
	_ = fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM thursday_periods`).Scan(&periods)
	_ = fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM shared_pools WHERE pool_type='thursday'`).Scan(&pools)
	if periods != 0 || pools != 1 {
		t.Fatalf("capacity failure periods=%d pools=%d", periods, pools)
	}
}

func TestFirstContributionFailureRollsBackFutureReservation(t *testing.T) {
	opensAt := beijingThursday(2027, 3, 11)
	fixture := newActivityFixture(t, opensAt-1)
	userID, _ := fixture.seedUser("insufficient", false)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"insufficient": true}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-03-11", OpensAt: opensAt,
			Entry: "0.005", PerUserLimit: 1, PumpsBP: PumpsBP{},
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.setActivityConfig(true, false, true, 0, 0)
	fixture.clock.Store(opensAt)
	period := fixture.period(created.Value.ID)
	mutation := fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"insufficient": true})
	if _, _, err := fixture.repository.ContributeThursday(context.Background(), userID, mutation,
		ThursdayContributionInput{PeriodID: period.id, ExpectedRevision: period.revision}); !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("insufficient error=%v", err)
	}
	var participants int
	_ = fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM thursday_participants WHERE period_id=?`, period.id).Scan(&participants)
	if participants != 0 {
		t.Fatalf("failed first contribution left %d participants", participants)
	}
	assertReservedRows(t, fixture.store.DB(), "1")
	fixture.fundUser(userID, 5)
	if _, _, err := fixture.repository.ContributeThursday(context.Background(), userID, mutation,
		ThursdayContributionInput{PeriodID: period.id, ExpectedRevision: period.revision}); err != nil {
		t.Fatalf("retry after funding: %v", err)
	}
	assertReservedRows(t, fixture.store.DB(), "2")
}

func TestFirstContributionCapacityRequiresCurrentAndFutureRowsAtomically(t *testing.T) {
	opensAt := beijingThursday(2027, 3, 25)
	fixture := newActivityFixture(t, opensAt-1)
	userID, _ := fixture.seedUser("capacity-current-future", false)
	fixture.fundUser(userID, 10)
	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"capacity": "current-and-future"}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-03-25", OpensAt: opensAt,
			Entry: "0.005", PerUserLimit: 1, PumpsBP: PumpsBP{},
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.setActivityConfig(true, false, true, 0, 0)
	fixture.clock.Store(opensAt)

	// The configured period already owns one finalize reservation. Leaving one
	// additional capacity row lets the participant reservation succeed inside
	// the transaction, but cannot also admit the current contribution row.
	if _, err := fixture.store.DB().Exec(`UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1`, int64(math.MaxInt64-2)); err != nil {
		t.Fatal(err)
	}
	var beforeLast int64
	var beforeReserved, beforeCapacityRevision []byte
	if err := fixture.store.DB().QueryRow(`SELECT last_ledger_seq,reserved_future_rows,revision FROM credit_capacity WHERE id=1`).Scan(
		&beforeLast, &beforeReserved, &beforeCapacityRevision); err != nil {
		t.Fatal(err)
	}
	beforeOperations := countActivityRows(t, fixture.store.DB(), `SELECT COUNT(*) FROM credit_operations`)
	beforeEntries := countActivityRows(t, fixture.store.DB(), `SELECT COUNT(*) FROM credit_entries`)
	beforeIdempotency := countActivityRows(t, fixture.store.DB(), `SELECT COUNT(*) FROM idempotency_records`)
	var beforePeriodRevision, beforePoolRevision int64
	if err := fixture.store.DB().QueryRow(`SELECT revision FROM thursday_periods WHERE id=?`, created.Value.ID).Scan(&beforePeriodRevision); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT revision FROM shared_pools WHERE id=?`, created.Value.CurrentPoolID).Scan(&beforePoolRevision); err != nil {
		t.Fatal(err)
	}

	period := fixture.period(created.Value.ID)
	_, _, err = fixture.repository.ContributeThursday(context.Background(), userID,
		fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"capacity": "current-and-future"}),
		ThursdayContributionInput{PeriodID: period.id, ExpectedRevision: period.revision})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("capacity error=%v", err)
	}

	var afterLast int64
	var afterReserved, afterCapacityRevision []byte
	if err := fixture.store.DB().QueryRow(`SELECT last_ledger_seq,reserved_future_rows,revision FROM credit_capacity WHERE id=1`).Scan(
		&afterLast, &afterReserved, &afterCapacityRevision); err != nil {
		t.Fatal(err)
	}
	if afterLast != beforeLast || !bytes.Equal(afterReserved, beforeReserved) || !bytes.Equal(afterCapacityRevision, beforeCapacityRevision) {
		t.Fatalf("capacity changed before=(%d,%x,%x) after=(%d,%x,%x)",
			beforeLast, beforeReserved, beforeCapacityRevision, afterLast, afterReserved, afterCapacityRevision)
	}
	assertReservedRows(t, fixture.store.DB(), "1")
	if participants := countActivityRows(t, fixture.store.DB(), `SELECT COUNT(*) FROM thursday_participants WHERE period_id=?`, created.Value.ID); participants != 0 {
		t.Fatalf("capacity failure left %d participants", participants)
	}
	if operations := countActivityRows(t, fixture.store.DB(), `SELECT COUNT(*) FROM credit_operations`); operations != beforeOperations {
		t.Fatalf("capacity failure changed operations: before=%d after=%d", beforeOperations, operations)
	}
	if entries := countActivityRows(t, fixture.store.DB(), `SELECT COUNT(*) FROM credit_entries`); entries != beforeEntries {
		t.Fatalf("capacity failure changed entries: before=%d after=%d", beforeEntries, entries)
	}
	if idempotencyRows := countActivityRows(t, fixture.store.DB(), `SELECT COUNT(*) FROM idempotency_records`); idempotencyRows != beforeIdempotency {
		t.Fatalf("capacity failure changed idempotency rows: before=%d after=%d", beforeIdempotency, idempotencyRows)
	}
	var state string
	var afterPeriodRevision, afterPoolRevision int64
	if err := fixture.store.DB().QueryRow(`SELECT state,revision FROM thursday_periods WHERE id=?`, created.Value.ID).Scan(&state, &afterPeriodRevision); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT revision FROM shared_pools WHERE id=?`, created.Value.CurrentPoolID).Scan(&afterPoolRevision); err != nil {
		t.Fatal(err)
	}
	if state != PeriodStateConfigured || afterPeriodRevision != beforePeriodRevision || afterPoolRevision != beforePoolRevision {
		t.Fatalf("domain state changed: state=%s period_revision=%d/%d pool_revision=%d/%d",
			state, beforePeriodRevision, afterPeriodRevision, beforePoolRevision, afterPoolRevision)
	}
	assertUserBalance(t, fixture.store.DB(), userID, "10")
	assertPoolBalance(t, fixture.store.DB(), created.Value.CurrentPoolID, "0")
}

func TestTypedPoolAdjustmentCASAndDangerousConfirmation(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_700_000)
	var welfarePool string
	var revision int64
	if err := fixture.store.DB().QueryRow(`SELECT id,revision FROM shared_pools WHERE pool_type='welfare'`).Scan(&welfarePool, &revision); err != nil {
		t.Fatal(err)
	}
	increase := PoolAdjustment{Direction: DirectionIncrease, Amount: "0.1", Reason: "seed welfare", ExpectedRevision: revision}
	first, _, err := fixture.repository.AdjustPool(context.Background(), fixture.adminID, welfarePool,
		fixture.control(http.MethodPost, routeAdminPoolAdjustment, map[string]any{"increase": true}, welfarePool), increase)
	if err != nil || first.Value.Balance != "0.1" {
		t.Fatalf("increase=%+v err=%v", first.Value, err)
	}
	badDecrease := PoolAdjustment{Direction: DirectionDecrease, Amount: "0.001", Reason: "bad confirmation", ExpectedRevision: revision + 1}
	if _, _, err := fixture.repository.AdjustPool(context.Background(), fixture.adminID, welfarePool,
		fixture.control(http.MethodPost, routeAdminPoolAdjustment, map[string]any{"bad": true}, welfarePool), badDecrease); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing confirmation error=%v", err)
	}
	decrease := badDecrease
	decrease.Amount = "0.03"
	decrease.Reason = "correct decrease"
	decrease.Confirmation = PoolDecreaseConfirmation
	last, _, err := fixture.repository.AdjustPool(context.Background(), fixture.adminID, welfarePool,
		fixture.control(http.MethodPost, routeAdminPoolAdjustment, map[string]any{"decrease": true}, welfarePool), decrease)
	if err != nil || last.Value.Balance != "0.07" || last.Value.Revision != "3" {
		t.Fatalf("decrease=%+v err=%v", last.Value, err)
	}
	if _, _, err := fixture.repository.AdjustPool(context.Background(), fixture.adminID, welfarePool,
		fixture.control(http.MethodPost, routeAdminPoolAdjustment, map[string]any{"stale": true}, welfarePool), decrease); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision error=%v", err)
	}
}
