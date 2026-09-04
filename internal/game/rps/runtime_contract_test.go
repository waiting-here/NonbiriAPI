package rps

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestWireUsesCanonicalSignedWalletAndClosedActorOptions(t *testing.T) {
	negative := "-1.25"
	seatBody, err := json.Marshal(Seat{
		SeatNo: 0, Viewer: "self", DeletionState: "active", DisplayName: "player",
		StartingBalance: "5", CurrentBalance: "3.75", CurrentRoundInput: "0", TotalInput: "5",
		TotalReturned: "3.75", TimeoutCount: "0", FunSnapshot: FunSnapshot{State: "none"}, WalletNet: &negative,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(seatBody), `"wallet_net":"-1.25"`) ||
		strings.Contains(string(seatBody), `"sign"`) || strings.Contains(string(seatBody), `"magnitude"`) {
		t.Fatalf("seat wallet wire=%s", seatBody)
	}
	var seatRoundTrip Seat
	if err := json.Unmarshal(seatBody, &seatRoundTrip); err != nil {
		t.Fatalf("seat round trip body=%s err=%v", seatBody, err)
	}
	pendingBody, err := json.Marshal(PendingResult{OwnWalletNet: "-1.25"})
	if err != nil || !strings.Contains(string(pendingBody), `"own_wallet_net":"-1.25"`) {
		t.Fatalf("pending wallet wire=%s err=%v", pendingBody, err)
	}

	record := sessionRecord{State: StateStarted, Phase: PhaseGesture}
	record.Seats[0].DeletionState = "active"
	if got := actorOptions(record, 0); len(got) != 1 || got[0] != "gesture" {
		t.Fatalf("gesture options=%v", got)
	}
	dealer := 0
	record.Phase, record.DealerSeat = PhaseDealerRaise, &dealer
	if got := actorOptions(record, 0); len(got) != 1 || got[0] != "dealer_decision" {
		t.Fatalf("dealer options=%v", got)
	}
	record.Phase = PhaseFollowers
	if got := actorOptions(record, 1); len(got) != 0 {
		t.Fatalf("inactive seat options=%v", got)
	}
	record.Seats[1].DeletionState = "active"
	if got := actorOptions(record, 1); len(got) != 1 || got[0] != "follower_decision" {
		t.Fatalf("follower options=%v", got)
	}
}

func TestActionRateWindowExactBoundaryAndRollback(t *testing.T) {
	fixture := newRPSFixture(t)
	userID, _ := fixture.seedUser("action-window", rpsTestFunding)
	for index := 0; index < actionLimit; index++ {
		release, err := fixture.service.reserveAction(userID)
		if err != nil {
			t.Fatalf("reserve %d: %v", index, err)
		}
		release(true)
	}
	if _, err := fixture.service.reserveAction(userID); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("31st action=%v", err)
	}
	fixture.clock.Add(int64(actionWindowDuration / time.Second))
	release, err := fixture.service.reserveAction(userID)
	if err != nil {
		t.Fatalf("exact 60s boundary: %v", err)
	}
	release(false)
	for index := 0; index < actionLimit; index++ {
		release, err = fixture.service.reserveAction(userID)
		if err != nil {
			t.Fatalf("post-rollback reserve %d: %v", index, err)
		}
		release(true)
	}
	if _, err := fixture.service.reserveAction(userID); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("post-boundary 31st action=%v", err)
	}
}

func TestActiveSessionActionExactReplay(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeQuick, rpsTestFunding, 2180)
	record := fixture.sessionForUser(users[0])
	input := ActionInput{
		UserID: users[0], SessionBinding: bindings[users[0]], SessionID: record.ID,
		PhaseSeq: record.PhaseSeq.Decimal(), ExpectedRevision: record.Revision.Decimal(),
		Action: "gesture", Gesture: GestureRock, IdempotencyKey: fixture.key(2190),
	}
	first, err := fixture.service.Action(context.Background(), input)
	if err != nil || first.State.Kind != "session" || first.IdempotentReplay {
		t.Fatalf("first action=(%+v,%v)", first, err)
	}
	replayed, err := fixture.service.Action(context.Background(), input)
	if err != nil || replayed.State.Kind != "session" || !replayed.IdempotentReplay {
		t.Fatalf("replayed action=(%+v,%v)", replayed, err)
	}
	firstBody, firstErr := json.Marshal(first.State)
	replayBody, replayErr := json.Marshal(replayed.State)
	if firstErr != nil || replayErr != nil || string(firstBody) != string(replayBody) {
		t.Fatalf("replay body first=%s/%v replay=%s/%v", firstBody, firstErr, replayBody, replayErr)
	}
}

