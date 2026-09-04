package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestPointsMilliCodecBoundaries(t *testing.T) {
	valid := []struct {
		wire      string
		milli     int64
		projected string
	}{
		{wire: "0", milli: 0, projected: "0"},
		{wire: "0.001", milli: 1, projected: "0.001"},
		{wire: "1", milli: 1000, projected: "1"},
		{wire: "1.23", milli: 1230, projected: "1.23"},
		{wire: "1.230", milli: 1230, projected: "1.23"},
		{wire: "9000000000000", milli: db.MaxMoneyMilli, projected: "9000000000000"},
	}
	for _, test := range valid {
		t.Run(test.wire, func(t *testing.T) {
			milli, ok := parsePointsMilli(test.wire)
			if !ok || milli != test.milli {
				t.Fatalf("parsePointsMilli(%q)=(%d,%t), want (%d,true)", test.wire, milli, ok, test.milli)
			}
			if projected := formatMilliPointsInt64(milli); projected != test.projected {
				t.Fatalf("formatMilliPointsInt64(%d)=%q, want %q", milli, projected, test.projected)
			}
		})
	}
	for _, wire := range []string{
		"", ".1", "1.", "0.0000", "01", "00.1", "+1", "-1", "1e3", " 1", "1 ",
		"9000000000000.001", "999999999999999999999999999999999",
	} {
		t.Run("reject_"+wire, func(t *testing.T) {
			if milli, ok := parsePointsMilli(wire); ok {
				t.Fatalf("parsePointsMilli(%q)=(%d,true), want rejection", wire, milli)
			}
		})
	}
	if got := formatMilliPoints(big.NewInt(-1230)); got != "-1.23" {
		t.Fatalf("signed aggregate projection=%q", got)
	}
}

func TestActivityPointsWireRoundTripsAcrossAllProjections(t *testing.T) {
	opensAt := beijingThursday(2027, 4, 15)
	fixture := newActivityFixture(t, opensAt-1)
	userID, _ := fixture.seedUser("points-wire", false)
	fixture.fundUser(userID, 200_000)

	threshold, cap := "1.23", "0.001"
	configured, _, err := fixture.repository.PatchActivitiesConfig(context.Background(), fixture.adminID,
		fixture.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"welfare": "points"}), ActivitiesConfigPatch{
			ExpectedRevision: fixture.configRevision(), Welfare: &WelfareConfigPatch{Threshold: &threshold, Cap: &cap},
		})
	if err != nil || configured.Value.Welfare.Threshold != threshold || configured.Value.Welfare.Cap != cap {
		t.Fatalf("configuration=%+v err=%v", configured.Value, err)
	}

	var welfarePoolID string
	var welfarePoolRevision int64
	if err := fixture.store.DB().QueryRow(`SELECT id,revision FROM shared_pools WHERE pool_type='welfare'`).Scan(&welfarePoolID, &welfarePoolRevision); err != nil {
		t.Fatal(err)
	}
	adjusted, _, err := fixture.repository.AdjustPool(context.Background(), fixture.adminID, welfarePoolID,
		fixture.control(http.MethodPost, routeAdminPoolAdjustment, map[string]any{"amount": "1.23"}, welfarePoolID),
		PoolAdjustment{Direction: DirectionIncrease, Amount: "1.23", Reason: "points projection", ExpectedRevision: welfarePoolRevision})
	if err != nil || adjusted.Value.Balance != "1.23" {
		t.Fatalf("adjusted pool=%+v err=%v", adjusted.Value, err)
	}

	created, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"entry": "100"}), ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-04-15", OpensAt: opensAt,
			Entry: "100", PerUserLimit: 1, PumpsBP: PumpsBP{},
		})
	if err != nil || created.Value.Entry != "100" || fixture.period(created.Value.ID).entryMilli != 100_000 {
		t.Fatalf("created period=%+v err=%v", created.Value, err)
	}
	fixture.setActivityConfig(true, true, true, 1230, 1)
	fixture.clock.Store(opensAt)
	contributed, _, err := fixture.repository.ContributeThursday(context.Background(), userID,
		fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"entry": "100"}),
		ThursdayContributionInput{PeriodID: created.Value.ID, ExpectedRevision: fixture.period(created.Value.ID).revision})
	if err != nil || contributed.Value.Balance != "100" || contributed.Value.PoolBalance != "100" {
		t.Fatalf("contribution=%+v err=%v", contributed.Value, err)
	}
	projection, err := fixture.repository.ProjectActivities(context.Background(), userID)
	if err != nil || projection.Snapshot.Welfare.Threshold != "1.23" || projection.Snapshot.Welfare.Cap != "0.001" ||
		projection.Snapshot.Welfare.PoolBalance != "1.23" || projection.Snapshot.Thursday.Current == nil ||
		projection.Snapshot.Thursday.Current.Entry != "100" || projection.Snapshot.Thursday.Current.PoolBalance != "100" ||
		projection.Snapshot.Thursday.Current.MyContributed != "100" {
		t.Fatalf("open projection=%+v err=%v", projection.Snapshot, err)
	}
	sink := &recordingActivitySink{}
	publisher, err := NewAccountstreamPublisher(fixture.repository, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(context.Background(), PublishFacts{AccountIDs: []int64{userID}}); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("point projection events=%d", len(sink.events))
	}
	var streamed ActivitiesSnapshot
	if err := json.Unmarshal(sink.events[0].event.Data, &streamed); err != nil || streamed.Welfare.PoolBalance != "1.23" ||
		streamed.Thursday.Current == nil || streamed.Thursday.Current.Entry != "100" ||
		streamed.Thursday.Current.MyContributed != "100" {
		t.Fatalf("streamed projection=%+v err=%v", streamed, err)
	}

	fixture.clock.Store(opensAt + 86400)
	if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.repository.RunSettlementStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	terminal := fixture.period(created.Value.ID)
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	period, err := projectPeriodTx(context.Background(), tx, terminal)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if period.Settlement == nil || period.Entry != "100" || period.Settlement.FrozenPool != "100" ||
		period.Settlement.PayoutTotal != "100" || period.Settlement.Rollover != "0" {
		t.Fatalf("terminal period=%+v", period)
	}
	projection, err = fixture.repository.ProjectActivities(context.Background(), userID)
	if err != nil || projection.Snapshot.Thursday.LastResult == nil ||
		projection.Snapshot.Thursday.LastResult.MyContributed != "100" || projection.Snapshot.Thursday.LastResult.Payout != "100" {
		t.Fatalf("terminal projection=%+v err=%v", projection.Snapshot.Thursday, err)
	}
	exported, err := fixture.repository.ExportUser(context.Background(), userID)
	if err != nil || len(exported.Thursday) != 1 || exported.Thursday[0].Contributed != "100" || exported.Thursday[0].Payout != "100" {
		t.Fatalf("activity export=%+v err=%v", exported, err)
	}
}

