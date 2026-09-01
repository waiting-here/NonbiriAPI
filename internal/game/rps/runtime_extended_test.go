package rps

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func (fixture *rpsFixture) maybeSession(sessionID string) (sessionRecord, bool) {
	fixture.t.Helper()
	tx := fixture.mustReadTx()
	defer tx.Rollback()
	record, found, err := loadSessionByID(context.Background(), tx, sessionID)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return record, found
}

func (fixture *rpsFixture) playGestures(sessionID string, bindings map[int64]string, gestures [3]string, key *int) HomeState {
	fixture.t.Helper()
	var last HomeState
	for seat := 0; seat < 3; seat++ {
		record, found := fixture.maybeSession(sessionID)
		if !found {
			fixture.t.Fatalf("session %s ended before seat %d", sessionID, seat)
		}
		userID := *record.Seats[seat].UserID
		result, err := fixture.service.Action(context.Background(), ActionInput{
			UserID: userID, SessionBinding: bindings[userID], SessionID: sessionID,
			PhaseSeq: record.PhaseSeq.Decimal(), ExpectedRevision: record.Revision.Decimal(),
			Action: "gesture", Gesture: gestures[seat], IdempotencyKey: fixture.key(*key),
		})
		*key++
		if err != nil {
			fixture.t.Fatalf("seat %d gesture in %s: %v", seat, record.Phase, err)
		}
		last = result.State
	}
	return last
}

func (fixture *rpsFixture) dealerDecision(sessionID string, bindings map[int64]string, decision, amount string, key *int) HomeState {
	fixture.t.Helper()
	record, found := fixture.maybeSession(sessionID)
	if !found || record.Phase != PhaseDealerRaise || record.DealerSeat == nil {
		fixture.t.Fatalf("dealer phase missing: found=%v record=%+v", found, record)
	}
	userID := *record.Seats[*record.DealerSeat].UserID
	result, err := fixture.service.Action(context.Background(), ActionInput{
		UserID: userID, SessionBinding: bindings[userID], SessionID: sessionID,
		PhaseSeq: record.PhaseSeq.Decimal(), ExpectedRevision: record.Revision.Decimal(),
		Action: "dealer_decision", DealerDecision: decision, RaiseAmount: amount, IdempotencyKey: fixture.key(*key),
	})
	*key++
	if err != nil {
		fixture.t.Fatalf("dealer %s: %v", decision, err)
	}
	return result.State
}

func TestStandardPaidToFreeTieReminderAndForcedTerminal(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeStandard, 100_000, 600)
	record := fixture.sessionForUser(users[0])
	var found bool
	sessionID := record.ID
	key := 620
	tie := [3]string{GestureRock, GestureRock, GestureRock}
	fixture.playGestures(sessionID, bindings, tie, &key)
	fixture.dealerDecision(sessionID, bindings, "no_raise", "", &key)

	paidPhases := 0
	for {
		record, found = fixture.maybeSession(sessionID)
		if !found {
			t.Fatal("session ended before free phase")
		}
		if record.Phase == PhaseFreePoolGesture {
			break
		}
		if record.Phase != PhasePaidPoolGesture {
			t.Fatalf("phase=%s before free pool", record.Phase)
		}
		paidPhases++
		if paidPhases > 8 {
			t.Fatal("paid pool did not become free")
		}
		fixture.playGestures(sessionID, bindings, tie, &key)
	}
	if paidPhases != 4 {
		t.Fatalf("paid phases=%d want=4", paidPhases)
	}
	for free := 1; free <= game.RPSFreeTieLimit; free++ {
		state := fixture.playGestures(sessionID, bindings, tie, &key)
		if free < game.RPSFreeTieLimit {
			record, found = fixture.maybeSession(sessionID)
			if !found || record.Phase != PhaseFreePoolGesture || record.FreePoolStreak.Decimal() != big.NewInt(int64(free)).String() {
				t.Fatalf("free tie %d record=%+v found=%v", free, record, found)
			}
			wantReminder := free >= game.RPSFreeTieReminder
			if (record.ReminderState == "active") != wantReminder {
				t.Fatalf("free tie %d reminder=%s", free, record.ReminderState)
			}
		} else if state.Kind != "pending_result" || state.Result == nil || state.Result.TerminalReason != TerminalFreeTieLimit {
			t.Fatalf("forced terminal state=%+v", state)
		}
	}
	if _, found := fixture.maybeSession(sessionID); found {
		t.Fatal("forced terminal left session")
	}
	var reason string
	var freeCount, welfareTotal []byte
	if err := fixture.database.QueryRow(`SELECT terminal_reason,free_tie_count,welfare_total FROM game_rps_summaries WHERE session_id=?`, sessionID).
		Scan(&reason, &freeCount, &welfareTotal); err != nil {
		t.Fatal(err)
	}
	freeValue, err := db.DecodeU128(freeCount)
	if err != nil || reason != TerminalFreeTieLimit || freeValue.Decimal() != "6" {
		t.Fatalf("summary reason=%s free=%s err=%v", reason, freeValue.Decimal(), err)
	}
	welfare, err := db.DecodeU128(welfareTotal)
	if err != nil || welfare.Big().Sign() <= 0 {
		t.Fatalf("welfare total=%s err=%v", welfare.Decimal(), err)
	}
}