func TestHomeSummaryTxActiveThenPending(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeQuick, rpsTestFunding, 2210)
	record := fixture.sessionForUser(users[0])
	tx := fixture.mustReadTx()
	summary, err := fixture.service.HomeSummaryTx(context.Background(), tx, users[0])
	_ = tx.Rollback()
	if err != nil || len(summary.Continue) != 1 || len(summary.PendingResults) != 0 ||
		summary.Continue[0].ResourceID != record.ID || summary.Continue[0].State != StateStarted ||
		summary.Continue[0].Game != game.RPSID || summary.Continue[0].RouteID != "game-rps" {
		t.Fatalf("active summary=(%+v,%v)", summary, err)
	}
	key := 2230
	fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureScissors, GestureScissors}, &key)
	tx = fixture.mustReadTx()
	summary, err = fixture.service.HomeSummaryTx(context.Background(), tx, users[0])
	_ = tx.Rollback()
	if err != nil || len(summary.Continue) != 0 || len(summary.PendingResults) != 1 ||
		summary.PendingResults[0].ResourceID != record.ID || summary.PendingResults[0].CreatedAt != fixture.clock.Load() ||
		summary.PendingResults[0].Game != game.RPSID || summary.PendingResults[0].RouteID != "game-rps" {
		t.Fatalf("pending summary=(%+v,%v)", summary, err)
	}
}

func TestRecentEventRingAndIdentityReset(t *testing.T) {
	one := mustRPSU128(t, "1")
	record := sessionRecord{IdentityEpoch: one, PhaseSeq: one}
	deadline := rpsTestNow + 20
	for index := 0; index < 70; index++ {
		if err := appendEvent(&record, EventPhaseChanged, phaseChangedPayload{Phase: PhaseGesture, Deadline: &deadline}); err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
	}
	if len(record.RecentEvents) != maxRecentEvents || record.RecentFirstSeq.Decimal() != "7" ||
		record.RecentLastSeq.Decimal() != "70" || record.RecentEvents[0].Seq != "7" {
		t.Fatalf("ring len=%d first=%s last=%s head=%+v", len(record.RecentEvents),
			record.RecentFirstSeq.Decimal(), record.RecentLastSeq.Decimal(), record.RecentEvents[0])
	}
	if err := clearEventsForIdentity(&record, 2); err != nil {
		t.Fatal(err)
	}
	if record.IdentityEpoch.Decimal() != "2" || len(record.RecentEvents) != 1 ||
		record.RecentFirstSeq.Decimal() != "1" || record.RecentLastSeq.Decimal() != "1" ||
		record.RecentEvents[0].Kind != EventIdentityReset || record.RecentEvents[0].IdentityEpoch != "2" {
		t.Fatalf("identity reset record=%+v", record)
	}
}

