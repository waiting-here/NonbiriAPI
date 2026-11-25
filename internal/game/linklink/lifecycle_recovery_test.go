package linklink

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

// Holding the only reader isolates startup validation from mutation work.
func TestStandaloneRecoveryValidationUsesCallerBudget(t *testing.T) {
	fixture := newFixture(t)
	reader := fixture.store.ReadDB()
	reader.SetMaxOpenConns(1)
	held, err := reader.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before := reader.Stats().WaitCount
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- fixture.service.RecoverBeforeListen(ctx) }()
		synctest.Wait()
		if reader.Stats().WaitCount != before+1 {
			t.Fatal("validation did not wait for the occupied reader")
		}
		time.Sleep(3 * time.Second)
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("validation error after caller canceled: %v", err)
		}
	})
	if fixture.service.recovered.Load() || fixture.service.recoveryValidated.Load() {
		t.Fatal("canceled validation marked recovery ready")
	}
}

func TestRecoverBeforeListenAtUsesFrozenDeadlineAndExactBatches(t *testing.T) {
	fixture := newFixture(t)
	if err := fixture.service.RecoverBeforeListen(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadlines := make([]int64, 0, 2)
	for index := 0; index < 2; index++ {
		userID, _ := fixture.seedUser("bounded-recovery-"+string(rune('a'+index)), testFunding)
		started, err := fixture.service.startLegacy(context.Background(), StartInput{
			UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(300 + index),
		})
		if err != nil {
			t.Fatal(err)
		}
		deadlines = append(deadlines, started.State.Deadline)
	}
	if deadlines[0] != deadlines[1] {
		t.Fatalf("fixture deadlines = %v", deadlines)
	}
	decisionNow := deadlines[0]
	fixture.clock.Store(-1)

	early, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), decisionNow-1, 100, time.Now().Add(time.Minute),
	)
	if err != nil || early != (RecoveryResult{}) {
		t.Fatalf("early recovery = (%+v,%v)", early, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions`) != 2 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries`) != 0 {
		t.Fatal("deadline-1 changed LinkLink authority")
	}

	first, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), decisionNow, 1, time.Now().Add(time.Minute),
	)
	if err != nil || first.Processed != 1 || !first.More {
		t.Fatalf("first recovery = (%+v,%v)", first, err)
	}
	second, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), decisionNow, 1, time.Now().Add(time.Minute),
	)
	if err != nil || second.Processed != 1 || second.More {
		t.Fatalf("second recovery = (%+v,%v)", second, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions`) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE terminal_reason='timed_out' AND terminal_at=?`, decisionNow) != 2 {
		t.Fatal("bounded recovery did not terminalize both sessions at frozen decision time")
	}
}

