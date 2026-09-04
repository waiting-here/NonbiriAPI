package runtime

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestFishingLifecycleRecoveryIsBoundedAndRestartSafe(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	fixture.service.beforeSettlement = func(string) error { return errInjected }

	users := make([]int64, 0, 3)
	batches := make([]string, 0, 3)
	for index := range 3 {
		userID := fixture.seedUser("lifecycle-recovery-"+string(rune('a'+index)), fixtureFunding)
		_, pending, err := fixture.service.StartFishing(context.Background(), StartInput{
			UserID: userID, Bait: string(fishing.BaitWorm), Count: 1,
			IdempotencyKey: validTestKey(800 + index),
		})
		if err != nil || pending == nil || pending.NextAttemptAt == nil {
			t.Fatalf("pending %d = (%#v,%v)", index, pending, err)
		}
		users = append(users, userID)
		batches = append(batches, pending.BatchID)
	}

	decisionNow := fixtureNow + int64(firstRetryDelay/time.Second)
	beforeCapacity := fishingCapacityHeadroom(t, fixture.database)
	randomCalls := fixture.random.callCount()
	fixture.service.beforeSettlement = nil
	deadline := time.Now().Add(time.Minute)

	first, err := fixture.service.RecoverBeforeListenAt(context.Background(), decisionNow, 2, deadline)
	if err != nil || first != (RecoveryResult{Processed: 2, More: true}) {
		t.Fatalf("first recovery = (%+v,%v)", first, err)
	}
	if fixture.service.recovered.Load() {
		t.Fatal("partial startup recovery opened the worker gate")
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches WHERE state='reserved' AND next_attempt_at<=?`, decisionNow); got != 1 {
		t.Fatalf("due batches after first recovery = %d", got)
	}

	second, err := fixture.service.RecoverBeforeListenAt(context.Background(), decisionNow, 2, deadline)
	if err != nil || second != (RecoveryResult{Processed: 1, More: false}) {
		t.Fatalf("second recovery = (%+v,%v)", second, err)
	}
	if !fixture.service.recovered.Load() {
		t.Fatal("drained startup recovery did not open the worker gate")
	}
	if fixture.random.callCount() != randomCalls {
		t.Fatal("recovery generated new Fishing outcomes")
	}
	if after := fishingCapacityHeadroom(t, fixture.database); after.Cmp(beforeCapacity) != 0 {
		t.Fatalf("capacity headroom changed: before=%s after=%s", beforeCapacity, after)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='fishing_settle'`); got != 3 {
		t.Fatalf("settlement operations = %d", got)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM game_fishing_rank_facts WHERE aggregate_applied=1`); got != 3 {
		t.Fatalf("rank facts = %d", got)
	}

	for index, userID := range users {
		state, stateErr := fixture.service.FishingState(context.Background(), userID)
		if stateErr != nil || state.SettlementPending != nil || state.Unrevealed == nil ||
			state.Unrevealed.BatchID != batches[index] {
			t.Fatalf("committed authority %d = (%#v,%v)", index, state, stateErr)
		}
	}
	again, err := fixture.service.RecoverBeforeListenAt(context.Background(), decisionNow, 100, deadline)
	if err != nil || again != (RecoveryResult{}) {
		t.Fatalf("idempotent drained recovery = (%+v,%v)", again, err)
	}

	if err := fixture.service.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(Options{
		Store: fixture.store, UserAuthorizer: fixture.userAuth, AdminAuthorizer: fixture.adminAuth,
		Random: fixture.random, Now: func() time.Time { return time.Unix(fixture.clock.Load(), 0).UTC() },
		LeaderboardTieKey: []byte("0123456789abcdef0123456789abcdef"), WorkerInterval: time.Minute,
	})
	if err != nil {
		t.Fatalf("restart service: %v", err)
	}
	defer restarted.Close()
	restartResult, err := restarted.RecoverBeforeListenAt(context.Background(), decisionNow, 100, deadline)
	if err != nil || restartResult != (RecoveryResult{}) || !restarted.recovered.Load() {
		t.Fatalf("restart recovery = (%+v,%v) recovered=%v", restartResult, err, restarted.recovered.Load())
	}
	state, err := restarted.FishingState(context.Background(), users[0])
	if err != nil || state.Unrevealed == nil || state.Unrevealed.BatchID != batches[0] {
		t.Fatalf("restart unrevealed authority = (%#v,%v)", state, err)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches WHERE state='committed' AND revealed_at IS NULL`); got != 3 {
		t.Fatalf("restart changed reveal state: %d", got)
	}
}

