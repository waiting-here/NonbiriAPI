package claim

import (
	"context"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func usageRequest(t *testing.T, fixture *claimFixture, userID int64) CompleteRequestInput {
	t.Helper()
	request := fixture.acceptSelf(userID, 2)
	key := fixture.seedKey(userID, "usage")
	for index, usage := range []connectorcontract.Usage{
		{Present: true, UncachedInputTokens: 13, CacheWriteInputTokens: 2, CacheReadInputTokens: 3, OutputTokens: 147},
		{},
	} {
		handle, err := fixture.service.Claim(context.Background(), ClaimInput{
			RequestID: request.ID, ActorUserID: userID, AttemptSeq: index + 1,
			Purpose: PurposeSelf, Candidate: key.candidate,
		})
		if err != nil {
			t.Fatal(err)
		}
		dispatch, err := fixture.service.TakeForDispatch(context.Background(), handle)
		if err != nil {
			t.Fatal(err)
		}
		dispatch.Clear()
		if _, err := fixture.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{
			Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: usage,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return CompleteRequestInput{RequestID: request.ID, Caller: CallerResult{Class: ResultSuccess, Status: 200}, Disposition: AccountingCommit}
}

func assertUsage(t *testing.T, fixture *claimFixture, table string, id int64, want [6]string) {
	t.Helper()
	var raw [6][]byte
	if err := fixture.db.QueryRow("SELECT "+usageColumns+" FROM "+table+" WHERE id=?", id).
		Scan(&raw[0], &raw[1], &raw[2], &raw[3], &raw[4], &raw[5]); err != nil {
		t.Fatal(err)
	}
	for index := range raw {
		got, err := db.DecodeU128(raw[index])
		if err != nil || got.Decimal() != want[index] {
			t.Fatalf("%s counter %d = %s (%v), want %s", table, index, got.Decimal(), err, want[index])
		}
	}
}

func TestTerminalUsageIsAtomicAndIdempotent(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("usage", false)
	input := usageRequest(t, fixture, userID)
	zero := [6]string{"0", "0", "0", "0", "0", "0"}
	assertUsage(t, fixture, "users", userID, zero)
	if _, err := fixture.db.Exec(`CREATE TEMP TRIGGER reject_usage BEFORE UPDATE ON site_usage_totals BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.CompleteRequest(context.Background(), input); err == nil {
		t.Fatal("completion should roll back")
	}
	assertUsage(t, fixture, "users", userID, zero)
	assertUsage(t, fixture, "site_usage_totals", 1, zero)
	var state string
	if err := fixture.db.QueryRow(`SELECT state FROM logical_requests WHERE id=?`, input.RequestID).Scan(&state); err != nil || state == "terminal" {
		t.Fatalf("request must remain recoverable: %s %v", state, err)
	}
	if _, err := fixture.db.Exec(`DROP TRIGGER reject_usage`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := fixture.service.CompleteRequest(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	want := [6]string{"1", "13", "2", "3", "147", "1"}
	assertUsage(t, fixture, "users", userID, want)
	assertUsage(t, fixture, "site_usage_totals", 1, want)
}

func TestUsageInitializationRepairsOnlyUninitializedTotals(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("usage-repair", false)
	input := usageRequest(t, fixture, userID)
	if _, err := fixture.service.CompleteRequest(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	// Reproduce a current database whose logs were written while its counters
	// were still at their original zero values.
	for _, table := range []string{"users", "site_usage_totals"} {
		if _, err := fixture.db.Exec("UPDATE "+table+` SET total_requests=?,total_uncached_input_tokens=?,total_cache_write_input_tokens=?,total_cache_read_input_tokens=?,total_output_tokens=?,total_unknown_usage_requests=?,revision=?`,
			u128Small(0), u128Small(0), u128Small(0), u128Small(0), u128Small(0), u128Small(0), u128Small(0)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := fixture.service.InitializeUsageTotals(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	want := [6]string{"1", "13", "2", "3", "147", "1"}
	assertUsage(t, fixture, "users", userID, want)
	assertUsage(t, fixture, "site_usage_totals", 1, want)
	tx, err := fixture.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := fixture.service.PrepareAccountDeletion(context.Background(), tx, userID, fixture.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.InitializeUsageTotals(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertUsage(t, fixture, "site_usage_totals", 1, want)
}

func TestUsageOverflowRollsBackTerminalRequest(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("usage-overflow", false)
	input := usageRequest(t, fixture, userID)
	maximum, err := db.ParseU128Decimal("340282366920938463463374607431768211455")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Exec(`UPDATE site_usage_totals SET total_requests=? WHERE id=1`, db.EncodeU128(maximum)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.CompleteRequest(context.Background(), input); err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("expected checked overflow, got %v", err)
	}
	assertUsage(t, fixture, "users", userID, [6]string{"0", "0", "0", "0", "0", "0"})
}