func TestRecoverBeforeListenAtRestartCleansLeaseAndIsRepeatable(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("bounded-restart", testFunding)
	started, err := fixture.service.startLegacy(context.Background(), StartInput{
		UserID: userID, Spec: game.LinkLinkSpec8x8, IdempotencyKey: fixture.key(310),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: fixture.mustID("gle_"),
	}); err != nil {
		t.Fatal(err)
	}
	otherUserID, otherBinding := fixture.seedUser("bounded-restart-other", testFunding)
	other, err := fixture.service.startLegacy(context.Background(), StartInput{
		UserID: otherUserID, Spec: game.LinkLinkSpec10x10, IdempotencyKey: fixture.key(311),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{
		UserID: otherUserID, SessionBinding: otherBinding, SessionID: other.State.SessionID, LeaseID: fixture.mustID("gle_"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := New(Options{Finance: registeredFinance(t, "linklink").LinkLink,
		Store: fixture.store, UserAuthorizer: fixture.authorizer, Continuation: fixture.continuation,
		Limiter: fixture.limiter, Random: fixture.random,
		Now:         func() time.Time { return time.Unix(fixture.clock.Load(), 0).UTC() },
		HealthEpoch: 8, WorkerInterval: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.service = restarted
	fixture.continuation.service = restarted
	t.Cleanup(func() { _ = restarted.Close() })
	fixture.clock.Store(-1)

	leaseBatch, err := restarted.RecoverBeforeListenAt(
		context.Background(), testNow, 1, time.Now().Add(time.Minute),
	)
	if err != nil || leaseBatch.Processed != 1 || !leaseBatch.More {
		t.Fatalf("lease recovery = (%+v,%v)", leaseBatch, err)
	}
	secondLeaseBatch, err := restarted.RecoverBeforeListenAt(
		context.Background(), testNow, 1, time.Now().Add(time.Minute),
	)
	if err != nil || secondLeaseBatch.Processed != 1 || !secondLeaseBatch.More {
		t.Fatal(secondLeaseBatch, err)
	}
	for range 2 {
		if _, err = restarted.RecoverBeforeListenAt(context.Background(), testNow, 1, time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if fixture.scalar("SELECT COUNT(*) FROM game_linklink_summaries WHERE terminal_reason='cancelled' AND score IS NULL") != 2 {
		t.Fatal("missing cancellation receipts")
	}
	if fixture.scalar("SELECT COUNT(*) FROM credit_operations WHERE kind='linklink_refund'") != 2 {
		t.Fatal("missing original-asset refunds")
	}

}

type failingRecoveryContinuation struct{ err error }

func (continuation failingRecoveryContinuation) AuthorizeContinuation(
	context.Context,
	*sql.Tx,
	maintenance.ContinuationRequest,
) (maintenance.ContinuationSnapshot, error) {
	return maintenance.ContinuationSnapshot{}, continuation.err
}

func TestRecoverBeforeListenAtMaintenanceRollbackAndRetry(t *testing.T) {
	fixture := newFixture(t)
	if err := fixture.service.RecoverBeforeListen(context.Background()); err != nil {
		t.Fatal(err)
	}
	userID, _ := fixture.seedUser("bounded-maintenance", testFunding)
	started, err := fixture.service.startLegacy(context.Background(), StartInput{
		UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(320),
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(started.State.Deadline)
	fixture.setMaintenance(true)
	wantErr := errors.New("continuation unavailable")
	fixture.service.continuation = failingRecoveryContinuation{err: wantErr}

	result, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), started.State.Deadline, 1, time.Now().Add(time.Minute),
	)
	if !errors.Is(err, ErrInvariant) || result != (RecoveryResult{}) {
		t.Fatalf("failed recovery = (%+v,%v)", result, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=?`, started.State.SessionID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, started.State.SessionID) != 0 {
		t.Fatal("failed maintenance continuation did not roll back")
	}

	fixture.service.continuation = fixture.continuation
	retried, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), started.State.Deadline, 1, time.Now().Add(time.Minute),
	)
	if err != nil || retried.Processed != 1 || retried.More || fixture.continuation.calls.Load() != 1 {
		t.Fatalf("retried recovery = (%+v,%v), continuation calls=%d", retried, err, fixture.continuation.calls.Load())
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=? AND terminal_at=?`, started.State.SessionID, started.State.Deadline) != 1 {
		t.Fatal("maintenance retry did not converge at frozen deadline")
	}
}

func TestRecoverBeforeListenAtValidatesInputsBudgetAndPersistedRows(t *testing.T) {
	fixture := newFixture(t)
	deadline := time.Now().Add(time.Minute)
	for name, call := range map[string]func() error{
		"nil context": func() error {
			_, err := fixture.service.RecoverBeforeListenAt(nil, testNow, 1, deadline)
			return err
		},
		"negative time": func() error {
			_, err := fixture.service.RecoverBeforeListenAt(context.Background(), -1, 1, deadline)
			return err
		},
		"zero limit": func() error {
			_, err := fixture.service.RecoverBeforeListenAt(context.Background(), testNow, 0, deadline)
			return err
		},
		"large limit": func() error {
			_, err := fixture.service.RecoverBeforeListenAt(context.Background(), testNow, 101, deadline)
			return err
		},
		"zero deadline": func() error {
			_, err := fixture.service.RecoverBeforeListenAt(context.Background(), testNow, 1, time.Time{})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.service.RecoverBeforeListenAt(canceled, testNow, 1, deadline); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
	if _, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), testNow, 1, time.Now().Add(-time.Second),
	); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired budget error = %v", err)
	}

	userID, _ := fixture.seedUser("bounded-hostile", testFunding)
	started, err := fixture.service.startLegacy(context.Background(), StartInput{
		UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(330),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Exec(`UPDATE game_linklink_sessions SET board_blob=? WHERE id=?`, []byte{1}, started.State.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), testNow, 100, deadline,
	); !errors.Is(err, ErrInvariant) {
		t.Fatalf("hostile persisted row error = %v", err)
	}
	if fixture.service.recovered.Load() {
		t.Fatal("hostile persisted row marked startup recovery complete")
	}
}