func TestStandardSixthFreePoolNonTieContinues(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeStandard, 100_000, 680)
	record := fixture.sessionForUser(users[0])
	sessionID := record.ID
	key := 700
	tie := [3]string{GestureRock, GestureRock, GestureRock}
	fixture.playGestures(sessionID, bindings, tie, &key)
	fixture.dealerDecision(sessionID, bindings, "no_raise", "", &key)

	for {
		record = fixture.sessionByID(sessionID)
		if record.Phase == PhaseFreePoolGesture {
			break
		}
		if record.Phase != PhasePaidPoolGesture {
			t.Fatalf("phase=%s before free pool", record.Phase)
		}
		fixture.playGestures(sessionID, bindings, tie, &key)
	}
	for free := 1; free < game.RPSFreeTieLimit; free++ {
		fixture.playGestures(sessionID, bindings, tie, &key)
	}
	state := fixture.playGestures(sessionID, bindings,
		[3]string{GestureRock, GestureScissors, GestureScissors}, &key)
	if state.Kind != "session" || state.Session == nil || state.Session.Phase != PhaseGesture {
		t.Fatalf("sixth non-tie did not continue: %+v", state)
	}
	record = fixture.sessionByID(sessionID)
	if record.FreeTieCount.Decimal() != "5" || record.FreePoolStreak.Big().Sign() != 0 || record.ReminderState != "none" {
		t.Fatalf("post-resolution counters free_ties=%s streak=%s reminder=%s",
			record.FreeTieCount.Decimal(), record.FreePoolStreak.Decimal(), record.ReminderState)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM game_rps_summaries WHERE session_id=?`, sessionID); got != 0 {
		t.Fatalf("sixth non-tie created %d summaries", got)
	}
}

func TestDeathmatchUltimateTieThenResolution(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeDeathmatch, 1_000, 700)
	record := fixture.sessionForUser(users[0])
	sessionID := record.ID
	key := 720
	fixture.playGestures(sessionID, bindings, [3]string{GesturePaper, GesturePaper, GesturePaper}, &key)
	record, found := fixture.maybeSession(sessionID)
	if !found || record.Phase != PhaseFreePoolGesture || record.DealerSeat != nil || record.FreePoolStreak.Big().Sign() != 0 {
		t.Fatalf("post-ultimate tie=%+v found=%v", record, found)
	}
	state := fixture.playGestures(sessionID, bindings, [3]string{GestureRock, GestureScissors, GestureScissors}, &key)
	if state.Kind != "pending_result" || state.Result == nil || state.Result.TerminalReason != TerminalUltimateResolved {
		t.Fatalf("ultimate result=%+v", state)
	}
	var reason string
	var baseCount []byte
	if err := fixture.database.QueryRow(`SELECT terminal_reason,base_round_count FROM game_rps_summaries WHERE session_id=?`, sessionID).
		Scan(&reason, &baseCount); err != nil {
		t.Fatal(err)
	}
	base, err := db.DecodeU128(baseCount)
	if err != nil || reason != TerminalUltimateResolved || base.Decimal() != "1" {
		t.Fatalf("summary reason=%s base=%s err=%v", reason, base.Decimal(), err)
	}
}

func TestDealerAndFollowerDeadlineDefaults(t *testing.T) {
	t.Run("dealer-no-raise", func(t *testing.T) {
		fixture := newRPSFixture(t)
		users, bindings := fixture.startThree(game.RPSModeStandard, 100_000, 800)
		record := fixture.sessionForUser(users[0])
		key := 820
		fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureScissors, GestureScissors}, &key)
		record, _ = fixture.maybeSession(record.ID)
		dealer := *record.DealerSeat
		fixture.clock.Store(*record.PhaseDeadline)
		processed, err := fixture.service.runDeadlineOne(context.Background(), record.ID, fixture.clock.Load())
		if err != nil || !processed {
			t.Fatalf("dealer deadline=(%v,%v)", processed, err)
		}
		next, found := fixture.maybeSession(record.ID)
		if !found {
			t.Fatal("dealer default unexpectedly terminalized standard game")
		}
		if next.Seats[dealer].TimeoutCount.Decimal() != "1" || next.DealerRaise != nil {
			t.Fatalf("dealer default seat=%+v raise=%v", next.Seats[dealer], next.DealerRaise)
		}
	})
	t.Run("followers-surrender", func(t *testing.T) {
		fixture := newRPSFixture(t)
		users, bindings := fixture.startThree(game.RPSModeStandard, 100_000, 850)
		record := fixture.sessionForUser(users[0])
		key := 870
		fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureScissors, GesturePaper}, &key)
		fixture.dealerDecision(record.ID, bindings, "raise", "1", &key)
		record, _ = fixture.maybeSession(record.ID)
		dealer := *record.DealerSeat
		fixture.clock.Store(*record.PhaseDeadline)
		processed, err := fixture.service.runDeadlineOne(context.Background(), record.ID, fixture.clock.Load())
		if err != nil || !processed {
			t.Fatalf("follower deadline=(%v,%v)", processed, err)
		}
		next, found := fixture.maybeSession(record.ID)
		if !found {
			t.Fatal("follower default unexpectedly terminalized standard game")
		}
		for seat := range next.Seats {
			want := "0"
			if seat != dealer {
				want = "1"
			}
			if next.Seats[seat].TimeoutCount.Decimal() != want {
				t.Fatalf("seat %d timeout=%s want=%s", seat, next.Seats[seat].TimeoutCount.Decimal(), want)
			}
		}
		reveal, ok := latestReveal(next.RecentEvents)
		if !ok || reveal.ResultCode != RevealTwoSurrenders {
			t.Fatalf("latest reveal=%+v ok=%v", reveal, ok)
		}
	})
}

func TestTerminalRetryIsAtomicAndConverges(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeQuick, 100_000, 900)
	record := fixture.sessionForUser(users[0])
	key := 920
	fixture.service.beforeTerminalCommit = func() error { return errors.New("injected terminal failure") }
	state := fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureScissors, GestureScissors}, &key)
	if state.Kind != "session" || state.Session == nil || state.Session.State != StateTerminalProcessing {
		t.Fatalf("retry state=%+v", state)
	}
	processing, found := fixture.maybeSession(record.ID)
	if !found || processing.TerminalRetryAttemptCount.Decimal() != "1" || processing.TerminalNextRetryAt == nil {
		t.Fatalf("processing=%+v found=%v", processing, found)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_terminal' AND source_id=?`, record.ID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_summaries WHERE session_id=?`, record.ID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_pending_results WHERE session_id_text=?`, record.ID) != 0 {
		t.Fatal("failed terminal left partial terminal facts")
	}
	fixture.service.beforeTerminalCommit = nil
	fixture.clock.Store(*processing.TerminalNextRetryAt)
	completed, err := fixture.service.runTerminalOne(context.Background(), record.ID, fixture.clock.Load())
	if err != nil || !completed {
		t.Fatalf("terminal retry=(%v,%v)", completed, err)
	}
	if _, found := fixture.maybeSession(record.ID); found ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_terminal' AND source_id=?`, record.ID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_summaries WHERE session_id=?`, record.ID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_pending_results WHERE session_id_text=?`, record.ID) != 3 {
		t.Fatal("terminal retry did not converge exactly once")
	}
	if completed, err := fixture.service.runTerminalOne(context.Background(), record.ID, fixture.clock.Load()); err != nil || completed {
		t.Fatalf("terminal replay=(%v,%v)", completed, err)
	}
}