func TestInvalidPointsAmountsLeaveNoWrites(t *testing.T) {
	for _, wire := range []string{"1.0000", "01", "9000000000000.001"} {
		t.Run(wire, func(t *testing.T) {
			opensAt := beijingThursday(2027, 4, 22)
			fixture := newActivityFixture(t, opensAt-1)
			baseline := activityEconomyStorageSnapshot(t, fixture)

			if _, _, err := fixture.repository.PatchActivitiesConfig(context.Background(), fixture.adminID,
				fixture.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"threshold": wire}), ActivitiesConfigPatch{
					ExpectedRevision: fixture.configRevision(), Welfare: &WelfareConfigPatch{Threshold: &wire},
				}); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("configuration amount %q error=%v", wire, err)
			}
			assertActivityEconomyUnchanged(t, fixture, baseline)

			if _, _, err := fixture.repository.PutThursdayNext(context.Background(), fixture.adminID,
				fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"entry": wire}), ThursdayNextMutation{
					ExpectedRevision: fixture.configRevision(), PeriodKey: "2027-04-22", OpensAt: opensAt,
					Entry: wire, PerUserLimit: 1, PumpsBP: PumpsBP{},
				}); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Thursday entry %q error=%v", wire, err)
			}
			assertActivityEconomyUnchanged(t, fixture, baseline)

			var poolID string
			var revision int64
			if err := fixture.store.DB().QueryRow(`SELECT id,revision FROM shared_pools WHERE pool_type='welfare'`).Scan(&poolID, &revision); err != nil {
				t.Fatal(err)
			}
			if _, _, err := fixture.repository.AdjustPool(context.Background(), fixture.adminID, poolID,
				fixture.control(http.MethodPost, routeAdminPoolAdjustment, map[string]any{"amount": wire}, poolID),
				PoolAdjustment{Direction: DirectionIncrease, Amount: wire, Reason: "invalid amount", ExpectedRevision: revision}); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("pool amount %q error=%v", wire, err)
			}
			assertActivityEconomyUnchanged(t, fixture, baseline)
		})
	}
}

func TestAmountIdempotencyDigestBindsOriginalPointsString(t *testing.T) {
	fixture := newActivityFixture(t, 1_806_000_000)
	var poolID string
	var revision int64
	if err := fixture.store.DB().QueryRow(`SELECT id,revision FROM shared_pools WHERE pool_type='welfare'`).Scan(&poolID, &revision); err != nil {
		t.Fatal(err)
	}
	firstInput := PoolAdjustment{Direction: DirectionIncrease, Amount: "1", Reason: "canonical amount", ExpectedRevision: revision}
	mutation := fixture.control(http.MethodPost, routeAdminPoolAdjustment, map[string]any{"amount": firstInput.Amount}, poolID)
	if _, _, err := fixture.repository.AdjustPool(context.Background(), fixture.adminID, poolID, mutation, firstInput); err != nil {
		t.Fatal(err)
	}
	replayed, facts, err := fixture.repository.AdjustPool(context.Background(), fixture.adminID, poolID, mutation, firstInput)
	if err != nil || !replayed.Replayed || replayed.Value.Balance != "1" || !facts.empty() {
		t.Fatalf("points replay=%+v facts=%+v err=%v", replayed, facts, err)
	}
	before := activityEconomyStorageSnapshot(t, fixture)
	secondInput := firstInput
	secondInput.Amount = "1.0"
	secondBody, err := idempotency.CanonicalJSON(map[string]any{"amount": secondInput.Amount})
	if err != nil {
		t.Fatal(err)
	}
	mutation.CanonicalBody = secondBody
	if _, _, err := fixture.repository.AdjustPool(context.Background(), fixture.adminID, poolID, mutation, secondInput); !errors.Is(err, ErrConflict) {
		t.Fatalf("same key with distinct original amount error=%v", err)
	}
	assertActivityEconomyUnchanged(t, fixture, before)
}

