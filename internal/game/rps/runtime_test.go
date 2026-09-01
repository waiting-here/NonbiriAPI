package rps

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestQuickMatchRevealTerminalAndACK(t *testing.T) {
	fixture := newRPSFixture(t)
	users := make([]int64, 3)
	bindings := map[int64]string{}
	for index := range users {
		userID, binding := fixture.seedUser("quick-"+string(rune('a'+index)), rpsTestFunding)
		users[index], bindings[userID] = userID, binding
		fixture.enqueue(users[index], game.RPSModeQuick, byte(index+1), byte(index+1), index+1)
	}
	matched, err := fixture.service.MatchOnce(context.Background(), game.RPSModeQuick)
	if err != nil || !matched {
		t.Fatalf("match=(%v,%v)", matched, err)
	}
	record := fixture.sessionForUser(users[0])
	if record.Phase != PhaseGesture || record.Mode != game.RPSModeQuick {
		t.Fatalf("initial phase=%s mode=%s", record.Phase, record.Mode)
	}
	gestureBySeat := [3]string{GestureRock, GestureScissors, GestureScissors}
	var winner int64
	for seat := range record.Seats {
		if seat == 0 {
			winner = *record.Seats[seat].UserID
		}
		userID := *record.Seats[seat].UserID
		current := fixture.sessionForUser(userID)
		result, err := fixture.service.Action(context.Background(), ActionInput{UserID: userID,
			SessionBinding: bindings[userID], SessionID: current.ID, PhaseSeq: current.PhaseSeq.Decimal(),
			ExpectedRevision: current.Revision.Decimal(), Action: "gesture", Gesture: gestureBySeat[seat],
			IdempotencyKey: fixture.key(100 + seat)})
		if err != nil {
			t.Fatalf("seat %d action: %v", seat, err)
		}
		if seat < 2 {
			wire, err := json.Marshal(result.State)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(wire), `"visible_gesture"`) || strings.Contains(string(wire), `"gesture":"`+gestureBySeat[seat]+`"`) {
				t.Fatalf("unrevealed gesture leaked: %s", wire)
			}
		} else if result.State.Kind != "pending_result" {
			t.Fatalf("terminal home kind=%q", result.State.Kind)
		}
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM game_rps_sessions`); got != 0 {
		t.Fatalf("sessions=%d", got)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM game_rps_pending_results`); got != 3 {
		t.Fatalf("pending=%d", got)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_round_cut'`); got != 1 {
		t.Fatalf("round cuts=%d", got)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_terminal'`); got != 1 {
		t.Fatalf("terminal operations=%d", got)
	}
	for _, userID := range users {
		want := int64(99_485)
		if userID == winner {
			want = 100_940
		}
		if got := fixture.balance(userID); got.Cmp(big.NewInt(want)) != 0 {
			t.Fatalf("user %d balance=%s want=%d", userID, got, want)
		}
	}
	tx := fixture.mustReadTx()
	pending, _, err := loadPending(context.Background(), tx, users[0])
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.ACK(context.Background(), ACKInput{UserID: users[0], SessionBinding: bindings[users[0]], SessionID: pending.SessionID})
	if err != nil || result.HTTPStatus != 204 {
		t.Fatalf("ack=(%+v,%v)", result, err)
	}
	if _, err := fixture.service.ACK(context.Background(), ACKInput{UserID: users[0], SessionBinding: bindings[users[0]], SessionID: pending.SessionID}); err != nil {
		t.Fatalf("ack replay: %v", err)
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM game_rps_pending_results WHERE user_id=?`, users[0]); got != 0 {
		t.Fatalf("pending after ack=%d", got)
	}
}

func TestStandardRetainsGesturePairAcrossDealerAndFollowerPhases(t *testing.T) {
	fixture := newRPSFixture(t)
	users, bindings := fixture.startThree(game.RPSModeStandard, 10_000, 20)
	record := fixture.sessionForUser(users[0])
	gestures := [3]string{GestureRock, GestureScissors, GesturePaper}
	for seat := range record.Seats {
		userID := *record.Seats[seat].UserID
		current := fixture.sessionForUser(userID)
		if _, err := fixture.service.Action(context.Background(), ActionInput{UserID: userID, SessionBinding: bindings[userID],
			SessionID: current.ID, PhaseSeq: current.PhaseSeq.Decimal(), ExpectedRevision: current.Revision.Decimal(),
			Action: "gesture", Gesture: gestures[seat], IdempotencyKey: fixture.key(200 + seat)}); err != nil {
			t.Fatal(err)
		}
	}
	record = fixture.sessionForUser(users[0])
	if record.Phase != PhaseDealerRaise {
		t.Fatalf("phase=%s", record.Phase)
	}
	for seat := range record.Seats {
		if record.Seats[seat].GestureEnvelope == nil || record.Seats[seat].GesturePhaseSeq == nil || record.Seats[seat].LastActionPhaseSeq != nil {
			t.Fatalf("seat %d retained pair matrix invalid", seat)
		}
		if record.Seats[seat].GesturePhaseSeq.Decimal() != "1" {
			t.Fatalf("gesture phase seq=%s", record.Seats[seat].GesturePhaseSeq.Decimal())
		}
	}
	dealer := *record.DealerSeat
	dealerUser := *record.Seats[dealer].UserID
	if _, err := fixture.service.Action(context.Background(), ActionInput{UserID: dealerUser, SessionBinding: bindings[dealerUser],
		SessionID: record.ID, PhaseSeq: record.PhaseSeq.Decimal(), ExpectedRevision: record.Revision.Decimal(),
		Action: "dealer_decision", DealerDecision: "raise", RaiseAmount: "1", IdempotencyKey: fixture.key(210)}); err != nil {
		t.Fatal(err)
	}
	record = fixture.sessionForUser(users[0])
	if record.Phase != PhaseFollowers {
		t.Fatalf("phase=%s", record.Phase)
	}
	followerIndex := 0
	for seat := range record.Seats {
		if seat == dealer {
			continue
		}
		userID := *record.Seats[seat].UserID
		decision := FollowerCall
		if followerIndex == 1 {
			decision = FollowerSurrender
		}
		current := fixture.sessionForUser(userID)
		if _, err := fixture.service.Action(context.Background(), ActionInput{UserID: userID, SessionBinding: bindings[userID],
			SessionID: current.ID, PhaseSeq: current.PhaseSeq.Decimal(), ExpectedRevision: current.Revision.Decimal(),
			Action: "follower_decision", FollowerDecision: decision, IdempotencyKey: fixture.key(211 + followerIndex)}); err != nil {
			t.Fatal(err)
		}
		followerIndex++
	}
	if got := fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_round_cut'`); got != 4 {
		t.Fatalf("round cuts=%d want=4", got)
	}
}