func TestMaintenanceLeaseContinuationAndPendingACK(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeQuick, 100_000, 1000)
	record := fixture.sessionForUser(users[0])
	leaseID := fixture.mustID("gle_")
	lease, err := fixture.service.RenewLease(context.Background(), LeaseInput{UserID: users[0], SessionBinding: bindings[users[0]],
		SessionID: record.ID, LeaseID: leaseID})
	if err != nil || lease.ExpiresAt != fixture.clock.Load()+leaseTTLSeconds {
		t.Fatalf("lease=(%+v,%v)", lease, err)
	}
	fixture.setMaintenance(true)
	if home, err := fixture.service.Read(context.Background(), ReadInput{UserID: users[0], SessionBinding: bindings[users[0]]}); err != nil || home.Kind != "session" {
		t.Fatalf("maintenance continuation read=(%+v,%v)", home, err)
	}
	if _, err := fixture.service.Read(context.Background(), ReadInput{UserID: users[1], SessionBinding: bindings[users[1]]}); !errors.Is(err, ErrMaintenance) {
		t.Fatalf("maintenance expanded read=%v", err)
	}
	fixture.clock.Store(*record.PhaseDeadline - 6)
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{UserID: users[0], SessionBinding: bindings[users[0]],
		SessionID: record.ID, LeaseID: leaseID}); err != nil {
		t.Fatalf("maintenance lease renewal: %v", err)
	}
	fixture.clock.Store(*record.PhaseDeadline)
	processed, err := fixture.service.runDeadlineOne(context.Background(), record.ID, fixture.clock.Load())
	if err != nil || !processed {
		t.Fatalf("maintenance deadline=(%v,%v)", processed, err)
	}
	home, err := fixture.service.Read(context.Background(), ReadInput{UserID: users[0], SessionBinding: bindings[users[0]]})
	if err != nil || home.Kind != "pending_result" || home.Result == nil {
		t.Fatalf("maintenance pending read=(%+v,%v)", home, err)
	}
	ack, err := fixture.service.ACK(context.Background(), ACKInput{UserID: users[0], SessionBinding: bindings[users[0]], SessionID: record.ID})
	if err != nil || ack.HTTPStatus != 204 {
		t.Fatalf("maintenance ACK=(%+v,%v)", ack, err)
	}
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{UserID: users[0], SessionBinding: bindings[users[0]],
		SessionID: record.ID, LeaseID: leaseID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("post-ACK lease recreated=%v", err)
	}
}

