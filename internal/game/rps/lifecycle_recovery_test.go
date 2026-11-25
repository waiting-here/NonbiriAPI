package rps

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/economyaudit"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestLedgerRetentionPreservesUnfinishedSessionRecovery(t *testing.T) {
	fixture := newRPSFixture(t)
	users, _ := fixture.startThree(game.RPSModeQuick, rpsTestFunding, 9000)
	before := fixture.sessionForUser(users[0])
	ctx := context.Background()
	fixture.clock.Add(31 * 24 * 60 * 60)
	if _, err := fixture.database.Exec(`INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','0',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fixture.clock.Load()); err != nil {
		t.Fatal(err)
	}
	tx, err := fixture.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ready, err := economyaudit.AdvanceTx(ctx, tx, fixture.clock.Load())
	if err != nil || !ready {
		t.Fatal(ready, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		result, err := ledger.RetainDetails(ctx, fixture.database, fixture.clock.Load(), 100)
		if err != nil {
			t.Fatal(err)
		}
		if !result.More {
			break
		}
	}
	if fixture.scalar(`SELECT count(*) FROM credit_operations WHERE source_type='rps_session' AND source_id=? AND compacted=0`, before.ID) == 0 {
		t.Fatal("unfinished session evidence was compacted")
	}
	if err := fixture.service.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.service = fixture.newService(8)
	if err := fixture.service.RecoverBeforeListen(ctx); err != nil {
		t.Fatal(err)
	}
	if fixture.scalar("SELECT count(*) FROM game_rps_summaries WHERE session_id=? AND terminal_reason='server_restart'", before.ID) != 1 {
		t.Fatal("missing cancellation after retention")
	}
}

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
	if err != nil || second.Processed != 1 || !second.More {
		t.Fatal(second, err)
	}
	for !fixture.service.recovered.Load() {
		if _, err = fixture.service.RecoverBeforeListenAt(context.Background(), decisionNow, 1, time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if fixture.scalar("SELECT count(*) FROM game_rps_queue") != 0 {
		t.Fatal("queue survived restart")
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM credit_operations
WHERE kind='rps_queue_release' AND created_at<>?`, decisionNow); got != 0 {
		t.Fatalf("queue releases with non-frozen time=%d", got)
	}
}

func TestRestartCancelsHiddenGestureWithoutRevealingIt(t *testing.T) {
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
	if fixture.scalar("SELECT count(*) FROM game_rps_summaries WHERE session_id=? AND terminal_reason='server_restart'", locked.ID) != 1 || fixture.scalar("SELECT count(*) FROM game_rps_seats WHERE session_id=?", locked.ID) != 0 {
		t.Fatal("hidden choice survived cancellation")
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
	second, err := restarted.RecoverBeforeListenAt(
		context.Background(), decisionNow, 1, time.Now().Add(time.Minute),
	)
	if err != nil || second.Processed != 0 || second.More || !restarted.recovered.Load() {
		t.Fatalf("terminal retry batch=(%+v,%v) recovered=%v", second, err, restarted.recovered.Load())
	}
	if _, found := fixture.maybeSession(record.ID); found ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_refund' AND source_id=?`, record.ID) != 1 ||
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