func TestDeathmatchAllInStartsAtUltimatePhase(t *testing.T) {
	fixture := newRPSFixture(t)
	users, _ := fixture.startThree(game.RPSModeDeathmatch, 1_000, 40)
	record := fixture.sessionForUser(users[0])
	if record.Phase != PhaseUltimateGesture || record.DealerSeat != nil || record.PhaseSeq.Decimal() != "1" {
		t.Fatalf("phase=%s dealer=%v phase_seq=%s", record.Phase, record.DealerSeat, record.PhaseSeq.Decimal())
	}
	if len(record.RecentEvents) != 1 || record.RecentEvents[0].Kind != EventPhaseChanged {
		t.Fatalf("events=%+v", record.RecentEvents)
	}
}

func TestRecoveryRestoresFullPhaseWithoutDefaults(t *testing.T) {
	fixture := newRPSFixture(t)
	users, _ := fixture.startThree(game.RPSModeQuick, 10_000, 60)
	before := fixture.sessionForUser(users[0])
	fixture.clock.Store(*before.PhaseDeadline + 100)
	if err := fixture.service.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := fixture.newService(8)
	fixture.service = restarted
	if err := restarted.RecoverBeforeListen(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := fixture.sessionForUser(users[0])
	if after.Phase != before.Phase || after.PhaseSeq != before.PhaseSeq || after.Seats[0].TimeoutCount.Big().Sign() != 0 {
		t.Fatalf("recovery mutated gameplay state")
	}
	want := fixture.clock.Load() + int64(after.GestureSeconds)
	if after.PhaseDeadline == nil || *after.PhaseDeadline != want || after.HealthEpoch != 8 {
		t.Fatalf("deadline=%v health=%d want deadline=%d", after.PhaseDeadline, after.HealthEpoch, want)
	}
}

func TestDeadlineDefaultsQuickAndLifecycleDeidentifiesSubmittedSeat(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		fixture := newRPSFixture(t)
		users, _ := fixture.startThree(game.RPSModeQuick, 10_000, 80)
		record := fixture.sessionForUser(users[0])
		fixture.clock.Store(*record.PhaseDeadline)
		processed, err := fixture.service.runDeadlineOne(context.Background(), record.ID, fixture.clock.Load())
		if err != nil || !processed {
			t.Fatalf("deadline=(%v,%v)", processed, err)
		}
		if fixture.scalar(`SELECT COUNT(*) FROM game_rps_pending_results`) != 3 {
			t.Fatalf("deadline did not terminalize quick game")
		}
		var total []byte
		if err := fixture.database.QueryRow(`SELECT total_timeout_count FROM game_rps_summaries WHERE session_id=?`, record.ID).Scan(&total); err != nil {
			t.Fatal(err)
		}
		decoded, err := db.DecodeU128(total)
		if err != nil || decoded.Decimal() != "3" {
			t.Fatalf("timeouts=%v %v", decoded.Decimal(), err)
		}
	})
	t.Run("delete-submitted", func(t *testing.T) {
		fixture := newRPSFixture(t)
		users, bindings := fixture.startThree(game.RPSModeStandard, 10_000, 100)
		record := fixture.sessionForUser(users[0])
		seat, ok := seatForUser(&record, users[0])
		if !ok {
			t.Fatal("owner seat missing")
		}
		if _, err := fixture.service.Action(context.Background(), ActionInput{UserID: users[0], SessionBinding: bindings[users[0]],
			SessionID: record.ID, PhaseSeq: record.PhaseSeq.Decimal(), ExpectedRevision: record.Revision.Decimal(),
			Action: "gesture", Gesture: GestureRock, IdempotencyKey: fixture.key(120)}); err != nil {
			t.Fatal(err)
		}
		record = fixture.sessionForUser(users[1])
		survivorReplay := ActionInput{UserID: users[1], SessionBinding: bindings[users[1]],
			SessionID: record.ID, PhaseSeq: record.PhaseSeq.Decimal(), ExpectedRevision: record.Revision.Decimal(),
			Action: "gesture", Gesture: GestureScissors, IdempotencyKey: fixture.key(122)}
		if _, err := fixture.service.Action(context.Background(), survivorReplay); err != nil {
			t.Fatal(err)
		}
		record = fixture.sessionForUser(users[0])
		oldEpoch := record.IdentityEpoch
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
			t.Fatal("finalizer commit")
		}
		record = fixture.sessionByID(record.ID)
		deleted := record.Seats[seat]
		if deleted.UserID != nil || deleted.DeletionState != "deletion_pending" || deleted.DisplayName != nil ||
			deleted.GestureEnvelope == nil || deleted.GesturePhaseSeq == nil {
			t.Fatalf("deleted seat=%+v", deleted)
		}
		if record.IdentityEpoch.Big().Cmp(oldEpoch.Big()) <= 0 || len(record.RecentEvents) != 1 || record.RecentEvents[0].Kind != EventIdentityReset {
			t.Fatalf("identity reset missing")
		}
		if _, err := fixture.service.Action(context.Background(), survivorReplay); !errors.Is(err, ErrConflict) {
			t.Fatalf("old-epoch survivor replay=%v", err)
		}
		if _, err := fixture.service.Action(context.Background(), ActionInput{UserID: users[0], SessionBinding: bindings[users[0]],
			SessionID: record.ID, PhaseSeq: record.PhaseSeq.Decimal(), ExpectedRevision: record.Revision.Decimal(),
			Action: "gesture", Gesture: GesturePaper, IdempotencyKey: fixture.key(121)}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("late deleted action=%v", err)
		}
	})
}