func TestLifecycleExportAndCleanupRecoveryGate(t *testing.T) {
	fixture := newRPSFixture(t)
	userID, _ := fixture.seedUser("export-queue", 100_000)
	queued := fixture.enqueue(userID, game.RPSModeQuick, 0x44, 0x45, 1100)
	tx, err := fixture.database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := fixture.service.Lifecycle().ExportUser(context.Background(), tx, userID, fixture.clock.Load())
	_ = tx.Rollback()
	if err != nil || exported.Current == nil || exported.Current.Kind != "queue" || exported.Current.Queue.ID != queued.Queue.ID {
		t.Fatalf("queue export=(%+v,%v)", exported, err)
	}
	fixture.service.recovered.Store(false)
	if _, err := fixture.service.Lifecycle().Cleanup(context.Background(), fixture.clock.Load()); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("cleanup before recovery=%v", err)
	}
	if _, err := fixture.service.ActiveCounts(context.Background()); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("active counts before recovery=%v", err)
	}
}

func TestLifecycleDeletionUsesCallerFrozenDecisionNow(t *testing.T) {
	fixture := newRPSFixture(t)
	userID, _ := fixture.seedUser("delete-frozen-now", 100_000)
	queued := fixture.enqueue(userID, game.RPSModeQuick, 0x46, 0x47, 1150)

	for _, invalid := range []int64{-1, 253402300800} {
		tx, err := fixture.database.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, prepareErr := fixture.service.Lifecycle().PrepareUserDeletion(context.Background(), tx, userID, invalid)
		_ = tx.Rollback()
		if !errors.Is(prepareErr, ErrInvalidRequest) {
			t.Fatalf("decisionNow=%d error=%v", invalid, prepareErr)
		}
	}

	decisionNow := rpsTestNow + 77
	fixture.clock.Store(-1) // Any adapter-local clock read would fail validation.
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	finalizer, err := fixture.service.Lifecycle().PrepareUserDeletion(context.Background(), tx, userID, decisionNow)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if !finalizer.Commit() {
		t.Fatal("delete finalizer")
	}
	var operationNow int64
	if err := fixture.database.QueryRow(`SELECT created_at FROM credit_operations
WHERE kind='rps_queue_release' AND source_id=?`, queued.Queue.ID).Scan(&operationNow); err != nil {
		t.Fatal(err)
	}
	if operationNow != decisionNow {
		t.Fatalf("release created_at=%d want=%d", operationNow, decisionNow)
	}
}

