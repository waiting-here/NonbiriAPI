package fatfish

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/fatfish/engine"
)

func syntheticCleanupSource() CleanupSource {
	return CleanupSource{InstanceIdentity: strings.Repeat("1", 64), SourceCommit: strings.Repeat("2", 40), SourceTree: strings.Repeat("3", 40), SourceSchemaHash: strings.Repeat("4", 64)}
}
func cleanupPlan(t *testing.T, f *fishFixture) CleanupManifest {
	t.Helper()
	manifest, err := PlanLegacyCleanup(context.Background(), f.db, syntheticCleanupSource())
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}
func runCleanup(t *testing.T, f *fishFixture, manifest CleanupManifest) (CleanupReceipt, error) {
	t.Helper()
	return RunOfflineLegacyCleanup(context.Background(), f.db, syntheticCleanupSource(), manifest, "synthetic-cleanup-operation", time.UnixMilli(f.clock.Load()))
}
func cleanupPaidGame(t *testing.T, f *fishFixture, period, node, state string) (ChallengeView, string) {
	t.Helper()
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(7000)); err != nil {
		t.Fatal(err)
	}
	cap, hash := fishCap(77)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(7001))
	if err != nil {
		t.Fatal(err)
	}
	if state == "prepared" {
		return prepared, cap
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: cap}, fishKey(7002))
	if err != nil {
		t.Fatal(err)
	}
	return started, cap
}
func assertCleanupEmpty(t *testing.T, f *fishFixture) {
	t.Helper()
	for _, table := range []string{"fatfish_levels", "fatfish_level_versions", "fatfish_periods", "fatfish_nodes", "fatfish_node_revisions", "fatfish_progress", "fatfish_period_progress", "fatfish_challenges", "fatfish_playtests"} {
		var count int
		if err := f.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
	var active, summary int
	if err := f.db.QueryRow("SELECT active_challenges,summary_rows FROM fatfish_capacity WHERE id=1").Scan(&active, &summary); err != nil || active != 0 || summary != 0 {
		t.Fatal(active, summary, err)
	}
}
func TestLegacyCleanupAtomicRefundAndReplay(t *testing.T) {
	for _, state := range []string{"prepared", "active", "settled", "playtest-prepared", "playtest-active"} {
		t.Run(state, func(t *testing.T) {
			f := newFishFixture(t)
			period, node := f.publishOne(t)
			ctx := context.Background()
			expectedBalance := "10000"
			paidID := ""
			refunds := 0
			if strings.HasPrefix(state, "playtest") {
				p, err := f.s.AdminPeriod(ctx, f.admin, period)
				if err != nil {
					t.Fatal(err)
				}
				cap, hash := fishCap(78)
				prepared, err := f.s.PreparePlaytest(ctx, f.admin, PlaytestInput{VersionID: p.Nodes[0].VersionID, TabCapabilityHash: hash}, fishKey(7020))
				if err != nil {
					t.Fatal(err)
				}
				if state == "playtest-active" {
					if _, err = f.s.StartPlaytest(ctx, f.admin, prepared.ID, StartInput{TabCapability: cap}, fishKey(7021)); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				started, cap := cleanupPaidGame(t, f, period, node, state)
				paidID = started.ID
				expectedBalance = "8500"
				if state == "active" {
					refunds = 1
				}
				if state == "settled" {
					f.clock.Store(*started.StartAtMS + (int64(f.terminalTick)*1000+59)/60 + 1)
					if _, err := f.s.Submit(ctx, f.user, started.ID, SubmitInput{TabCapability: cap, Inputs: []byte("[]"), TerminalTick: f.terminalTick}, fishKey(7022)); err != nil {
						t.Fatal(err)
					}
					f.waitState(t, started.ID, "settled_pass")
					expectedBalance = "15500"
				}
			}
			var claims, operations int
			if err := f.db.QueryRow("SELECT count(*) FROM fatfish_reward_claims").Scan(&claims); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRow("SELECT count(*) FROM credit_operations").Scan(&operations); err != nil {
				t.Fatal(err)
			}
			manifest := cleanupPlan(t, f)
			receipt, err := runCleanup(t, f, manifest)
			if err != nil || receipt.Replayed || receipt.RefundedChallenges != refunds {
				t.Fatal(receipt, err)
			}
			if refunds == 1 {
				var ticket, refund, amount string
				var credited int
				if err = f.db.QueryRow("SELECT operation_id FROM fatfish_financial_receipts WHERE receipt_key=? AND kind='ticket'", "ticket:"+paidID).Scan(&ticket); err != nil {
					t.Fatal(err)
				}
				if err = f.db.QueryRow("SELECT operation_id,hex(amount_mag) FROM fatfish_financial_receipts WHERE receipt_key=? AND kind='refund'", "refund:"+paidID).Scan(&refund, &amount); err != nil {
					t.Fatal(err)
				}
				if err = f.db.QueryRow("SELECT count(*) FROM credit_entries e JOIN credit_accounts a ON a.id=e.account_id WHERE e.operation_id=? AND a.user_id=? AND e.delta_sign=1 AND e.delta_mag=X'000000000000000000000000000007D0'", refund, f.user).Scan(&credited); err != nil {
					t.Fatal(err)
				}
				if ticket == refund || amount != "000000000000000000000000000007D0" || credited != 1 {
					t.Fatal("refund did not bind original ticket/user", ticket, refund, amount, credited)
				}
			}
			assertCleanupEmpty(t, f)
			if got := f.balance(t); got != expectedBalance {
				t.Fatal("wrong balance", got, expectedBalance)
			}
			var afterClaims, afterOperations int
			f.db.QueryRow("SELECT count(*) FROM fatfish_reward_claims").Scan(&afterClaims)
			f.db.QueryRow("SELECT count(*) FROM credit_operations").Scan(&afterOperations)
			if afterClaims != claims || afterOperations != operations+refunds {
				t.Fatal("historical finance changed")
			}
			// A retry cannot reconstruct a removed object with its old mutation key.
			if _, err = f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: "1", TabCapabilityHash: strings.Repeat("a", 64)}, fishKey(7001)); !errors.Is(err, ErrNotFound) {
				t.Fatal("old key restored a challenge", err)
			}
			if _, err = f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: "1"}, fishKey(7000)); err != nil && !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			// A generic response may replay until expiry, but cannot restore rows or debit money.
			assertCleanupEmpty(t, f)
			if f.balance(t) != expectedBalance {
				t.Fatal("old key changed money")
			}
			level, err := engine.ParseLevel(f.level)
			if err != nil {
				t.Fatal(err)
			}
			level.EngineVersion = 3
			raw, err := engine.NormalizedLevel(level)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := f.s.SaveLevel(ctx, f.admin, "", LevelInput{Title: "New generation", Draft: raw}, fishKey(7030))
			if err != nil {
				t.Fatal(err)
			}
			replay, err := runCleanup(t, f, manifest)
			if err != nil || !replay.Replayed {
				t.Fatal(replay, err)
			}
			if _, err = f.s.Level(ctx, f.admin, fresh.ID); err != nil {
				t.Fatal("retry deleted new content", err)
			}
			other, err := RunOfflineLegacyCleanup(ctx, f.db, syntheticCleanupSource(), manifest, "different-cleanup-operation", time.UnixMilli(f.clock.Load()))
			if !errors.Is(err, ErrConflict) {
				t.Fatal("another key ran cleanup", other, err)
			}
		})
	}
}