func TestExpiredLeaseMarkerSurvivesActiveSessionAndShortensLaterPhase(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeStandard, rpsTestFunding, 2200)
	record := fixture.sessionForUser(users[0])
	leaseID := fixture.mustID("gle_")
	fixture.clock.Add(5)
	lease, err := fixture.service.RenewLease(context.Background(), LeaseInput{
		UserID: users[0], SessionBinding: bindings[users[0]], SessionID: record.ID, LeaseID: leaseID,
	})
	if err != nil || lease.ExpiresAt != rpsTestNow+20 {
		t.Fatalf("lease=%+v err=%v", lease, err)
	}
	fixture.clock.Store(rpsTestNow + 10)
	key := 2220
	fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureScissors, GestureScissors}, &key)
	fixture.dealerDecision(record.ID, bindings, "no_raise", "", &key)
	next, found := fixture.maybeSession(record.ID)
	if !found || next.Phase != PhaseGesture || next.PhaseDeadline == nil || *next.PhaseDeadline != rpsTestNow+30 {
		t.Fatalf("next phase=%+v found=%v", next, found)
	}
	fixture.clock.Store(rpsTestNow + 21)
	if deleted, err := fixture.service.pruneLeases(context.Background(), fixture.clock.Load()); err != nil || deleted != 0 {
		t.Fatalf("active marker prune=(%d,%v)", deleted, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_online_leases WHERE session_id=? AND user_id=?`, record.ID, users[0]) != 1 {
		t.Fatal("expired active-session disconnect marker was lost")
	}
	if err := fixture.service.shortenOneDisconnected(context.Background(), record.ID, fixture.clock.Load()); err != nil {
		t.Fatal(err)
	}
	shortened, found := fixture.maybeSession(record.ID)
	if !found || shortened.PhaseDeadline == nil || *shortened.PhaseDeadline != rpsTestNow+26 {
		t.Fatalf("shortened=%+v found=%v", shortened, found)
	}
}

func TestTerminalProcessingRenewsOnlyPreterminalLease(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeQuick, rpsTestFunding, 2250)
	record := fixture.sessionForUser(users[0])
	preterminalLease := fixture.mustID("gle_")
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{
		UserID: users[0], SessionBinding: bindings[users[0]], SessionID: record.ID, LeaseID: preterminalLease,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.service.beforeTerminalCommit = func() error { return errors.New("injected terminal retry") }
	key := 2270
	fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureScissors, GestureScissors}, &key)
	processing, found := fixture.maybeSession(record.ID)
	if !found || processing.State != StateTerminalProcessing {
		t.Fatalf("processing=%+v found=%v", processing, found)
	}
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{
		UserID: users[1], SessionBinding: bindings[users[1]], SessionID: record.ID, LeaseID: fixture.mustID("gle_"),
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("new terminal lease=%v", err)
	}
	fixture.clock.Add(1)
	lease, err := fixture.service.RenewLease(context.Background(), LeaseInput{
		UserID: users[0], SessionBinding: bindings[users[0]], SessionID: record.ID, LeaseID: preterminalLease,
	})
	if err != nil || lease.ExpiresAt != fixture.clock.Load()+leaseTTLSeconds {
		t.Fatalf("preterminal renewal=%+v err=%v", lease, err)
	}
}

func TestGestureKeyMismatchRollsBackAndRaisesSafeInvariantAlert(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeStandard, rpsTestFunding, 2280)
	record := fixture.sessionForUser(users[0])
	key := 2290
	fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureScissors, GestureScissors}, &key)
	locked, found := fixture.maybeSession(record.ID)
	if !found || locked.Phase != PhaseDealerRaise || locked.DealerSeat == nil {
		t.Fatalf("locked=%+v found=%v", locked, found)
	}
	fixture.service.keys.gesture[0] ^= 0x80
	dealerID := *locked.Seats[*locked.DealerSeat].UserID
	_, err := fixture.service.Action(context.Background(), ActionInput{
		UserID: dealerID, SessionBinding: bindings[dealerID], SessionID: record.ID,
		PhaseSeq: locked.PhaseSeq.Decimal(), ExpectedRevision: locked.Revision.Decimal(),
		Action: "dealer_decision", DealerDecision: "no_raise", IdempotencyKey: fixture.key(key),
	})
	fixture.service.keys.gesture[0] ^= 0x80
	if !errors.Is(err, ErrInvariant) {
		t.Fatalf("wrong gesture key=%v", err)
	}
	unchanged, found := fixture.maybeSession(record.ID)
	if !found || unchanged.Phase != locked.Phase || unchanged.Revision != locked.Revision ||
		unchanged.PhaseSeq != locked.PhaseSeq || unchanged.PlayerPool != locked.PlayerPool {
		t.Fatalf("failed reveal changed state: before=%+v after=%+v found=%v", locked, unchanged, found)
	}
	var message, ref string
	if err := fixture.database.QueryRow(`SELECT message,ref FROM admin_alerts
WHERE kind='invariant_violation' AND resolved=0`).Scan(&message, &ref); err != nil {
		t.Fatal(err)
	}
	if message != "RPS persisted state failed validation" || ref != record.ID || strings.Contains(message, "gesture") {
		t.Fatalf("unsafe invariant alert message=%q ref=%q", message, ref)
	}
}

func TestMatchCommitFailureRollsBackThenConverges(t *testing.T) {
	fixture := newRPSFixture(t)
	users := make([]int64, 3)
	for index := range users {
		users[index], _ = fixture.seedUser("match-rollback-"+string(rune('a'+index)), rpsTestFunding)
		fixture.enqueue(users[index], game.RPSModeQuick, byte(0xb0+index), byte(0xc0+index), 2330+index)
	}
	fixture.service.beforeMatchCommit = func() error { return errors.New("injected precommit loss") }
	if matched, err := fixture.service.MatchOnce(context.Background(), game.RPSModeQuick); matched || !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("failed match=(%v,%v)", matched, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_rps_queue`) != 3 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_user_slots WHERE queue_id IS NOT NULL`) != 3 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_sessions`) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_session_start'`) != 0 {
		t.Fatal("precommit failure left partial match state")
	}
	fixture.service.beforeMatchCommit = nil
	if matched, err := fixture.service.MatchOnce(context.Background(), game.RPSModeQuick); err != nil || !matched {
		t.Fatalf("retry match=(%v,%v)", matched, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_rps_queue`) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_sessions`) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_session_start'`) != 1 {
		t.Fatal("match retry did not converge exactly once")
	}
}

