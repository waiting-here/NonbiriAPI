package linklink

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func TestStartChargesOnceAndResumesAcrossSpecifications(t *testing.T) {
	fixture := newFixture(t)
	userID, _ := fixture.seedUser("start", testFunding)
	before := fixture.balance(userID)
	started, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(1)})
	if err != nil {
		t.Fatal(err)
	}
	if started.HTTPStatus != 201 || started.State == nil || started.State.Spec != game.LinkLinkSpec6x8 || started.State.Revision != "1" || started.State.Price != "1" || started.State.Deadline-started.State.StartedAt != 150 {
		t.Fatalf("start = %+v", started)
	}
	if before != "1000000" || fixture.balance(userID) != "999000" {
		t.Fatalf("balance %s -> %s", before, fixture.balance(userID))
	}
	if fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='linklink_entry' AND source_id=?`, started.State.SessionID) != 1 {
		t.Fatal("missing unique LinkLink entry operation")
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE user_id=?`, userID) != 1 {
		t.Fatal("active session count is not one")
	}

	replayed, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(1)})
	if err != nil || !replayed.IdempotentReplay || replayed.HTTPStatus != 201 || !reflect.DeepEqual(replayed.State, started.State) {
		t.Fatalf("exact replay = (%+v,%v)", replayed, err)
	}
	resumed, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec10x10, IdempotencyKey: fixture.key(2)})
	if err != nil || resumed.HTTPStatus != 200 || resumed.State == nil || resumed.State.SessionID != started.State.SessionID || resumed.State.Spec != game.LinkLinkSpec6x8 {
		t.Fatalf("cross-spec resume = (%+v,%v)", resumed, err)
	}
	if fixture.balance(userID) != "999000" || fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='linklink_entry' AND actor_user_id=?`, userID) != 1 {
		t.Fatal("resume or replay charged again")
	}
	if _, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec8x8, IdempotencyKey: fixture.key(1)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("same key with different digest = %v", err)
	}
}

func TestStartGatesAndGenerationFailureAreWriteFree(t *testing.T) {
	tests := []struct {
		name    string
		master  bool
		link    bool
		spec    bool
		funds   int64
		failRNG bool
		want    error
	}{
		{name: "master disabled", funds: testFunding, want: ErrFeatureDisabled},
		{name: "game disabled", master: true, funds: testFunding, want: ErrFeatureDisabled},
		{name: "spec disabled", master: true, link: true, funds: testFunding, want: ErrFeatureDisabled},
		{name: "insufficient balance", master: true, link: true, spec: true, funds: 999, want: ErrInsufficientCredits},
		{name: "random failure", master: true, link: true, spec: true, funds: testFunding, failRNG: true, want: ErrServiceUnavailable},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.setConfig(test.master, test.link, map[string]bool{"6x8": test.spec}, 1000)
			userID, _ := fixture.seedUser(test.name, test.funds)
			before := fixture.balance(userID)
			if test.failRNG {
				fixture.random.failNext()
			}
			_, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(10 + index)})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if fixture.balance(userID) != before || fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE user_id=?`, userID) != 0 ||
				fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='linklink_entry' AND actor_user_id=?`, userID) != 0 ||
				fixture.scalar(`SELECT COUNT(*) FROM idempotency_records WHERE scope='game_linklink'`) != 0 {
				t.Fatal("rejected start changed authority or accounting")
			}
		})
	}
}

func TestExpiredActiveMaterializesBeforeFreshCrossSpecCharge(t *testing.T) {
	fixture := newFixture(t)
	userID, _ := fixture.seedUser("start-after-timeout", testFunding)
	first, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(15)})
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(first.State.Deadline)
	second, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec8x8, IdempotencyKey: fixture.key(16)})
	if err != nil || second.State == nil || second.HTTPStatus != 201 || second.State.Spec != game.LinkLinkSpec8x8 || second.State.SessionID == first.State.SessionID {
		t.Fatalf("fresh start after timeout = (%+v,%v)", second, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=? AND terminal_reason='timed_out'`, first.State.SessionID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE user_id=? AND id=?`, userID, second.State.SessionID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='linklink_entry' AND actor_user_id=?`, userID) != 2 ||
		fixture.scalar(`SELECT COUNT(*) FROM idempotency_records WHERE scope='game_linklink'`) != 2 || fixture.balance(userID) != "998000" {
		t.Fatal("timeout-to-new-start transition did not settle then charge exactly once")
	}
}