func TestFishingLifecycleRecoverySchedulesFailuresAndExhaustion(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	fixture.service.beforeSettlement = func(string) error { return errInjected }
	firstUser := fixture.seedUser("lifecycle-retry", fixtureFunding)
	secondUser := fixture.seedUser("lifecycle-exhaust", fixtureFunding)
	_, first, err := fixture.service.StartFishing(context.Background(), StartInput{
		UserID: firstUser, Bait: string(fishing.BaitLure), Count: 1, IdempotencyKey: validTestKey(810),
	})
	if err != nil || first == nil || first.NextAttemptAt == nil {
		t.Fatalf("first pending = (%#v,%v)", first, err)
	}
	_, second, err := fixture.service.StartFishing(context.Background(), StartInput{
		UserID: secondUser, Bait: string(fishing.BaitLure), Count: 1, IdempotencyKey: validTestKey(811),
	})
	if err != nil || second == nil || second.NextAttemptAt == nil {
		t.Fatalf("second pending = (%#v,%v)", second, err)
	}
	if _, err := fixture.database.Exec(`UPDATE game_fishing_batches SET attempt_count=9 WHERE id=?`, second.BatchID); err != nil {
		t.Fatal(err)
	}

	decisionNow := *first.NextAttemptAt
	beforeCapacity := fishingCapacityHeadroom(t, fixture.database)
	randomCalls := fixture.random.callCount()
	result, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), decisionNow, 2, time.Now().Add(time.Minute),
	)
	if err != nil || result != (RecoveryResult{Processed: 2}) {
		t.Fatalf("failure recovery = (%+v,%v)", result, err)
	}
	var attempt, exhausted int
	var next sql.NullInt64
	if err := fixture.database.QueryRow(`SELECT attempt_count,next_attempt_at,retry_exhausted FROM game_fishing_batches WHERE id=?`, first.BatchID).Scan(&attempt, &next, &exhausted); err != nil {
		t.Fatal(err)
	}
	if attempt != 1 || exhausted != 0 || !next.Valid || next.Int64 <= decisionNow {
		t.Fatalf("scheduled retry = attempt:%d next:%v exhausted:%d", attempt, next, exhausted)
	}
	if err := fixture.database.QueryRow(`SELECT attempt_count,next_attempt_at,retry_exhausted FROM game_fishing_batches WHERE id=?`, second.BatchID).Scan(&attempt, &next, &exhausted); err != nil {
		t.Fatal(err)
	}
	if attempt != 10 || exhausted != 1 || next.Valid {
		t.Fatalf("exhausted retry = attempt:%d next:%v exhausted:%d", attempt, next, exhausted)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM admin_alerts WHERE kind='fishing_retry_exhausted' AND ref=?`, second.BatchID); got != 1 {
		t.Fatalf("retry exhaustion alerts = %d", got)
	}
	if fixture.random.callCount() != randomCalls {
		t.Fatal("failure recovery generated new Fishing outcomes")
	}
	if after := fishingCapacityHeadroom(t, fixture.database); after.Cmp(beforeCapacity) != 0 {
		t.Fatalf("failed recovery changed capacity headroom: before=%s after=%s", beforeCapacity, after)
	}
	again, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), decisionNow, 2, time.Now().Add(time.Minute),
	)
	if err != nil || again != (RecoveryResult{}) {
		t.Fatalf("same-decision retry = (%+v,%v)", again, err)
	}
}

func TestFishingLifecycleRecoveryUsesFrozenTimeAndBudget(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	fixture.service.beforeSettlement = func(string) error { return errInjected }
	userID := fixture.seedUser("lifecycle-frozen-time", fixtureFunding)
	_, pending, err := fixture.service.StartFishing(context.Background(), StartInput{
		UserID: userID, Bait: string(fishing.BaitPremium), Count: 1, IdempotencyKey: validTestKey(820),
	})
	if err != nil || pending == nil || pending.NextAttemptAt == nil {
		t.Fatalf("pending = (%#v,%v)", pending, err)
	}
	fixture.service.beforeSettlement = nil
	fixture.clock.Store(*pending.NextAttemptAt + 86_400)

	beforeDue, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), *pending.NextAttemptAt-1, 1, time.Now().Add(time.Minute),
	)
	if err != nil || beforeDue != (RecoveryResult{}) {
		t.Fatalf("before frozen due = (%+v,%v)", beforeDue, err)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches WHERE id=? AND state='reserved'`, pending.BatchID); got != 1 {
		t.Fatal("runtime clock replaced the frozen decision time")
	}
	atDue, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), *pending.NextAttemptAt, 1, time.Now().Add(time.Minute),
	)
	if err != nil || atDue != (RecoveryResult{Processed: 1}) {
		t.Fatalf("at frozen due = (%+v,%v)", atDue, err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.service.RecoverBeforeListenAt(cancelled, fixtureNow, 1, time.Now().Add(time.Minute)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error = %v", err)
	}
	if _, err := fixture.service.RecoverBeforeListenAt(context.Background(), fixtureNow, 1, time.Now().Add(-time.Second)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired budget error = %v", err)
	}
	for _, call := range []func() error{
		func() error {
			_, err := fixture.service.RecoverBeforeListenAt(nil, fixtureNow, 1, time.Now().Add(time.Minute))
			return err
		},
		func() error {
			_, err := fixture.service.RecoverBeforeListenAt(context.Background(), -1, 1, time.Now().Add(time.Minute))
			return err
		},
		func() error {
			_, err := fixture.service.RecoverBeforeListenAt(context.Background(), 253402300800, 1, time.Now().Add(time.Minute))
			return err
		},
		func() error {
			_, err := fixture.service.RecoverBeforeListenAt(context.Background(), fixtureNow, 0, time.Now().Add(time.Minute))
			return err
		},
		func() error {
			_, err := fixture.service.RecoverBeforeListenAt(context.Background(), fixtureNow, 101, time.Now().Add(time.Minute))
			return err
		},
		func() error {
			_, err := fixture.service.RecoverBeforeListenAt(context.Background(), fixtureNow, 1, time.Time{})
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid input error = %v", err)
		}
	}
	var missing *Service
	if _, err := missing.RecoverBeforeListenAt(context.Background(), fixtureNow, 1, time.Now().Add(time.Minute)); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil service error = %v", err)
	}
}