func TestPoolProjectionFormatsAggregateBeyondInt64(t *testing.T) {
	fixture := newActivityFixture(t, 1_806_100_000)
	var poolID string
	if err := fixture.store.DB().QueryRow(`SELECT id FROM shared_pools WHERE pool_type='welfare'`).Scan(&poolID); err != nil {
		t.Fatal(err)
	}
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	pool, err := readPoolRecordTx(context.Background(), tx, poolID)
	if err != nil {
		t.Fatal(err)
	}
	external, err := ledger.CodedAccount(context.Background(), tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 1025; index++ {
		plan, err := ledger.NewAdminPoolAdjustment(ledger.Meta{
			OperationID: fixture.operationID(), ActorUserID: fixture.adminID, CreatedAt: fixture.clock.Load(),
		}, pool.accountID, external.ID, ledger.AmountFromMilli(db.MaxMoneyMilli), "wide aggregate fixture")
		if err != nil {
			t.Fatalf("plan %d: %v", index, err)
		}
		if _, err := ledger.Apply(context.Background(), tx, plan); err != nil {
			t.Fatalf("apply %d: %v", index, err)
		}
	}
	if _, err := tx.Exec(`UPDATE shared_pools SET revision=revision+1 WHERE id=?`, poolID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	wantMilli := new(big.Int).Mul(big.NewInt(db.MaxMoneyMilli), big.NewInt(1025))
	if wantMilli.IsInt64() || wantMilli.Cmp(big.NewInt(math.MaxInt64)) <= 0 {
		t.Fatalf("fixture aggregate=%s did not exceed int64", wantMilli)
	}
	page, err := fixture.repository.ListPools(context.Background(), PoolListQuery{PoolType: PoolTypeWelfare, Limit: 1})
	if err != nil || len(page.Data) != 1 || page.Data[0].Balance != formatMilliPoints(wantMilli) {
		t.Fatalf("wide pool page=%+v want=%s err=%v", page, formatMilliPoints(wantMilli), err)
	}
	validateLedgerRecovery(t, fixture.store.DB())
}

type activityEconomyWriteState struct {
	config                                                       activityConfigWriteState
	accounts, operations, entries, periods, participants, claims int
	capacity, pools                                              string
}

func activityEconomyStorageSnapshot(t *testing.T, fixture *activityFixture) activityEconomyWriteState {
	t.Helper()
	state := activityEconomyWriteState{config: activityConfigStorageSnapshot(t, fixture)}
	if err := fixture.store.DB().QueryRow(`SELECT
 (SELECT COUNT(*) FROM credit_accounts),
 (SELECT COUNT(*) FROM credit_operations),
 (SELECT COUNT(*) FROM credit_entries),
 (SELECT COUNT(*) FROM thursday_periods),
 (SELECT COUNT(*) FROM thursday_participants),
 (SELECT COUNT(*) FROM welfare_claims)`).Scan(
		&state.accounts, &state.operations, &state.entries, &state.periods, &state.participants, &state.claims); err != nil {
		t.Fatal(err)
	}
	var lastSequence int64
	var reserved, revision []byte
	if err := fixture.store.DB().QueryRow(`SELECT last_ledger_seq,reserved_future_rows,revision FROM credit_capacity WHERE id=1`).Scan(
		&lastSequence, &reserved, &revision); err != nil {
		t.Fatal(err)
	}
	state.capacity = fmt.Sprintf("%d:%x:%x", lastSequence, reserved, revision)
	if err := fixture.store.DB().QueryRow(`
SELECT COALESCE(group_concat(
 id || char(31) || pool_type || char(31) || period_id || char(31) || state || char(31) || revision ||
 char(31) || created_at || char(31) || closed_at || char(31) || balance_sign || char(31) || balance_mag ||
 char(31) || account_updated_at, char(30)), '')
FROM (
 SELECT p.id,p.pool_type,COALESCE(p.period_id,'' ) AS period_id,p.state,p.revision,p.created_at,
        COALESCE(p.closed_at,'') AS closed_at,a.balance_sign,hex(a.balance_mag) AS balance_mag,
        a.updated_at AS account_updated_at
 FROM shared_pools p JOIN credit_accounts a ON a.id=p.account_id ORDER BY p.id
)`).Scan(&state.pools); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertActivityEconomyUnchanged(t *testing.T, fixture *activityFixture, before activityEconomyWriteState) {
	t.Helper()
	after := activityEconomyStorageSnapshot(t, fixture)
	if after != before {
		t.Fatalf("rejected amount wrote state: before=%+v after=%+v", before, after)
	}
}