func TestLegacyCleanupRejectsChangedInput(t *testing.T) {
	f := newFishFixture(t)
	f.publishOne(t)
	ctx := context.Background()
	manifest := cleanupPlan(t, f)
	wrong := syntheticCleanupSource()
	wrong.SourceTree = strings.Repeat("5", 40)
	if _, err := RunOfflineLegacyCleanup(ctx, f.db, wrong, manifest, "synthetic-cleanup-operation", time.UnixMilli(f.clock.Load())); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	changed := manifest
	changed.EligibleIDsHash = strings.Repeat("0", 64)
	if _, err := runCleanup(t, f, changed); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	var id string
	if err := f.db.QueryRow("SELECT id FROM fatfish_levels LIMIT 1").Scan(&id); err != nil {
		t.Fatal(err)
	}
	level, err := f.s.Level(ctx, f.admin, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.SaveLevel(ctx, f.admin, id, LevelInput{Title: "Changed", Draft: level.Draft, ExpectedRevision: level.Revision}, fishKey(7040)); err != nil {
		t.Fatal(err)
	}
	if _, err = runCleanup(t, f, manifest); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	var levels, receipts int
	f.db.QueryRow("SELECT count(*) FROM fatfish_levels").Scan(&levels)
	f.db.QueryRow("SELECT count(*) FROM instance_cleanup_receipts").Scan(&receipts)
	if levels != 1 || receipts != 0 {
		t.Fatal("rejected input changed data", levels, receipts)
	}
}
func TestLegacyCleanupRollsBackRefundAndDeletion(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	challenge, _ := cleanupPaidGame(t, f, period, node, "active")
	manifest := cleanupPlan(t, f)
	if _, err := f.db.Exec("CREATE TRIGGER reject_cleanup BEFORE INSERT ON instance_cleanup_receipts BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCleanup(t, f, manifest); err == nil {
		t.Fatal("injected failure succeeded")
	}
	current, err := f.s.Challenge(context.Background(), f.user, challenge.ID, "")
	if err != nil || current.State != "active" || f.balance(t) != "6500" {
		t.Fatal(current, err, f.balance(t))
	}
	var levels, receipts int
	f.db.QueryRow("SELECT count(*) FROM fatfish_levels").Scan(&levels)
	f.db.QueryRow("SELECT count(*) FROM instance_cleanup_receipts").Scan(&receipts)
	if levels != 1 || receipts != 0 {
		t.Fatal("partial deletion", levels, receipts)
	}
	if _, err = f.db.Exec("DROP TRIGGER reject_cleanup"); err != nil {
		t.Fatal(err)
	}
	receipt, err := runCleanup(t, f, manifest)
	if err != nil || receipt.RefundedChallenges != 1 || f.balance(t) != "8500" {
		t.Fatal(receipt, err)
	}
}
func TestLegacyCleanupBlocksLateVerification(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	started, cap := cleanupPaidGame(t, f, period, node, "active")
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.s.replay = func(l engine.Level, seed [32]byte, inputs []engine.InputTuple, options engine.ReplayOptions) (engine.ReplayResult, error) {
		close(entered)
		select {
		case <-release:
		case <-options.Context.Done():
			return engine.ReplayResult{}, options.Context.Err()
		}
		return engine.Replay(l, seed, inputs, options)
	}
	f.clock.Store(*started.StartAtMS + (int64(f.terminalTick)*1000+59)/60 + 1)
	if _, err := f.s.Submit(context.Background(), f.user, started.ID, SubmitInput{TabCapability: cap, Inputs: []byte("[]"), TerminalTick: f.terminalTick}, fishKey(7050)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("verification not entered")
	}
	manifest := cleanupPlan(t, f)
	receipt, err := runCleanup(t, f, manifest)
	if err != nil || receipt.RefundedChallenges != 1 {
		t.Fatal(receipt, err)
	}
	unblock()
	done := make(chan struct{})
	go func() { f.s.workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("late worker did not stop")
	}
	assertCleanupEmpty(t, f)
	var claims int
	f.db.QueryRow("SELECT count(*) FROM fatfish_reward_claims").Scan(&claims)
	if claims != 0 || f.balance(t) != "8500" {
		t.Fatal("late verification settled removed content", claims, f.balance(t))
	}
}

func TestLegacyCleanupPreservesNewContentAndOtherDataAcrossReopen(t *testing.T) {
	f := newFishFixture(t)
	f.publishOne(t)
	ctx := context.Background()
	level, err := engine.ParseLevel(f.level)
	if err != nil {
		t.Fatal(err)
	}
	level.EngineVersion = 3
	raw, err := engine.NormalizedLevel(level)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := f.s.SaveLevel(ctx, f.admin, "", LevelInput{Title: "Retained v3", Draft: raw}, fishKey(7060))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec("INSERT INTO game_rank_events(seq,user_id,game_key,source_id,settled_at,positive_profit) VALUES(X'00000000000000000000000000000001',?,'fishing','retained-source',?,zeroblob(16))", f.user, fixtureNow); err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]string {
		result := map[string]string{}
		queries := map[string]string{
			"users":      "SELECT json_group_array(json_object('id',id,'username',username,'revision',hex(revision))) FROM (SELECT * FROM users ORDER BY id)",
			"config":     "SELECT json_group_array(json_object('key',activity_key,'visible',visible,'revision',hex(revision))) FROM (SELECT * FROM limited_activity_configs ORDER BY activity_key)",
			"other_game": "SELECT json_group_array(json_object('seq',hex(seq),'user',user_id,'game',game_key,'source',source_id,'settled',settled_at,'profit',hex(positive_profit))) FROM game_rank_events",
			"money":      "SELECT json_group_array(json_object('id',id,'kind',kind,'sign',balance_sign,'amount',hex(balance_mag))) FROM (SELECT * FROM credit_accounts ORDER BY id)",
		}
		for name, query := range queries {
			var value string
			if err := f.db.QueryRow(query).Scan(&value); err != nil {
				t.Fatal(name, err)
			}
			result[name] = value
		}
		return result
	}
	before := snapshot()
	manifest := cleanupPlan(t, f)
	for _, row := range manifest.Rows {
		if row.ID == fresh.ID {
			t.Fatal("new version entered legacy plan")
		}
	}
	receipt, err := runCleanup(t, f, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("retained data changed")
	}
	if _, err = f.s.Level(ctx, f.admin, fresh.ID); err != nil {
		t.Fatal(err)
	}
	var path string
	var seq int
	var name string
	if err = f.db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	f.s.Close()
	if err = f.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.SetMaxOpenConns(1)
	if _, err = reopened.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	replay, err := RunOfflineLegacyCleanup(ctx, reopened, syntheticCleanupSource(), manifest, "synthetic-cleanup-operation", time.UnixMilli(f.clock.Load()))
	if err != nil || !replay.Replayed || replay.CompletedAt != receipt.CompletedAt || !reflect.DeepEqual(replay.DeletedCounts, receipt.DeletedCounts) {
		t.Fatal(replay, err)
	}
	var stored string
	if err = reopened.QueryRow("SELECT draft_json FROM fatfish_levels WHERE id=?", fresh.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var kept engine.Level
	if err = json.Unmarshal([]byte(stored), &kept); err != nil || kept.EngineVersion != 3 {
		t.Fatal(kept, err)
	}
}

func TestLegacyCleanupRejectsBalancedTransferDuringDeletion(t *testing.T) {
	f := newFishFixture(t)
	f.publishOne(t)
	manifest := cleanupPlan(t, f)
	query := fmt.Sprintf("CREATE TRIGGER corrupt_cleanup AFTER DELETE ON fatfish_levels BEGIN UPDATE credit_accounts SET balance_sign=1,balance_mag=X'00000000000000000000000000002711' WHERE user_id=%d; UPDATE credit_accounts SET balance_sign=-1,balance_mag=X'00000000000000000000000000000001' WHERE user_id=%d; END", f.user, f.admin)
	if _, err := f.db.Exec(query); err != nil {
		t.Fatal(err)
	}
	if _, err := runCleanup(t, f, manifest); !errors.Is(err, ErrInvariant) {
		t.Fatal("balanced transfer escaped retained digest", err)
	}
	if f.balance(t) != "10000" {
		t.Fatal("failed cleanup changed balance")
	}
	var levels int
	f.db.QueryRow("SELECT count(*) FROM fatfish_levels").Scan(&levels)
	if levels != 1 {
		t.Fatal("failed digest check committed deletion")
	}
}