func TestDeletionFinalizerDoesNotRebuildAfterCleanupFailure(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*rpsTestAccountEvents)
		wantCalls []string
	}{
		{
			name: "forget",
			configure: func(events *rpsTestAccountEvents) {
				events.forgetErr = errors.New("forget failed")
			},
			wantCalls: []string{"forget"},
		},
		{
			name: "discard",
			configure: func(events *rpsTestAccountEvents) {
				events.discardErr = errors.New("discard failed")
			},
			wantCalls: []string{"forget", "discard"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRPSFixture(t)
			users := make([]int64, 3)
			for index := range users {
				users[index], _ = fixture.seedUser("cleanup-failure-"+test.name+string(rune('a'+index)), 100_000)
			}
			test.configure(fixture.account)
			finalizer := &DeletionFinalizer{service: fixture.service, userID: users[0], survivors: users[1:]}
			if !finalizer.Commit() {
				t.Fatal("delete finalizer")
			}
			fixture.account.mu.Lock()
			calls := append([]string(nil), fixture.account.calls...)
			purged := append([]int64(nil), fixture.account.purged...)
			fixture.account.mu.Unlock()
			if len(calls) != len(test.wantCalls) {
				t.Fatalf("calls=%v want=%v", calls, test.wantCalls)
			}
			for index := range calls {
				if calls[index] != test.wantCalls[index] {
					t.Fatalf("calls=%v want=%v", calls, test.wantCalls)
				}
			}
			if len(purged) != 0 {
				t.Fatalf("rebuilt after cleanup failure: %v", purged)
			}
		})
	}
}

