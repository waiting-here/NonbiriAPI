package linklink

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

func TestMaintenanceContinuationRequiresLiveBindingExactLeaseAndAction(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("maintenance", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(70)})
	if err != nil {
		t.Fatal(err)
	}
	leaseID := fixture.mustID("gle_")
	lease, err := fixture.service.RenewLease(context.Background(), LeaseInput{UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: leaseID})
	if err != nil || lease.ExpiresAt != testNow+15 {
		t.Fatalf("initial lease = (%+v,%v)", lease, err)
	}
	fixture.setMaintenance(true)
	current, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
	if err != nil || current.State == nil || current.Summary != nil || current.State.SessionID != started.State.SessionID {
		t.Fatalf("maintenance read = (%+v,%v)", current, err)
	}
	if _, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: "different-session"}); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("different binding read = %v", err)
	}
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: fixture.mustID("gle_")}); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("different lease renewal = %v", err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_online_leases WHERE session_id=?`, started.State.SessionID) != 1 {
		t.Fatal("maintenance created a replacement lease")
	}
	first, second := fixture.firstLegalPair(started.State.SessionID)
	matched, err := fixture.service.Match(context.Background(), MatchInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, ExpectedRevision: "1",
		First: first, Second: second, IdempotencyKey: fixture.key(71),
	})
	if err != nil || matched.State == nil || matched.State.Revision != "2" {
		t.Fatalf("maintenance exact action = (%+v,%v)", matched, err)
	}
	if _, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec8x8, IdempotencyKey: fixture.key(72)}); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("maintenance start = %v", err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='linklink_entry' AND actor_user_id=?`, userID) != 1 {
		t.Fatal("maintenance created new business")
	}
	if _, err := fixture.database.Exec(`DELETE FROM game_online_leases WHERE session_id=?`, started.State.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding}); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("missing persisted lease read = %v", err)
	}
}

func TestMaintenanceCannotCreateOrReviveExpiredLease(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("lease-expiry", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec8x8, IdempotencyKey: fixture.key(80)})
	if err != nil {
		t.Fatal(err)
	}
	fixture.setMaintenance(true)
	leaseID := fixture.mustID("gle_")
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: leaseID}); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("maintenance lease creation = %v", err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_online_leases WHERE session_id=?`, started.State.SessionID) != 0 {
		t.Fatal("maintenance lease creation wrote a row")
	}
	fixture.setMaintenance(false)
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: leaseID}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Add(15)
	fixture.setMaintenance(true)
	var beforeExpiry, beforeRenew int64
	if err := fixture.database.QueryRow(`SELECT expires_at,last_renewed_at FROM game_online_leases WHERE session_id=? AND lease_id=?`, started.State.SessionID, leaseID).Scan(&beforeExpiry, &beforeRenew); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: leaseID}); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("expired maintenance lease renewal = %v", err)
	}
	var afterExpiry, afterRenew int64
	if err := fixture.database.QueryRow(`SELECT expires_at,last_renewed_at FROM game_online_leases WHERE session_id=? AND lease_id=?`, started.State.SessionID, leaseID).Scan(&afterExpiry, &afterRenew); err != nil {
		t.Fatal(err)
	}
	if beforeExpiry != afterExpiry || beforeRenew != afterRenew {
		t.Fatal("late maintenance lease renewal wrote")
	}
}

func TestContinuationRegistrationIsClosedAndFrozen(t *testing.T) {
	fixture := newFixture(t)
	registry := maintenance.NewRegistry()
	if err := RegisterContinuation(registry, fixture.service); err != nil {
		t.Fatal(err)
	}
	if err := RegisterContinuation(registry, fixture.service); err == nil {
		t.Fatal("duplicate continuation registration succeeded")
	}
	if err := registry.Freeze(); err != nil || !registry.Frozen() {
		t.Fatalf("freeze = %v", err)
	}
	if validContinuationAction(maintenance.ContinuationSession, "start") || validContinuationAction(maintenance.ContinuationSystem, ActionMatch) {
		t.Fatal("closed continuation action set admitted new business or wrong authority")
	}
}

func TestExactDeadlineReadAndLateActionConvergeToTimeout(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("deadline", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(90)})
	if err != nil {
		t.Fatal(err)
	}
	leaseID := fixture.mustID("gle_")
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: leaseID}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(started.State.Deadline)
	current, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
	if err != nil || current.State != nil || current.Summary == nil || current.Summary.SessionID != started.State.SessionID || current.Summary.TerminalReason != TerminalTimedOut {
		t.Fatalf("deadline read = (%+v,%v)", current, err)
	}
	var reason string
	var terminalAt, score int64
	if err := fixture.database.QueryRow(`SELECT terminal_reason,terminal_at,score FROM game_linklink_summaries WHERE session_id=?`, started.State.SessionID).Scan(&reason, &terminalAt, &score); err != nil {
		t.Fatal(err)
	}
	if reason != TerminalTimedOut || terminalAt != started.State.Deadline || score != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=?`, started.State.SessionID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_online_leases WHERE session_id=?`, started.State.SessionID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM idempotency_records WHERE scope='game_linklink'`) != 1 {
		t.Fatal("exact-deadline terminal projection or physical cleanup is wrong")
	}
	if _, err := fixture.service.Match(context.Background(), MatchInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, ExpectedRevision: "1",
		First: Coordinate{0, 0}, Second: Coordinate{0, 1}, IdempotencyKey: fixture.key(91),
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("late action = %v", err)
	}
}

