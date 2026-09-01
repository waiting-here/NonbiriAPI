package rps

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestRecoverBeforeListenAtBindsFrozenTimeLimitAndMore(t *testing.T) {
	fixture := newRPSFixture(t)
	deadlines := make([]int64, 0, 2)
	for index := 0; index < 2; index++ {
		userID, _ := fixture.seedUser("bounded-recovery-"+string(rune('a'+index)), rpsTestFunding)
		queued := fixture.enqueue(userID, game.RPSModeQuick, byte(0x70+index), byte(0x60+index), 8000+index)
		deadlines = append(deadlines, queued.Queue.Deadline)
	}
	decisionNow := deadlines[0]
	if deadlines[1] > decisionNow {
		decisionNow = deadlines[1]
	}
	fixture.clock.Add(10)
	futureUser, _ := fixture.seedUser("bounded-recovery-future", rpsTestFunding)
	future := fixture.enqueue(futureUser, game.RPSModeQuick, 0x79, 0x69, 8010)
	if future.Queue.Deadline <= decisionNow {
		t.Fatalf("future deadline=%d decision=%d", future.Queue.Deadline, decisionNow)
	}
	fixture.service.recovered.Store(false)
	fixture.clock.Store(-1)

	first, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), decisionNow, 1, time.Now().Add(time.Minute),
	)
	if err != nil || first.Processed != 1 || !first.More || fixture.service.recovered.Load() {
		t.Fatalf("first recovery=(%+v,%v) recovered=%v", first, err, fixture.service.recovered.Load())
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM game_rps_queue`); got != 2 {
		t.Fatalf("queues after first=%d", got)
	}

	second, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), decisionNow, 1, time.Now().Add(time.Minute),
	)
	if err != nil || second.Processed != 1 || second.More || !fixture.service.recovered.Load() {
		t.Fatalf("second recovery=(%+v,%v) recovered=%v", second, err, fixture.service.recovered.Load())
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM game_rps_queue`); got != 1 {
		t.Fatalf("queues after second=%d", got)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM game_rps_queue WHERE id=? AND deadline=?`, future.Queue.ID, future.Queue.Deadline); got != 1 {
		t.Fatal("startup recovery changed an unexpired queue")
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM credit_operations
WHERE kind='rps_queue_release' AND created_at<>?`, decisionNow); got != 0 {
		t.Fatalf("queue releases with non-frozen time=%d", got)
	}
}

func TestRecoverBeforeListenAtRestoresPhaseWithoutRevealingOrReplacingGesture(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeStandard, rpsTestFunding, 8100)
	record := fixture.sessionForUser(users[0])
	seat, ok := seatForUser(&record, users[0])
	if !ok {
		t.Fatal("owner seat missing")
	}
	if _, err := fixture.service.Action(context.Background(), ActionInput{
		UserID: users[0], SessionBinding: bindings[users[0]], SessionID: record.ID,
		PhaseSeq: record.PhaseSeq.Decimal(), ExpectedRevision: record.Revision.Decimal(),
		Action: "gesture", Gesture: GestureRock, IdempotencyKey: fixture.key(8200),
	}); err != nil {
		t.Fatal(err)
	}
	locked := fixture.sessionForUser(users[0])
	before := locked.Seats[seat]
	if len(before.GestureEnvelope) == 0 || before.GesturePhaseSeq == nil || before.LastActionPhaseSeq == nil {
		t.Fatalf("submitted gesture not persisted: %+v", before)
	}

	if err := fixture.service.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := fixture.newService(8)
	fixture.service = restarted
	decisionNow := *locked.PhaseDeadline + 100
	fixture.clock.Store(-1)
	result, err := restarted.RecoverBeforeListenAt(
		context.Background(), decisionNow, 100, time.Now().Add(time.Minute),
	)
	if err != nil || result.Processed != 1 || result.More || !restarted.recovered.Load() {
		t.Fatalf("phase recovery=(%+v,%v) recovered=%v", result, err, restarted.recovered.Load())
	}
	after := fixture.sessionForUser(users[0])
	got := after.Seats[seat]
	if after.HealthEpoch != 8 || after.Phase != locked.Phase || after.PhaseSeq != locked.PhaseSeq ||
		after.PhaseDeadline == nil || *after.PhaseDeadline != decisionNow+int64(after.GestureSeconds) {
		t.Fatalf("recovered session phase=%s seq=%s health=%d deadline=%v", after.Phase, after.PhaseSeq.Decimal(), after.HealthEpoch, after.PhaseDeadline)
	}
	if !bytes.Equal(got.GestureEnvelope, before.GestureEnvelope) || got.GesturePhaseSeq == nil ||
		*got.GesturePhaseSeq != *before.GesturePhaseSeq || got.LastActionPhaseSeq == nil ||
		*got.LastActionPhaseSeq != *before.LastActionPhaseSeq || got.FollowerAction != nil {
		t.Fatal("health recovery changed hidden gesture material")
	}
	for index, candidate := range after.Seats {
		if index != seat && (candidate.GestureEnvelope != nil || candidate.GesturePhaseSeq != nil || candidate.LastActionPhaseSeq != nil) {
			t.Fatalf("seat %d gained hidden gesture material", index)
		}
		if candidate.TimeoutCount.Big().Sign() != 0 {
			t.Fatalf("seat %d timed out during restart", index)
		}
	}
}