func TestLifecycleDeleteBeforeActionDefaultsAndTerminalizesForSurvivors(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeQuick, 100_000, 1300)
	record := fixture.sessionForUser(users[0])
	deletedSeat, ok := seatForUser(&record, users[0])
	if !ok {
		t.Fatal("deleted seat missing")
	}
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	finalizer, err := fixture.service.Lifecycle().PrepareUserDeletion(context.Background(), tx, users[0], fixture.clock.Load())
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	fixture.account.mu.Lock()
	fixture.account.frames = map[int64]bool{}
	fixture.account.subscriptions = map[int64]bool{}
	for _, userID := range users {
		fixture.account.frames[userID] = true
		fixture.account.subscriptions[userID] = true
	}
	fixture.account.purgeErr = errors.New("survivor snapshot rebuild failed")
	fixture.account.mu.Unlock()
	if !finalizer.Commit() {
		t.Fatal("delete finalizer")
	}
	fixture.account.mu.Lock()
	purged := append([]int64(nil), fixture.account.purged...)
	forgotten := append([]int64(nil), fixture.account.forgotten...)
	discarded := append([]int64(nil), fixture.account.discarded...)
	calls := append([]string(nil), fixture.account.calls...)
	framesRemaining := len(fixture.account.frames)
	subscriptionsRemaining := len(fixture.account.subscriptions)
	tombstones := make(map[int64]bool, len(fixture.account.tombstones))
	for userID, tombstoned := range fixture.account.tombstones {
		tombstones[userID] = tombstoned
	}
	fixture.account.mu.Unlock()
	if len(calls) != 3 || calls[0] != "forget" || calls[1] != "discard" || calls[2] != "purge" {
		t.Fatalf("account event call order=%v", calls)
	}
	if len(forgotten) != 1 || forgotten[0] != users[0] || !tombstones[users[0]] {
		t.Fatalf("deleted account forget=%v tombstones=%v", forgotten, tombstones)
	}
	if len(discarded) != 2 || framesRemaining != 0 || subscriptionsRemaining != 0 {
		t.Fatalf("survivor discard=%v frames=%d subscriptions=%d", discarded, framesRemaining, subscriptionsRemaining)
	}
	seenDiscarded := map[int64]bool{}
	for _, userID := range discarded {
		seenDiscarded[userID] = true
	}
	for _, userID := range users[1:] {
		if !seenDiscarded[userID] || tombstones[userID] {
			t.Fatalf("survivor %d discard/tombstone mismatch: discarded=%v tombstones=%v", userID, discarded, tombstones)
		}
		if !fixture.account.reconnect(userID) {
			t.Fatalf("survivor %d cannot reconnect after failed purge", userID)
		}
	}
	if fixture.account.reconnect(users[0]) {
		t.Fatal("deleted account reconnected after permanent forget")
	}
	if len(purged) != 2 {
		t.Fatalf("peer purge=%v", purged)
	}
	seenPurged := map[int64]bool{}
	for _, userID := range purged {
		seenPurged[userID] = true
	}
	if seenPurged[users[0]] || !seenPurged[users[1]] || !seenPurged[users[2]] {
		t.Fatalf("peer purge included deleted or missed survivor: %v", purged)
	}
	record, found := fixture.maybeSession(record.ID)
	if !found || record.Seats[deletedSeat].UserID != nil || record.Seats[deletedSeat].DeletionState != "deletion_pending" {
		t.Fatalf("deleted inflight seat=%+v found=%v", record.Seats[deletedSeat], found)
	}
	key := 1320
	for seat := range record.Seats {
		if seat == deletedSeat {
			continue
		}
		current, _ := fixture.maybeSession(record.ID)
		userID := *current.Seats[seat].UserID
		if _, err := fixture.service.Action(context.Background(), ActionInput{UserID: userID, SessionBinding: bindings[userID],
			SessionID: record.ID, PhaseSeq: current.PhaseSeq.Decimal(), ExpectedRevision: current.Revision.Decimal(),
			Action: "gesture", Gesture: []string{GestureRock, GestureScissors, GesturePaper}[seat], IdempotencyKey: fixture.key(key)}); err != nil {
			t.Fatal(err)
		}
		key++
	}
	record, _ = fixture.maybeSession(record.ID)
	fixture.clock.Store(*record.PhaseDeadline)
	if processed, err := fixture.service.runDeadlineOne(context.Background(), record.ID, fixture.clock.Load()); err != nil || !processed {
		t.Fatalf("deleted-seat deadline=(%v,%v)", processed, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_rps_pending_results WHERE session_id_text=?`, record.ID) != 2 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_rank_facts WHERE session_id_text=?`, record.ID) != 2 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_summary_seats WHERE session_id=? AND user_id IS NULL`, record.ID) != 1 {
		t.Fatal("deleted participant received private facts or survivor facts were lost")
	}
	var outcome string
	column := []string{"seat0_result", "seat1_result", "seat2_result"}[deletedSeat]
	if err := fixture.database.QueryRow(`SELECT `+column+` FROM game_rps_pending_results WHERE session_id_text=? LIMIT 1`, record.ID).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if outcome != "deidentified" {
		t.Fatalf("deleted outcome=%s", outcome)
	}
}

func TestLifecycleDeleteAfterTerminalPurgesPrivateAndRankFacts(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeQuick, 100_000, 1400)
	record := fixture.sessionForUser(users[0])
	deletedSeat, ok := seatForUser(&record, users[0])
	if !ok {
		t.Fatal("deleted seat missing")
	}
	key := 1420
	fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureScissors, GestureScissors}, &key)
	if fixture.scalar(`SELECT COUNT(*) FROM game_rps_pending_results WHERE user_id=?`, users[0]) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_rank_facts WHERE user_id=?`, users[0]) != 1 {
		t.Fatal("terminal private facts missing")
	}
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	finalizer, err := fixture.service.Lifecycle().PrepareUserDeletion(context.Background(), tx, users[0], fixture.clock.Load())
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if !finalizer.Commit() {
		t.Fatal("delete finalizer")
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM game_rps_pending_results WHERE user_id=?`,
		`SELECT COUNT(*) FROM game_rps_rank_facts WHERE user_id=?`,
		`SELECT COUNT(*) FROM game_rps_rank_aggregates WHERE user_id=?`,
		`SELECT COUNT(*) FROM game_rps_fun_stats WHERE user_id=?`,
	} {
		if fixture.scalar(query, users[0]) != 0 {
			t.Fatalf("private lifecycle row remains for %q", query)
		}
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_rps_summary_seats WHERE session_id=? AND user_id IS NULL`, record.ID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_summary_seats WHERE session_id=? AND user_id IS NOT NULL`, record.ID) != 2 {
		t.Fatal("shared summary identity was not selectively deidentified")
	}
	column := []string{"seat0_result", "seat1_result", "seat2_result"}[deletedSeat]
	var nonDeidentified int
	if err := fixture.database.QueryRow(`SELECT COUNT(*) FROM game_rps_pending_results
WHERE session_id_text=? AND `+column+`<>'deidentified'`, record.ID).Scan(&nonDeidentified); err != nil {
		t.Fatal(err)
	}
	if nonDeidentified != 0 {
		t.Fatalf("survivor pending results retained deleted seat outcome: %d", nonDeidentified)
	}
}

func TestActionExactReplayConflictAndRevocation(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeQuick, 100_000, 1500)
	record := fixture.sessionForUser(users[0])
	gestures := [3]string{GestureRock, GestureScissors, GestureScissors}
	key := 1520
	for seat := 0; seat < 2; seat++ {
		current, _ := fixture.maybeSession(record.ID)
		userID := *current.Seats[seat].UserID
		if _, err := fixture.service.Action(context.Background(), ActionInput{UserID: userID, SessionBinding: bindings[userID],
			SessionID: current.ID, PhaseSeq: current.PhaseSeq.Decimal(), ExpectedRevision: current.Revision.Decimal(),
			Action: "gesture", Gesture: gestures[seat], IdempotencyKey: fixture.key(key)}); err != nil {
			t.Fatal(err)
		}
		key++
	}
	current, _ := fixture.maybeSession(record.ID)
	lastUser := *current.Seats[2].UserID
	input := ActionInput{UserID: lastUser, SessionBinding: bindings[lastUser], SessionID: current.ID,
		PhaseSeq: current.PhaseSeq.Decimal(), ExpectedRevision: current.Revision.Decimal(), Action: "gesture",
		Gesture: gestures[2], IdempotencyKey: fixture.key(key)}
	first, err := fixture.service.Action(context.Background(), input)
	if err != nil || first.State.Kind != "pending_result" || first.IdempotentReplay {
		t.Fatalf("terminal action=(%+v,%v)", first, err)
	}
	replay, err := fixture.service.Action(context.Background(), input)
	if err != nil || !replay.IdempotentReplay {
		t.Fatalf("action replay=(%+v,%v)", replay, err)
	}
	firstJSON, _ := json.Marshal(first.State)
	replayJSON, _ := json.Marshal(replay.State)
	if string(firstJSON) != string(replayJSON) ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_terminal' AND source_id=?`, record.ID) != 1 {
		t.Fatalf("replay changed response or terminal count: first=%s replay=%s", firstJSON, replayJSON)
	}
	changed := input
	changed.Gesture = GesturePaper
	if _, err := fixture.service.Action(context.Background(), changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("same key changed action=%v", err)
	}
	if _, err := fixture.service.ACK(context.Background(), ACKInput{UserID: lastUser, SessionBinding: bindings[lastUser], SessionID: record.ID}); err != nil {
		t.Fatalf("ACK before replay invalidation=%v", err)
	}
	if _, err := fixture.service.Action(context.Background(), input); !errors.Is(err, ErrConflict) {
		t.Fatalf("ACKed private result replay=%v", err)
	}
	if _, err := fixture.database.Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, lastUser); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Action(context.Background(), input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked action replay=%v", err)
	}
}