func TestExactMatchReplayAtDeadlineStillMaterializesTimeout(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("deadline-replay", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{
		UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(96),
	})
	if err != nil {
		t.Fatal(err)
	}
	first, second := fixture.firstLegalPair(started.State.SessionID)
	input := MatchInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID,
		ExpectedRevision: "1", First: first, Second: second, IdempotencyKey: fixture.key(97),
	}
	matched, err := fixture.service.Match(context.Background(), input)
	if err != nil || matched.State == nil {
		t.Fatalf("initial match = (%+v,%v)", matched, err)
	}
	fixture.clock.Store(started.State.Deadline)
	replayed, err := fixture.service.Match(context.Background(), input)
	if err != nil || !replayed.IdempotentReplay || !reflect.DeepEqual(replayed.State, matched.State) {
		t.Fatalf("deadline replay = (%+v,%v), want %+v", replayed, err, matched)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=?`, started.State.SessionID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=? AND terminal_reason='timed_out' AND terminal_at=? AND pairs_removed=1 AND score=100`, started.State.SessionID, started.State.Deadline) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM idempotency_records WHERE scope='game_linklink'`) != 2 {
		t.Fatal("deadline replay did not preserve exact response and atomically terminalize authority")
	}
}

func TestDeadlineBoundaryMinusOneAndPlusOne(t *testing.T) {
	for _, offset := range []int64{-1, 1} {
		t.Run(fmt.Sprintf("%+d", offset), func(t *testing.T) {
			fixture := newFixture(t)
			userID, binding := fixture.seedUser(fmt.Sprintf("deadline-%d", offset), testFunding)
			started, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(92 + int(offset+1))})
			if err != nil {
				t.Fatal(err)
			}
			fixture.clock.Store(started.State.Deadline + offset)
			if offset < 0 {
				current, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
				if err != nil || current.State == nil || current.Summary != nil || current.State.ServerNow != started.State.Deadline-1 || fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, started.State.SessionID) != 0 {
					t.Fatalf("deadline-1 = (%+v,%v)", current, err)
				}
				return
			}
			first, second := fixture.firstLegalPair(started.State.SessionID)
			_, err = fixture.service.Match(context.Background(), MatchInput{
				UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, ExpectedRevision: "1",
				First: first, Second: second, IdempotencyKey: fixture.key(95),
			})
			if !errors.Is(err, ErrConflict) || fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=?`, started.State.SessionID) != 0 ||
				fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=? AND terminal_reason='timed_out' AND terminal_at=?`, started.State.SessionID, started.State.Deadline+1) != 1 {
				t.Fatalf("deadline+1 action = %v", err)
			}
		})
	}
}

func TestRecoveryPrecedesWorkerAndCatchesAbsoluteDeadline(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("recovery", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec8x8, IdempotencyKey: fixture.key(100)})
	if err != nil {
		t.Fatal(err)
	}
	leaseID := fixture.mustID("gle_")
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: leaseID}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.StartWorker(context.Background()); err == nil {
		t.Fatal("worker started before recovery")
	}
	fixture.clock.Store(started.State.Deadline + 30)
	fixture.setMaintenance(true)
	if err := fixture.service.RecoverBeforeListen(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fixture.continuation.calls.Load() != 1 {
		t.Fatalf("system timeout continuation calls = %d, want 1", fixture.continuation.calls.Load())
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions`) != 0 || fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE terminal_reason='timed_out'`) != 1 || fixture.scalar(`SELECT COUNT(*) FROM game_online_leases WHERE session_id=?`, started.State.SessionID) != 0 {
		t.Fatal("pre-listener recovery did not converge the expired session")
	}
	fixture.service.workerInterval = time.Millisecond
	if err := fixture.service.StartWorker(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRestartRecoveryPreservesActiveAuthorityAndPrunesOldHealthLease(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("restart-recovery", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec10x10, IdempotencyKey: fixture.key(105)})
	if err != nil {
		t.Fatal(err)
	}
	first, second := fixture.firstLegalPair(started.State.SessionID)
	matched, err := fixture.service.Match(context.Background(), MatchInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, ExpectedRevision: "1",
		First: first, Second: second, IdempotencyKey: fixture.key(106),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: fixture.mustID("gle_"),
	}); err != nil {
		t.Fatal(err)
	}
	before := fixture.sessionBytes(started.State.SessionID)
	if err := fixture.service.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := New(Options{
		Store: fixture.store, UserAuthorizer: fixture.authorizer, Continuation: fixture.continuation,
		Limiter: fixture.limiter, Random: fixture.random,
		Now:         func() time.Time { return time.Unix(fixture.clock.Load(), 0).UTC() },
		HealthEpoch: 8, WorkerInterval: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.continuation.service = restarted
	t.Cleanup(func() {
		if err := restarted.Close(); err != nil {
			t.Errorf("close restarted service: %v", err)
		}
	})
	if err := restarted.RecoverBeforeListen(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := fixture.sessionBytes(started.State.SessionID); !reflect.DeepEqual(after, before) {
		t.Fatalf("restart recovery changed active authority: before=%+v after=%+v", before, after)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_online_leases WHERE session_id=?`, started.State.SessionID) != 0 {
		t.Fatal("restart recovery retained an old health-epoch lease")
	}
	current, err := restarted.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
	if err != nil || current.State == nil || current.Summary != nil || current.State.Revision != matched.State.Revision || current.State.Deadline != matched.State.Deadline || !reflect.DeepEqual(current.State.Board, matched.State.Board) {
		t.Fatalf("restarted state = (%+v,%v), want %+v", current, err, matched.State)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, started.State.SessionID) != 0 {
		t.Fatal("restart recovery terminalized an unexpired session")
	}
	if err := restarted.StartWorker(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAbandonRequiresConfirmationNeverRefundsAndAllowsFreshCharge(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("abandon", testFunding)
	startInput := StartInput{UserID: userID, Spec: game.LinkLinkSpec10x10, IdempotencyKey: fixture.key(110)}
	started, err := fixture.service.Start(context.Background(), startInput)
	if err != nil {
		t.Fatal(err)
	}
	abandonInput := AbandonInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID,
		ExpectedRevision: "1", Confirmation: true, IdempotencyKey: fixture.key(112),
	}
	unconfirmed := abandonInput
	unconfirmed.Confirmation = false
	if _, err := fixture.service.Abandon(context.Background(), unconfirmed); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unconfirmed abandon = %v", err)
	}
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: fixture.mustID("gle_")}); err != nil {
		t.Fatal(err)
	}
	summary, err := fixture.service.Abandon(context.Background(), abandonInput)
	if err != nil || summary.TerminalReason != TerminalAbandoned || summary.Score != nil || summary.TotalPairs != 50 {
		t.Fatalf("abandon = (%+v,%v)", summary, err)
	}
	replayedSummary, err := fixture.service.Abandon(context.Background(), abandonInput)
	if err != nil || !reflect.DeepEqual(replayedSummary, summary) {
		t.Fatalf("abandon replay = (%+v,%v)", replayedSummary, err)
	}
	changedAbandon := abandonInput
	changedAbandon.ExpectedRevision = "2"
	if _, err := fixture.service.Abandon(context.Background(), changedAbandon); !errors.Is(err, ErrConflict) {
		t.Fatalf("abandon key accepted a different digest: %v", err)
	}
	if _, err := fixture.database.Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Abandon(context.Background(), abandonInput); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked abandon replay = %v", err)
	}
	if _, err := fixture.database.Exec(`UPDATE users SET is_banned=0 WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if fixture.balance(userID) != "999000" || fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE user_id=?`, userID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_online_leases WHERE session_id=?`, started.State.SessionID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM idempotency_records WHERE scope='game_linklink'`) != 2 {
		t.Fatal("abandon refunded or left active material")
	}
	replayed, err := fixture.service.Start(context.Background(), startInput)
	if err != nil || !replayed.IdempotentReplay || replayed.HTTPStatus != 201 || !reflect.DeepEqual(replayed.State, started.State) ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE user_id=?`, userID) != 0 || fixture.balance(userID) != "999000" {
		t.Fatalf("abandoned start replay = (%+v,%v), balance=%s", replayed, err, fixture.balance(userID))
	}
	changed := startInput
	changed.Spec = game.LinkLinkSpec6x8
	if _, err := fixture.service.Start(context.Background(), changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("abandoned start key accepted a different body: %v", err)
	}
	fresh, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(111)})
	if err != nil || fresh.State == nil || fresh.State.SessionID == started.State.SessionID || fixture.balance(userID) != "998000" ||
		fixture.scalar(`SELECT COUNT(*) FROM idempotency_records WHERE scope='game_linklink'`) != 3 {
		t.Fatalf("fresh post-abandon start = (%+v,%v) balance=%s", fresh, err, fixture.balance(userID))
	}
}