func TestRecoverBeforeListenAtRunsCurrentPhaseDeadline(t *testing.T) {
	fixture := newRPSFixture(t)
	users, _ := fixture.startThree(game.RPSModeQuick, rpsTestFunding, 8300)
	record := fixture.sessionForUser(users[0])
	decisionNow := *record.PhaseDeadline
	fixture.clock.Store(-1)

	result, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), decisionNow, 100, time.Now().Add(time.Minute),
	)
	if err != nil || result.Processed != 1 || result.More {
		t.Fatalf("deadline recovery=(%+v,%v)", result, err)
	}
	if _, found := fixture.maybeSession(record.ID); found ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_summaries WHERE session_id=? AND terminal_at=?`, record.ID, decisionNow) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_pending_results WHERE session_id_text=?`, record.ID) != 3 {
		t.Fatal("deadline recovery did not atomically converge quick session")
	}
}

func TestRecoverBeforeListenAtTerminalProcessingConvergesAfterHealthBatch(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeQuick, rpsTestFunding, 8400)
	record := fixture.sessionForUser(users[0])
	fixture.service.beforeTerminalCommit = func() error { return errors.New("injected terminal failure") }
	key := 8500
	fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureScissors, GestureScissors}, &key)
	processing, found := fixture.maybeSession(record.ID)
	if !found || processing.State != StateTerminalProcessing || processing.TerminalNextRetryAt == nil {
		t.Fatalf("processing=%+v found=%v", processing, found)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_terminal' AND source_id=?`, record.ID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_summaries WHERE session_id=?`, record.ID) != 0 {
		t.Fatal("failed terminal attempt left partial facts")
	}

	if err := fixture.service.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := fixture.newService(9)
	fixture.service = restarted
	decisionNow := *processing.TerminalNextRetryAt
	fixture.clock.Store(-1)

	first, err := restarted.RecoverBeforeListenAt(
		context.Background(), decisionNow, 1, time.Now().Add(time.Minute),
	)
	if err != nil || first.Processed != 1 || !first.More || restarted.recovered.Load() {
		t.Fatalf("terminal health batch=(%+v,%v) recovered=%v", first, err, restarted.recovered.Load())
	}
	stillProcessing, found := fixture.maybeSession(record.ID)
	if !found || stillProcessing.State != StateTerminalProcessing || stillProcessing.HealthEpoch != 9 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_summaries WHERE session_id=?`, record.ID) != 0 {
		t.Fatal("health batch partially finalized terminal session")
	}

	second, err := restarted.RecoverBeforeListenAt(
		context.Background(), decisionNow, 1, time.Now().Add(time.Minute),
	)
	if err != nil || second.Processed != 1 || second.More || !restarted.recovered.Load() {
		t.Fatalf("terminal retry batch=(%+v,%v) recovered=%v", second, err, restarted.recovered.Load())
	}
	if _, found := fixture.maybeSession(record.ID); found ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_terminal' AND source_id=?`, record.ID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_summaries WHERE session_id=?`, record.ID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_pending_results WHERE session_id_text=?`, record.ID) != 3 {
		t.Fatal("terminal recovery did not converge exactly once")
	}
}

func TestRecoverBeforeListenAtRejectsInvalidBudgetAndPropagatesContext(t *testing.T) {
	fixture := newRPSFixture(t)
	deadline := time.Now().Add(time.Minute)
	for name, call := range map[string]func() error{
		"nil context": func() error {
			_, err := fixture.service.RecoverBeforeListenAt(nil, rpsTestNow, 1, deadline)
			return err
		},
		"zero limit": func() error {
			_, err := fixture.service.RecoverBeforeListenAt(context.Background(), rpsTestNow, 0, deadline)
			return err
		},
		"large limit": func() error {
			_, err := fixture.service.RecoverBeforeListenAt(context.Background(), rpsTestNow, 101, deadline)
			return err
		},
		"zero deadline": func() error {
			_, err := fixture.service.RecoverBeforeListenAt(context.Background(), rpsTestNow, 1, time.Time{})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.service.RecoverBeforeListenAt(canceled, rpsTestNow, 1, deadline); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error=%v", err)
	}
	if _, err := fixture.service.RecoverBeforeListenAt(
		context.Background(), rpsTestNow, 1, time.Now().Add(-time.Second),
	); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error=%v", err)
	}
}