func (fixture *rpsFixture) applyRankFact(userID int64, mode string, sign int, magnitude int64, terminalAt int64) string {
	fixture.t.Helper()
	sessionID := fixture.mustID("rps_")
	value, err := u128(big.NewInt(magnitude))
	if err != nil {
		fixture.t.Fatal(err)
	}
	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		fixture.t.Fatal(err)
	}
	defer tx.Rollback()
	if err := fixture.service.applyRankFactTx(context.Background(), tx, sessionID, userID, mode, terminalAt, sign, value); err != nil {
		fixture.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		fixture.t.Fatal(err)
	}
	return sessionID
}

func TestLeaderboardEligibilityPrivacyAndExactExpiry(t *testing.T) {
	fixture := newRPSFixture(t)
	publicUser, _ := fixture.seedUser("rank-public", 0)
	privateUser, _ := fixture.seedUser("rank-private", 0)
	if _, err := fixture.database.Exec(`INSERT INTO game_user_preferences(user_id,tutorial_rps_seen,game_profile_public,updated_at)
VALUES(?,0,1,?)`, publicUser, fixture.clock.Load()); err != nil {
		t.Fatal(err)
	}
	for sample := 0; sample < 10; sample++ {
		fixture.applyRankFact(publicUser, game.RPSModeQuick, 1, 1000, fixture.clock.Load())
		sign := -1
		if sample < 5 {
			sign = 1
		}
		fixture.applyRankFact(privateUser, game.RPSModeQuick, sign, 1000, fixture.clock.Load())
	}
	profit, err := fixture.service.Leaderboard(context.Background(), privateUser, game.RPSModeQuick, "profit_rate")
	if err != nil || len(profit.Rows) != 2 || profit.Me != nil {
		t.Fatalf("profit board=(%+v,%v)", profit, err)
	}
	if profit.Rows[0].Identity.Kind != "public" || profit.Rows[0].Identity.DisplayName != "rank-public" ||
		profit.Rows[0].ProfitRate != "100" || profit.Rows[0].SessionCount != "10" {
		t.Fatalf("public profit row=%+v", profit.Rows[0])
	}
	if profit.Rows[1].Identity.Kind != "anonymous" || !profit.Rows[1].IsMe || profit.Rows[1].ProfitRate != "50" {
		t.Fatalf("private profit row=%+v", profit.Rows[1])
	}
	net, err := fixture.service.Leaderboard(context.Background(), privateUser, game.RPSModeQuick, "net_profit")
	if err != nil || len(net.Rows) != 2 || net.Rows[0].NetProfit != "10" || net.Rows[1].NetProfit != "0" {
		t.Fatalf("net board=(%+v,%v)", net, err)
	}
	if _, err := fixture.database.Exec(`UPDATE users SET is_banned=1,banned_until=? WHERE id=?`, fixture.clock.Load()-1, publicUser); err != nil {
		t.Fatal(err)
	}
	expiredBan, err := fixture.service.Leaderboard(context.Background(), privateUser, game.RPSModeQuick, "profit_rate")
	if err != nil || len(expiredBan.Rows) != 2 {
		t.Fatalf("expired temporary ban remained excluded=(%+v,%v)", expiredBan, err)
	}
	if _, err := fixture.database.Exec(`UPDATE users SET banned_until=? WHERE id=?`, fixture.clock.Load()+1, publicUser); err != nil {
		t.Fatal(err)
	}
	activeBan, err := fixture.service.Leaderboard(context.Background(), privateUser, game.RPSModeQuick, "profit_rate")
	if err != nil || len(activeBan.Rows) != 1 || activeBan.Rows[0].Identity.Kind != "anonymous" {
		t.Fatalf("active temporary ban projection=(%+v,%v)", activeBan, err)
	}
	if _, err := fixture.database.Exec(`UPDATE users SET is_banned=0,banned_until=NULL WHERE id=?`, publicUser); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(rpsTestNow + summaryWindowSeconds - 1)
	before, err := fixture.service.Leaderboard(context.Background(), privateUser, game.RPSModeQuick, "profit_rate")
	if err != nil || len(before.Rows) != 2 || fixture.scalar(`SELECT COUNT(*) FROM game_rps_rank_facts`) != 20 {
		t.Fatalf("expiry -1 board=(%+v,%v) facts=%d", before, err, fixture.scalar(`SELECT COUNT(*) FROM game_rps_rank_facts`))
	}
	fixture.clock.Store(rpsTestNow + summaryWindowSeconds)
	after, err := fixture.service.Leaderboard(context.Background(), privateUser, game.RPSModeQuick, "profit_rate")
	if err != nil || len(after.Rows) != 0 || after.Me != nil || fixture.scalar(`SELECT COUNT(*) FROM game_rps_rank_facts`) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_rank_aggregates`) != 0 {
		t.Fatalf("expiry exact board=(%+v,%v)", after, err)
	}
}

func TestLeaderboardTopTwentyPlusMe(t *testing.T) {
	fixture := newRPSFixture(t)
	var requester int64
	for index := 0; index < 21; index++ {
		userID, _ := fixture.seedUser("rank-page-"+string(rune('a'+index)), 0)
		if index == 20 {
			requester = userID
		}
		for sample := 0; sample < 10; sample++ {
			fixture.applyRankFact(userID, game.RPSModeStandard, 1, int64(21-index)*1000, fixture.clock.Load())
		}
	}
	board, err := fixture.service.Leaderboard(context.Background(), requester, game.RPSModeStandard, "net_profit")
	if err != nil || len(board.Rows) != 20 || board.Me == nil || board.Me.Rank != "21" || !board.Me.IsMe || board.Me.NetProfit != "10" {
		t.Fatalf("top20+me=(%+v,%v)", board, err)
	}
	for _, row := range board.Rows {
		if row.IsMe || row.Identity.Kind != "anonymous" {
			t.Fatalf("unexpected top row=%+v", row)
		}
	}
}