func (fixture *rpsFixture) startThree(mode string, funding int64, keyBase int) ([]int64, map[int64]string) {
	fixture.t.Helper()
	users := make([]int64, 3)
	bindings := map[int64]string{}
	for index := range users {
		userID, binding := fixture.seedUser(mode+"-"+string(rune('a'+index))+"-"+string(rune('a'+keyBase%20)), funding)
		users[index], bindings[userID] = userID, binding
		fixture.enqueue(userID, mode, byte(index+1+keyBase), byte(index+1+keyBase), keyBase+index)
	}
	matched, err := fixture.service.MatchOnce(context.Background(), mode)
	if err != nil || !matched {
		fixture.t.Fatalf("match=(%v,%v)", matched, err)
	}
	return users, bindings
}

func (fixture *rpsFixture) sessionForUser(userID int64) sessionRecord {
	fixture.t.Helper()
	tx := fixture.mustReadTx()
	defer tx.Rollback()
	record, found, err := loadSessionByUser(context.Background(), tx, userID)
	if err != nil || !found {
		fixture.t.Fatalf("load session user=%d found=%v err=%v", userID, found, err)
	}
	return record
}

func (fixture *rpsFixture) sessionByID(sessionID string) sessionRecord {
	fixture.t.Helper()
	tx := fixture.mustReadTx()
	defer tx.Rollback()
	record, found, err := loadSessionByID(context.Background(), tx, sessionID)
	if err != nil || !found {
		fixture.t.Fatalf("load session id=%s found=%v err=%v", sessionID, found, err)
	}
	return record
}

func (fixture *rpsFixture) mustReadTx() *sql.Tx {
	fixture.t.Helper()
	tx, err := fixture.database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fixture.t.Fatal(err)
	}
	return tx
}
