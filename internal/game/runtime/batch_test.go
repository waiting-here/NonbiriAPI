package runtime

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

func TestStartFishingAtomicOneTenAndAuthoritativeReplay(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	cases := []struct {
		bait  string
		count int
	}{
		{string(fishing.BaitWorm), 1}, {string(fishing.BaitWorm), 10},
		{string(fishing.BaitLure), 1}, {string(fishing.BaitLure), 10},
		{string(fishing.BaitPremium), 1}, {string(fishing.BaitPremium), 10},
	}
	for index, test := range cases {
		userID := fixture.seedUser("atomic-"+test.bait+string(rune('a'+index)), fixtureFunding)
		key := validTestKey(index + 1)
		beforeCalls := fixture.random.callCount()
		result, pending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: test.bait, Count: test.count, IdempotencyKey: key})
		if err != nil || pending != nil || result == nil {
			t.Fatalf("start %s/%d = (%#v,%#v,%v)", test.bait, test.count, result, pending, err)
		}
		if result.Count != test.count || len(result.Outcomes) != test.count || result.PayoutTotal != "0" || result.IdempotentReplay {
			t.Fatalf("result %s/%d = %#v", test.bait, test.count, result)
		}
		for ordinal, outcome := range result.Outcomes {
			if outcome.Ordinal != ordinal || outcome.Tier != string(fishing.TierJunk) || outcome.SpeciesKey != "boot" || outcome.SizeCM != 0 || outcome.Reward != "0" {
				t.Fatalf("outcome %d = %#v", ordinal, outcome)
			}
		}
		if calls := fixture.random.callCount() - beforeCalls; calls != test.count*2 {
			t.Fatalf("random calls %s/%d = %d", test.bait, test.count, calls)
		}
		if got := fixture.scalar(`SELECT COUNT(*) FROM game_fishing_outcomes WHERE batch_id=?`, result.BatchID); got != int64(test.count) {
			t.Fatalf("persisted outcomes = %d", got)
		}
		if got := fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE source_type='fishing_batch' AND source_id=?`, result.BatchID); got != 2 {
			t.Fatalf("fishing operations = %d", got)
		}
		if got := fixture.scalar(`SELECT COUNT(*) FROM game_fishing_rank_facts WHERE batch_id_text=?`, result.BatchID); got != 1 {
			t.Fatalf("rank facts = %d", got)
		}

		if _, err = fixture.database.Exec(`UPDATE maintenance_state SET enabled=1,revision=revision+1,changed_at=? WHERE id=1`, fixture.clock.Load()); err != nil {
			t.Fatal(err)
		}
		replayCalls := fixture.random.callCount()
		replay, replayPending, replayErr := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: test.bait, Count: test.count, IdempotencyKey: key})
		if replayErr != nil || replayPending != nil || replay == nil || replay.BatchID != result.BatchID || !replay.IdempotentReplay {
			t.Fatalf("replay = (%#v,%#v,%v)", replay, replayPending, replayErr)
		}
		if fixture.random.callCount() != replayCalls {
			t.Fatal("replay consumed randomness")
		}
		_, _, conflict := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: test.bait, Count: map[int]int{1: 10, 10: 1}[test.count], IdempotencyKey: key})
		if !errors.Is(conflict, ErrConflict) {
			t.Fatalf("same key different body error = %v", conflict)
		}
		if _, err = fixture.database.Exec(`UPDATE maintenance_state SET enabled=0,revision=revision+1,changed_at=? WHERE id=1`, fixture.clock.Load()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStartFishingChecksBalanceAndCapacityBeforeRandom(t *testing.T) {
	t.Run("balance", func(t *testing.T) {
		fixture := newGameFixture(t, &scriptedSource{})
		userID := fixture.seedUser("empty", 0)
		_, _, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: string(fishing.BaitWorm), Count: 1, IdempotencyKey: validTestKey(20)})
		if !errors.Is(err, ErrInsufficientCredits) || fixture.random.callCount() != 0 {
			t.Fatalf("start error=%v random=%d", err, fixture.random.callCount())
		}
		if fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches`) != 0 {
			t.Fatal("insufficient balance persisted a batch")
		}
	})
	t.Run("capacity", func(t *testing.T) {
		fixture := newGameFixture(t, &scriptedSource{})
		userID := fixture.seedUser("capacity", fixtureFunding)
		zero := db.EncodeU128(db.U128{})
		if _, err := fixture.database.Exec(`UPDATE credit_capacity SET last_ledger_seq=?,reserved_future_rows=? WHERE id=1`, int64(math.MaxInt64), zero); err != nil {
			t.Fatal(err)
		}
		_, _, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: string(fishing.BaitWorm), Count: 1, IdempotencyKey: validTestKey(21)})
		if !errors.Is(err, ErrServiceUnavailable) || fixture.random.callCount() != 0 {
			t.Fatalf("start error=%v random=%d", err, fixture.random.callCount())
		}
	})
}

