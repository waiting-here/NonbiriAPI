package runtime

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type recoveryTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *recoveryTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *recoveryTestClock) Advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	c.mu.Unlock()
}

type recoveryZeroSource struct{}

func (recoveryZeroSource) Uint64n(uint64) (uint64, error) { return 0, nil }

type recoveryTestEnv struct {
	store      *db.Store
	service    *db.GameSettlementService
	worker     *RecoveryWorker
	userID     int64
	clock      *recoveryTestClock
	settlement string
}

func newRecoveryTestEnv(t *testing.T) *recoveryTestEnv {
	t.Helper()
	key := bytes.Repeat([]byte{0x71}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	path := filepath.Join(t.TempDir(), "recovery.db")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatalf("db.Open: %v", err)
	}
	user, err := store.CreateUser("recovery-user", "recovery-user", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := store.DB().Exec(`UPDATE users SET credits=? WHERE id=?`, 10_000_000, user.ID); err != nil {
		t.Fatalf("seed credits: %v", err)
	}
	for key, value := range map[string]string{game.GamesEnabledKey: "1", game.FishingEnabledKey: "1"} {
		if err := store.SetSiteConfigValue(key, value); err != nil {
			t.Fatalf("enable %s: %v", key, err)
		}
	}
	clock := &recoveryTestClock{now: time.Unix(1_800_000_000, 0).UTC()}
	service, err := db.NewGameSettlementService(db.GameSettlementServiceConfig{
		Store: store, Now: clock.Now, OutcomeSource: recoveryZeroSource{},
	})
	if err != nil {
		t.Fatalf("NewGameSettlementService: %v", err)
	}
	worker, err := NewRecoveryWorker(RecoveryWorkerConfig{Settlement: service, Store: store, Now: clock.Now, Interval: time.Hour})
	if err != nil {
		t.Fatalf("NewRecoveryWorker: %v", err)
	}
	t.Cleanup(func() {
		_ = service.Close()
		_ = store.Close()
		_ = vault.Close()
	})
	return &recoveryTestEnv{store: store, service: service, worker: worker, userID: user.ID, clock: clock}
}

func (e *recoveryTestEnv) startDue(t *testing.T) db.FishingRound {
	t.Helper()
	round, err := e.service.StartFishingRound(context.Background(), db.StartFishingInput{
		UserID: e.userID, Bait: fishing.BaitWorm, IdempotencyKey: "recovery-" + time.Now().UTC().Format("150405.000000000"),
	})
	if err != nil {
		t.Fatalf("StartFishingRound: %v", err)
	}
	if _, err := e.store.DB().Exec(`UPDATE game_settlements SET next_attempt_at=?,updated_at=? WHERE id=?`, e.clock.Now().Unix(), e.clock.Now().Unix(), round.SettlementID); err != nil {
		t.Fatalf("make settlement due: %v", err)
	}
	e.settlement = round.SettlementID
	return round
}

func (e *recoveryTestEnv) settlementState(t *testing.T) (state string, attempts int64, next sql.NullInt64, class string) {
	t.Helper()
	if err := e.store.DB().QueryRow(`SELECT state,attempt_count,next_attempt_at,last_error_class FROM game_settlements WHERE id=?`, e.settlement).
		Scan(&state, &attempts, &next, &class); err != nil {
		t.Fatalf("read settlement state: %v", err)
	}
	return state, attempts, next, class
}

func (e *recoveryTestEnv) ledgerCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_ledger WHERE game_settlement_id=?`, e.settlement).Scan(&count); err != nil {
		t.Fatalf("read game ledger count: %v", err)
	}
	return count
}

func TestRecoveryWorkerConstructorFailsClosed(t *testing.T) {
	if _, err := NewRecoveryWorker(RecoveryWorkerConfig{}); err == nil {
		t.Fatal("nil dependencies accepted")
	}
	if _, err := NewRecoveryWorker(RecoveryWorkerConfig{Settlement: &db.GameSettlementService{}}); err == nil {
		t.Fatal("nil store accepted")
	}
}

func TestRecoveryWorkerTickerStopsOnShutdown(t *testing.T) {
	worker, err := NewRecoveryWorker(RecoveryWorkerConfig{
		Settlement: &db.GameSettlementService{}, Store: &db.Store{}, Interval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.RunTicker(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ticker shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ticker did not stop after context cancellation")
	}
}

func TestRecoveryWorkerDueSettlementIsExactlyOnce(t *testing.T) {
	env := newRecoveryTestEnv(t)
	env.startDue(t)
	summary, err := env.worker.RecoverDue(context.Background())
	if err != nil {
		t.Fatalf("RecoverDue: %v", err)
	}
	if summary != (RecoverySummary{Due: 1, Settled: 1}) {
		t.Fatalf("summary=%+v", summary)
	}
	state, attempts, next, class := env.settlementState(t)
	if state != string(db.GameCommitted) || attempts != 0 || next.Valid || class != "" || env.ledgerCount(t) != 2 {
		t.Fatalf("settled state=%q attempts=%d next=%v class=%q ledger=%d", state, attempts, next, class, env.ledgerCount(t))
	}
	replay, err := env.worker.RecoverDue(context.Background())
	if err != nil {
		t.Fatalf("second RecoverDue: %v", err)
	}
	if replay != (RecoverySummary{}) || env.ledgerCount(t) != 2 {
		t.Fatalf("second recovery summary=%+v ledger=%d", replay, env.ledgerCount(t))
	}
}

func TestRecoveryWorkerFailureRecordsBackoffAndKeepsPending(t *testing.T) {
	env := newRecoveryTestEnv(t)
	env.startDue(t)
	env.worker.settleDue = func(context.Context, string) (db.FishingRound, error) {
		return db.FishingRound{}, db.ErrGameInternal
	}
	summary, err := env.worker.RecoverDue(context.Background())
	if err != nil {
		t.Fatalf("RecoverDue failure: %v", err)
	}
	if summary != (RecoverySummary{Due: 1, Retried: 1}) {
		t.Fatalf("failure summary=%+v", summary)
	}
	state, attempts, next, class := env.settlementState(t)
	wantNext := env.clock.Now().Add(db.GameRetryInitialDelay).Unix()
	if state != string(db.GameReserved) || attempts != 1 || !next.Valid || next.Int64 != wantNext || class != string(db.GameRetryInternal) || env.ledgerCount(t) != 1 {
		t.Fatalf("pending retry state=%q attempts=%d next=%v want=%d class=%q ledger=%d", state, attempts, next, wantNext, class, env.ledgerCount(t))
	}
	env.clock.Advance(db.GameRetryInitialDelay)
	second, err := env.worker.RecoverDue(context.Background())
	if err != nil || second.Retried != 1 {
		t.Fatalf("second backoff recovery summary=%+v err=%v", second, err)
	}
	_, attempts, next, _ = env.settlementState(t)
	if attempts != 2 || !next.Valid || next.Int64 != env.clock.Now().Add(2*db.GameRetryInitialDelay).Unix() {
		t.Fatalf("second backoff attempts=%d next=%v", attempts, next)
	}
}

func TestRecoveryWorkerManualSettlementReplayDoesNotDuplicateLedger(t *testing.T) {
	env := newRecoveryTestEnv(t)
	round := env.startDue(t)
	if _, err := env.service.SettleFishingRound(context.Background(), db.SettleFishingInput{UserID: env.userID, RoundID: round.RoundID}); err != nil {
		t.Fatalf("manual settle: %v", err)
	}
	if env.ledgerCount(t) != 2 {
		t.Fatalf("manual ledger count=%d", env.ledgerCount(t))
	}
	env.worker.listDue = func(context.Context, time.Time, int) ([]db.DueGameSettlement, error) {
		return []db.DueGameSettlement{{SettlementID: round.SettlementID}}, nil
	}
	summary, err := env.worker.RecoverDue(context.Background())
	if err != nil || summary != (RecoverySummary{Due: 1, Settled: 1}) {
		t.Fatalf("manual/worker replay summary=%+v err=%v", summary, err)
	}
	if env.ledgerCount(t) != 2 {
		t.Fatalf("manual/worker replay duplicated ledger=%d", env.ledgerCount(t))
	}
}

func TestRecoveryWorkerRetryWriteFailureIsNotReportedAsRetried(t *testing.T) {
	env := newRecoveryTestEnv(t)
	env.startDue(t)
	env.worker.settleDue = func(context.Context, string) (db.FishingRound, error) {
		return db.FishingRound{}, db.ErrGameInternal
	}
	sentinel := errors.New("retry write failed")
	var gotClass db.GameRetryErrorClass
	env.worker.recordRetryFailure = func(_ context.Context, _ string, _ time.Time, class db.GameRetryErrorClass) error {
		gotClass = class
		return sentinel
	}
	summary, err := env.worker.RecoverDue(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("retry write error=%v, want sentinel", err)
	}
	if summary.Retried != 0 || summary.Due != 1 || gotClass != db.GameRetryInternal {
		t.Fatalf("retry write summary=%+v class=%q", summary, gotClass)
	}
	state, attempts, next, class := env.settlementState(t)
	if state != string(db.GameReserved) || attempts != 0 || !next.Valid || class != "" {
		t.Fatalf("retry write failure changed row: state=%q attempts=%d next=%v class=%q", state, attempts, next, class)
	}
}

func TestRecoveryWorkerRunRecoversBeforeTickerAndHandlesCancellation(t *testing.T) {
	env := newRecoveryTestEnv(t)
	env.startDue(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- env.worker.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		state, _, _, _ := env.settlementState(t)
		if state == string(db.GameCommitted) {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("startup Run did not settle before ticker")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}

func TestRecoveryWorkerContextCancelAndDeletedRowAreSafeSkips(t *testing.T) {
	env := newRecoveryTestEnv(t)
	env.startDue(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	summary, err := env.worker.RecoverDue(ctx)
	if !errors.Is(err, context.Canceled) || summary != (RecoverySummary{}) {
		t.Fatalf("cancelled recovery summary=%+v err=%v", summary, err)
	}
	if err := env.store.DeleteUserAccount(context.Background(), env.userID); err != nil {
		t.Fatalf("DeleteUserAccount: %v", err)
	}
	env.worker.listDue = func(context.Context, time.Time, int) ([]db.DueGameSettlement, error) {
		return []db.DueGameSettlement{{SettlementID: env.settlement}}, nil
	}
	summary, err = env.worker.RecoverDue(context.Background())
	if err != nil || summary != (RecoverySummary{Due: 1, Skipped: 1}) {
		t.Fatalf("deleted recovery summary=%+v err=%v", summary, err)
	}
}