func TestOneMilliZeroPumpRoundHasNoCutPosting(t *testing.T) {
	fixture := newRPSFixture(t)
	fixture.setRPSConfig(true, 1, PumpsBP{})
	users, bindings := fixture.startThree(game.RPSModeQuick, 1, 2300)
	record := fixture.sessionForUser(users[0])
	key := 2320
	state := fixture.playGestures(record.ID, bindings, [3]string{GestureRock, GestureScissors, GestureScissors}, &key)
	if state.Kind != "pending_result" || state.Result == nil || state.Result.TerminalReason != TerminalQuickResolved {
		t.Fatalf("terminal state=%+v", state)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE source_id=? AND kind='rps_round_cut'`, record.ID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE source_id=? AND kind='rps_terminal'`, record.ID) != 1 {
		t.Fatal("zero-pump minimum round emitted cut or lost terminal posting")
	}
}

func TestMatchRecordsThreeParticipantActivityRounds(t *testing.T) {
	tests := []struct {
		name       string
		pumps      PumpsBP
		wantGlobal bool
	}{
		{name: "zero-pump", pumps: PumpsBP{}, wantGlobal: false},
		{name: "pool-transfer", pumps: PumpsBP{Platform: 100, Welfare: 100, Thursday: 100}, wantGlobal: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRPSFixture(t)
			fixture.setRPSConfig(true, 1_000, test.pumps)
			if _, err := fixture.database.Exec(`INSERT INTO site_config(key,value,updated_at) VALUES(?,'0',?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, db.SiteTimezoneKey, fixture.clock.Load()); err != nil {
				t.Fatal(err)
			}
			users, _ := fixture.startThree(game.RPSModeQuick, rpsTestFunding, 2400)
			if got := fixture.scalar(`SELECT COALESCE(SUM(game_rounds),0) FROM user_activity_daily`); got != 3 {
				t.Fatalf("user activity rounds=%d", got)
			}
			var raw []byte
			if err := fixture.database.QueryRow(`SELECT game_rounds FROM site_activity_daily`).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			rounds, err := db.DecodeU128(raw)
			if err != nil || rounds.Decimal() != "3" {
				t.Fatalf("site activity rounds=%s err=%v", rounds.Decimal(), err)
			}
			fixture.activity.mu.Lock()
			facts := append([]activities.PublishFacts(nil), fixture.activity.facts...)
			fixture.activity.mu.Unlock()
			if len(facts) != 1 || facts[0].Global != test.wantGlobal {
				t.Fatalf("published facts=%+v want global=%v", facts, test.wantGlobal)
			}
			seen := map[int64]int{}
			for _, userID := range facts[0].AccountIDs {
				seen[userID]++
			}
			if len(facts[0].AccountIDs) != 3 || len(seen) != 3 {
				t.Fatalf("published account ids=%v", facts[0].AccountIDs)
			}
			for _, userID := range users {
				if seen[userID] != 1 {
					t.Fatalf("user %d publish count=%d facts=%+v", userID, seen[userID], facts[0])
				}
			}
		})
	}
}