func TestStartFishingRequiresLiveRuntimeCapability(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	userID := fixture.seedUser("unavailable", fixtureFunding)
	fixture.service.capability = game.RuntimeCapabilityFunc(func(string, string, string) bool { return false })
	snapshot, err := fixture.service.GamesSnapshot(context.Background(), userID, fixture.service.now())
	if err != nil || !snapshot.Fishing.Enabled || snapshot.Fishing.Available {
		t.Fatalf("snapshot = (%#v,%v)", snapshot.Fishing, err)
	}
	operations := fixture.scalar(`SELECT COUNT(*) FROM credit_operations`)
	_, _, err = fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(25)})
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("unavailable runtime error = %v", err)
	}
	if fixture.random.callCount() != 0 || fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches`) != 0 || fixture.scalar(`SELECT COUNT(*) FROM credit_operations`) != operations {
		t.Fatal("unavailable runtime produced side effects")
	}
}

func TestStartFishingRandomAndInsertFailureRollBackEverything(t *testing.T) {
	for _, test := range []struct {
		name    string
		source  *scriptedSource
		trigger bool
	}{
		{name: "random", source: &scriptedSource{failAt: 1}},
		{name: "outcome insert", source: &scriptedSource{}, trigger: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newGameFixture(t, test.source)
			userID := fixture.seedUser("rollback", fixtureFunding)
			if test.trigger {
				if _, err := fixture.database.Exec(`CREATE TRIGGER reject_fishing_outcome BEFORE INSERT ON game_fishing_outcomes BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
					t.Fatal(err)
				}
			}
			operations := fixture.scalar(`SELECT COUNT(*) FROM credit_operations`)
			capacity := fixture.scalar(`SELECT last_ledger_seq FROM credit_capacity WHERE id=1`)
			_, _, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: string(fishing.BaitWorm), Count: 10, IdempotencyKey: validTestKey(30)})
			if err == nil {
				t.Fatal("injected failure was accepted")
			}
			if fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches`) != 0 || fixture.scalar(`SELECT COUNT(*) FROM game_fishing_outcomes`) != 0 {
				t.Fatal("failed batch left domain rows")
			}
			if fixture.scalar(`SELECT COUNT(*) FROM credit_operations`) != operations || fixture.scalar(`SELECT last_ledger_seq FROM credit_capacity WHERE id=1`) != capacity {
				t.Fatal("failed batch changed ledger capacity or history")
			}
			if fixture.scalar(`SELECT COUNT(*) FROM user_activity_daily WHERE user_id=?`, userID) != 0 {
				t.Fatal("failed batch changed activity")
			}
		})
	}
}

func TestFishingHighPayoutAndCrossRouteIdempotencyConflicts(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{max: true})
	userID := fixture.seedUser("high", fixtureFunding)
	startKey := validTestKey(40)
	result, pending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: string(fishing.BaitPremium), Count: 1, IdempotencyKey: startKey})
	if err != nil || pending != nil || result == nil || result.Outcomes[0].SpeciesKey != "shell" || result.PayoutTotal != "37500" {
		t.Fatalf("high payout start = (%#v,%#v,%v)", result, pending, err)
	}
	_, _, err = fixture.service.RecoverFishing(context.Background(), RecoverInput{UserID: userID, BatchID: result.BatchID, IdempotencyKey: startKey})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("start then recover same key error = %v", err)
	}

	pendingUser := fixture.seedUser("pending", fixtureFunding)
	fixture.service.beforeSettlement = func(string) error { return errInjected }
	recoverKey := validTestKey(41)
	_, firstPending, err := fixture.service.StartFishing(context.Background(), StartInput{UserID: pendingUser, Bait: string(fishing.BaitWorm), Count: 1, IdempotencyKey: validTestKey(42)})
	if err != nil || firstPending == nil {
		t.Fatalf("create pending = (%#v,%v)", firstPending, err)
	}
	if _, err = fixture.database.Exec(`UPDATE game_fishing_batches SET attempt_count=10,next_attempt_at=NULL,last_error_class='settlement_failed',retry_exhausted=1 WHERE id=? AND state='reserved'`, firstPending.BatchID); err != nil {
		t.Fatal(err)
	}
	_, recoveredPending, err := fixture.service.RecoverFishing(context.Background(), RecoverInput{UserID: pendingUser, BatchID: firstPending.BatchID, IdempotencyKey: recoverKey})
	if err != nil || recoveredPending == nil || !recoveredPending.RetryExhausted {
		t.Fatalf("failed recover = (%#v,%v)", recoveredPending, err)
	}
	calls := fixture.random.callCount()
	_, _, err = fixture.service.StartFishing(context.Background(), StartInput{UserID: pendingUser, Bait: string(fishing.BaitLure), Count: 1, IdempotencyKey: recoverKey})
	if !errors.Is(err, ErrConflict) || fixture.random.callCount() != calls {
		t.Fatalf("recover then start same key error=%v calls=%d->%d", err, calls, fixture.random.callCount())
	}

	fixture.userAuth.setError(authz.ErrForbidden)
	_, _, err = fixture.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: string(fishing.BaitPremium), Count: 1, IdempotencyKey: startKey})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("replay without live authority error = %v", err)
	}
}