func TestTimedOutStartRetainsExactLostResponseReplay(t *testing.T) {
	fixture := newFixture(t)
	userID, _ := fixture.seedUser("lost-start-timeout", testFunding)
	input := StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(17)}
	started, err := fixture.service.Start(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(started.State.Deadline)
	replayed, err := fixture.service.Start(context.Background(), input)
	if err != nil || !replayed.IdempotentReplay || replayed.HTTPStatus != 201 || !reflect.DeepEqual(replayed.State, started.State) {
		t.Fatalf("lost start replay after timeout = (%+v,%v)", replayed, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE user_id=?`, userID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=? AND terminal_reason='timed_out'`, started.State.SessionID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='linklink_entry' AND actor_user_id=?`, userID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM idempotency_records WHERE scope='game_linklink'`) != 1 || fixture.balance(userID) != "999000" {
		t.Fatal("timeout replay created a session, charged twice, or rolled back terminal cleanup")
	}
}

func TestMatchRevisionOwnershipAndIdempotency(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("match", testFunding)
	otherID, otherBinding := fixture.seedUser("other", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(20)})
	if err != nil {
		t.Fatal(err)
	}
	first, second := fixture.firstLegalPair(started.State.SessionID)
	input := MatchInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, ExpectedRevision: "1",
		First: first, Second: second, IdempotencyKey: fixture.key(21),
	}
	matched, err := fixture.service.Match(context.Background(), input)
	if err != nil || matched.State == nil || matched.State.Revision != "2" || matched.State.PairsRemoved != 1 {
		t.Fatalf("match = (%+v,%v)", matched, err)
	}
	replay, err := fixture.service.Match(context.Background(), input)
	if err != nil || !replay.IdempotentReplay || !reflect.DeepEqual(replay.State, matched.State) {
		t.Fatalf("match replay = (%+v,%v)", replay, err)
	}
	if _, err := fixture.database.Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Match(context.Background(), input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked replay = %v", err)
	}
	if _, err := fixture.database.Exec(`UPDATE users SET is_banned=0 WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	changedDigest := input
	changedDigest.Second = Coordinate{Row: 0, Col: 3}
	if _, err := fixture.service.Match(context.Background(), changedDigest); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed digest = %v", err)
	}
	stale := input
	stale.IdempotencyKey = fixture.key(22)
	if _, err := fixture.service.Match(context.Background(), stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision = %v", err)
	}
	foreign := input
	foreign.UserID, foreign.SessionBinding, foreign.IdempotencyKey = otherID, otherBinding, fixture.key(23)
	if _, err := fixture.service.Match(context.Background(), foreign); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign owner = %v", err)
	}
	if fixture.scalar(`SELECT pairs_removed FROM game_linklink_sessions WHERE id=?`, started.State.SessionID) != 1 {
		t.Fatal("replay/conflicts changed pairs")
	}
}

func TestCompletedTerminalIsAtomicAndPreservesAllExactReplays(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("complete", testFunding)
	startInput := StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(30)}
	started, err := fixture.service.Start(context.Background(), startInput)
	if err != nil {
		t.Fatal(err)
	}
	leaseID := fixture.mustID("gle_")
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: leaseID}); err != nil {
		t.Fatal(err)
	}
	earlyFirst, earlySecond := fixture.firstLegalPair(started.State.SessionID)
	earlyInput := MatchInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, ExpectedRevision: "1",
		First: earlyFirst, Second: earlySecond, IdempotencyKey: fixture.key(31),
	}
	early, err := fixture.service.Match(context.Background(), earlyInput)
	if err != nil || early.State == nil || early.State.Revision != "2" {
		t.Fatalf("early match = (%+v,%v)", early, err)
	}
	definition, _ := resolveSpec(game.LinkLinkSpec6x8)
	nearComplete := validBoardWithActive(definition, map[Coordinate]byte{{Row: 0, Col: 0}: 1, {Row: 0, Col: 1}: 1})
	revision := fixture.replaceBoard(started.State.SessionID, nearComplete, definition.totalPairs()-1)
	finalInput := MatchInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, ExpectedRevision: revision.Decimal(),
		First: Coordinate{Row: 0, Col: 0}, Second: Coordinate{Row: 0, Col: 1}, IdempotencyKey: fixture.key(32),
	}
	completed, err := fixture.service.Match(context.Background(), finalInput)
	if err != nil || completed.Summary == nil || completed.Summary.TerminalReason != TerminalCompleted || completed.Summary.PairsRemoved != 24 || completed.Summary.Score == nil || *completed.Summary.Score != "2550" {
		t.Fatalf("completed = (%+v,%v)", completed, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=?`, started.State.SessionID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_online_leases WHERE session_id=?`, started.State.SessionID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, started.State.SessionID) != 1 {
		t.Fatal("terminal did not physically replace active board and lease with one summary")
	}
	if fixture.scalar(`SELECT COUNT(*) FROM idempotency_records WHERE scope='game_linklink'`) != 3 {
		t.Fatal("terminal did not retain start and all successful match replays")
	}
	finalKeyHash, err := idempotency.KeyHash(finalInput.IdempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	var replayBody []byte
	if err := fixture.database.QueryRow(`SELECT response_body FROM idempotency_records WHERE scope='game_linklink' AND key_hash=?`, finalKeyHash[:]).Scan(&replayBody); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(replayBody), "board") || !strings.Contains(string(replayBody), TerminalCompleted) {
		t.Fatalf("terminal replay body retained board or lost summary: %s", replayBody)
	}
	finalReplay, err := fixture.service.Match(context.Background(), finalInput)
	if err != nil || finalReplay.Summary == nil || !finalReplay.IdempotentReplay || finalReplay.HTTPStatus != completed.HTTPStatus || !reflect.DeepEqual(finalReplay.Summary, completed.Summary) {
		t.Fatalf("terminal match replay = (%+v,%v)", finalReplay, err)
	}
	earlyReplay, err := fixture.service.Match(context.Background(), earlyInput)
	if err != nil || earlyReplay.State == nil || !earlyReplay.IdempotentReplay || earlyReplay.HTTPStatus != early.HTTPStatus || !reflect.DeepEqual(earlyReplay.State, early.State) {
		t.Fatalf("early match replay = (%+v,%v)", earlyReplay, err)
	}
	startReplay, err := fixture.service.Start(context.Background(), startInput)
	if err != nil || startReplay.State == nil || !startReplay.IdempotentReplay || startReplay.HTTPStatus != started.HTTPStatus || !reflect.DeepEqual(startReplay.State, started.State) {
		t.Fatalf("start replay after completion = (%+v,%v)", startReplay, err)
	}
	changedEarly := earlyInput
	changedEarly.ExpectedRevision = "2"
	if _, err := fixture.service.Match(context.Background(), changedEarly); !errors.Is(err, ErrConflict) {
		t.Fatalf("earlier match key accepted a different body: %v", err)
	}
	changedFinal := finalInput
	changedFinal.ExpectedRevision = "23"
	if _, err := fixture.service.Match(context.Background(), changedFinal); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal match key accepted a different body: %v", err)
	}
	changedStart := startInput
	changedStart.Spec = game.LinkLinkSpec8x8
	if _, err := fixture.service.Start(context.Background(), changedStart); !errors.Is(err, ErrConflict) {
		t.Fatalf("start key accepted a different body: %v", err)
	}
	current, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
	if err != nil || current.State != nil || !reflect.DeepEqual(current.Summary, completed.Summary) {
		t.Fatalf("post-terminal current = (%+v,%v)", current, err)
	}
	if fixture.balance(userID) != "999000" {
		t.Fatalf("completion unexpectedly rewarded/refunded: %s", fixture.balance(userID))
	}
	fresh, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec8x8, IdempotencyKey: fixture.key(33)})
	if err != nil || fresh.State == nil || fresh.HTTPStatus != 201 || fresh.State.SessionID == started.State.SessionID ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=? AND user_id=?`, fresh.State.SessionID, userID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='linklink_entry' AND actor_user_id=?`, userID) != 2 ||
		fixture.scalar(`SELECT COUNT(*) FROM idempotency_records WHERE scope='game_linklink'`) != 4 || fixture.balance(userID) != "998000" {
		t.Fatalf("new intent after completion = (%+v,%v), balance=%s", fresh, err, fixture.balance(userID))
	}
}

func TestDeadBoardReshuffleFailureRollsBackWholeMatch(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("reshuffle", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(40)})
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := resolveSpec(game.LinkLinkSpec6x8)
	deadAfter := validBoardWithActive(definition, map[Coordinate]byte{
		{Row: 0, Col: 0}: 1, {Row: 0, Col: 1}: 2, {Row: 1, Col: 0}: 2, {Row: 1, Col: 1}: 1,
		{Row: 5, Col: 0}: 3, {Row: 5, Col: 1}: 3,
	})
	revision := fixture.replaceBoard(started.State.SessionID, deadAfter, definition.totalPairs()-3)
	before := fixture.sessionBytes(started.State.SessionID)
	fixture.random.setNoSwap(true)
	beforeCalls := fixture.random.callCount()
	input := MatchInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, ExpectedRevision: revision.Decimal(),
		First: Coordinate{Row: 5, Col: 0}, Second: Coordinate{Row: 5, Col: 1}, IdempotencyKey: fixture.key(41),
	}
	if _, err := fixture.service.Match(context.Background(), input); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("failed reshuffle = %v", err)
	}
	if fixture.random.callCount()-beforeCalls != maxReshuffleAttempts*3 || !reflect.DeepEqual(fixture.sessionBytes(started.State.SessionID), before) {
		t.Fatal("bounded reshuffle failure changed board, removed bits, pairs, or revision")
	}
	if fixture.scalar(`SELECT COUNT(*) FROM idempotency_records WHERE scope='game_linklink'`) != 1 {
		t.Fatal("failed action left accepted idempotency material")
	}
	fixture.random.setNoSwap(false)
	input.IdempotencyKey = fixture.key(42)
	matched, err := fixture.service.Match(context.Background(), input)
	expectedNext, nextErr := nextRevision(revision)
	if err != nil || nextErr != nil || matched.State == nil || matched.State.Revision != expectedNext.Decimal() {
		t.Fatalf("retry after reshuffle failure = (%+v,%v)", matched, err)
	}
	stored := fixture.loadBoard(started.State.SessionID)
	if !stored.hasMove() || !stored.solvable() || stored.activeCount() != 4 {
		t.Fatal("successful free reshuffle was not continueable/solvable")
	}
}

func TestConcurrentStartsAndMatchesCannotDoubleChargeOrDoubleRemove(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("concurrent", testFunding)
	var wait sync.WaitGroup
	startErrors := make(chan error, 2)
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, err := fixture.service.Start(context.Background(), StartInput{UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(50 + index)})
			startErrors <- err
		}(index)
	}
	wait.Wait()
	close(startErrors)
	for err := range startErrors {
		if err != nil && !errors.Is(err, ErrServiceUnavailable) {
			t.Fatalf("concurrent start error = %v", err)
		}
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE user_id=?`, userID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='linklink_entry' AND actor_user_id=?`, userID) != 1 || fixture.balance(userID) != "999000" {
		t.Fatal("concurrent starts double-created or double-charged")
	}
	current, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
	if err != nil || current.State == nil || current.Summary != nil {
		t.Fatal(err)
	}
	state := current.State
	first, second := fixture.firstLegalPair(state.SessionID)
	matchErrors := make(chan error, 2)
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, err := fixture.service.Match(context.Background(), MatchInput{
				UserID: userID, SessionBinding: binding, SessionID: state.SessionID, ExpectedRevision: state.Revision,
				First: first, Second: second, IdempotencyKey: fixture.key(60 + index),
			})
			matchErrors <- err
		}(index)
	}
	wait.Wait()
	close(matchErrors)
	successes := 0
	for err := range matchErrors {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrServiceUnavailable) {
			t.Fatalf("concurrent match error = %v", err)
		}
	}
	if successes != 1 || fixture.scalar(`SELECT pairs_removed FROM game_linklink_sessions WHERE id=?`, state.SessionID) != 1 {
		t.Fatalf("concurrent matches successes=%d", successes)
	}
}

func TestServiceRequiresAndDoesNotOwnSharedStartLimiter(t *testing.T) {
	fixture := newFixture(t)
	if _, err := New(Options{Store: fixture.store, UserAuthorizer: fixture.authorizer, Continuation: fixture.continuation, HealthEpoch: 8}); err == nil {
		t.Fatal("service accepted a missing shared limiter")
	}
	if err := fixture.service.Close(); err != nil {
		t.Fatal(err)
	}
	reservation, _, err := fixture.limiter.Reserve(99999)
	if err != nil {
		t.Fatalf("closing LinkLink service closed shared limiter: %v", err)
	}
	reservation.Release()
}

type sessionSnapshot struct {
	Revision []byte
	Board    []byte
	Removed  []byte
	Pairs    int
}

func (fixture *fixture) replaceBoard(sessionID string, value board, pairsRemoved int) db.U128 {
	fixture.t.Helper()
	if err := value.validate(); err != nil {
		fixture.t.Fatal(err)
	}
	revision, err := db.U128FromBig(big.NewInt(int64(pairsRemoved) + 1))
	if err != nil {
		fixture.t.Fatal(err)
	}
	if _, err := fixture.database.Exec(`UPDATE game_linklink_sessions SET revision=?,board_blob=?,removed_bits=?,pairs_removed=? WHERE id=?`, db.EncodeU128(revision), value.tiles, value.removed, pairsRemoved, sessionID); err != nil {
		fixture.t.Fatal(err)
	}
	return revision
}

func (fixture *fixture) sessionBytes(sessionID string) sessionSnapshot {
	fixture.t.Helper()
	var result sessionSnapshot
	if err := fixture.database.QueryRow(`SELECT revision,board_blob,removed_bits,pairs_removed FROM game_linklink_sessions WHERE id=?`, sessionID).Scan(&result.Revision, &result.Board, &result.Removed, &result.Pairs); err != nil {
		fixture.t.Fatal(err)
	}
	return result
}

func (fixture *fixture) loadBoard(sessionID string) board {
	fixture.t.Helper()
	var spec string
	var blob, removed []byte
	if err := fixture.database.QueryRow(`SELECT spec,board_blob,removed_bits FROM game_linklink_sessions WHERE id=?`, sessionID).Scan(&spec, &blob, &removed); err != nil {
		fixture.t.Fatal(err)
	}
	value, err := decodeBoard(spec, blob, removed)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return value
}

func (fixture *fixture) firstLegalPair(sessionID string) (Coordinate, Coordinate) {
	fixture.t.Helper()
	value := fixture.loadBoard(sessionID)
	pairs := value.legalPairs()
	if len(pairs) == 0 {
		fixture.t.Fatal("session board has no legal pair")
	}
	return Coordinate{Row: pairs[0][0] / value.definition.Cols, Col: pairs[0][0] % value.definition.Cols},
		Coordinate{Row: pairs[0][1] / value.definition.Cols, Col: pairs[0][1] % value.definition.Cols}
}

func assertJSONEqual(t *testing.T, left, right any) {
	t.Helper()
	leftBody, _ := json.Marshal(left)
	rightBody, _ := json.Marshal(right)
	if string(leftBody) != string(rightBody) {
		t.Fatalf("JSON differs:\n%s\n%s", leftBody, rightBody)
	}
}