func TestFishingLifecycleRecoveryPreservesStartupCapacityValidation(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	fixture.service.beforeSettlement = func(string) error { return errInjected }
	userID := fixture.seedUser("lifecycle-capacity-validation", fixtureFunding)
	_, pending, err := fixture.service.StartFishing(context.Background(), StartInput{
		UserID: userID, Bait: string(fishing.BaitWorm), Count: 1, IdempotencyKey: validTestKey(830),
	})
	if err != nil || pending == nil || pending.NextAttemptAt == nil {
		t.Fatalf("pending = (%#v,%v)", pending, err)
	}
	if _, err := fixture.database.Exec(`UPDATE credit_capacity SET reserved_future_rows=? WHERE id=1`, db.EncodeU128(db.U128{})); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), *pending.NextAttemptAt, 1, time.Now().Add(time.Minute),
	)
	if !errors.Is(err, ledger.ErrInvariant) || result != (RecoveryResult{}) {
		t.Fatalf("capacity validation = (%+v,%v)", result, err)
	}
	if fixture.service.recovered.Load() {
		t.Fatal("failed persisted validation opened the worker gate")
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches WHERE id=? AND state='reserved' AND attempt_count=0`, pending.BatchID); got != 1 {
		t.Fatal("failed persisted validation changed the batch")
	}
}

func fishingCapacityHeadroom(t *testing.T, database *sql.DB) *big.Int {
	t.Helper()
	var last int64
	var raw []byte
	if err := database.QueryRow(`SELECT last_ledger_seq,reserved_future_rows FROM credit_capacity WHERE id=1`).Scan(&last, &raw); err != nil {
		t.Fatal(err)
	}
	reserved, err := db.DecodeU128(raw)
	if err != nil {
		t.Fatal(err)
	}
	return new(big.Int).Add(big.NewInt(last), reserved.Big())
}
