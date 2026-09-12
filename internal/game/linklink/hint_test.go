package linklink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (fixture *fixture) startCurrent(user int64, binding, spec string, key int) State {
	fixture.t.Helper()
	result, err := fixture.service.Start(context.Background(), StartInput{UserID: user, Spec: spec, IdempotencyKey: fixture.key(key)})
	if err != nil || result.State == nil {
		fixture.t.Fatal(result, err)
	}
	state := *result.State
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{
		UserID: user, SessionBinding: binding, SessionID: state.SessionID, LeaseID: fixture.mustID("gle_"),
	}); err != nil {
		fixture.t.Fatal(err)
	}
	return state
}

func hintIntent(fixture *fixture, user int64, binding string, state State, key int) HintInput {
	return HintInput{UserID: user, SessionBinding: binding, SessionID: state.SessionID, ExpectedRevision: state.Revision, IdempotencyKey: fixture.key(key)}
}

func TestHintBudgetsStablePairReplayAndRecovery(t *testing.T) {
	for index, spec := range []string{"6x8", "8x8", "10x10"} {
		t.Run(spec, func(t *testing.T) {
			f := newFixture(t)
			user, binding := f.seedUser("hint", testFunding)
			state := f.startCurrent(user, binding, spec, 7000+index)
			want := []int{2, 3, 5}[index]
			if state.RulesVersion != 2 || state.OpportunitiesInitial != want || state.OpportunitiesRemaining != want {
				t.Fatal(state)
			}
			before := f.sessionBytes(state.SessionID)
			balance, randomCalls := f.balance(user), f.random.callCount()
			for n := 0; n < want; n++ {
				input := hintIntent(f, user, binding, state, 7010+n)
				result, err := f.service.Hint(context.Background(), input)
				if err != nil || result.Hint == nil || result.Reshuffled == nil || *result.Reshuffled ||
					result.State.OpportunitiesRemaining != want-n-1 || result.State.PairsRemoved != 0 {
					t.Fatal(result, err)
				}
				expected := f.loadBoard(state.SessionID).firstHint()
				if !reflect.DeepEqual(result.Hint, expected) {
					t.Fatal("hint does not use first legal row-major pair")
				}
				replay, err := f.service.Hint(context.Background(), input)
				if err != nil || !replay.IdempotentReplay {
					t.Fatal(replay, err)
				}
				body, _ := marshalResult(result)
				replayBody, _ := marshalResult(replay)
				if !bytes.Equal(body, replayBody) {
					t.Fatal("hint response changed on replay")
				}
				state = *result.State
			}
			if _, err := f.service.Hint(context.Background(), hintIntent(f, user, binding, state, 7050)); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			after := f.sessionBytes(state.SessionID)
			if !bytes.Equal(before.Board, after.Board) || !bytes.Equal(before.Removed, after.Removed) || f.balance(user) != balance || f.random.callCount() != randomCalls {
				t.Fatal("a legal hint changed the board, funds, or random stream")
			}
			if err := f.service.ValidatePersistedState(context.Background()); err != nil {
				t.Fatal(err)
			}
			current, err := f.service.Read(context.Background(), ReadInput{UserID: user, SessionBinding: binding})
			if err != nil || current.State.OpportunitiesRemaining != 0 || current.State.Revision != strconv.Itoa(want+1) {
				t.Fatal(current, err)
			}
			body, _ := json.Marshal(current)
			if bytes.Contains(body, []byte("\"hint\"")) || bytes.Contains(body, []byte("reshuffled")) {
				t.Fatal("GET restored transient hint")
			}
		})
	}
}

func TestHintScansCellsBeforeTileIdentifiers(t *testing.T) {
	definition, _ := resolveSpec("6x8")
	board := validBoardWithActive(definition, map[Coordinate]byte{
		{0, 0}: 2, {0, 1}: 2, {1, 0}: 1, {1, 1}: 1,
	})
	hint := board.firstHint()
	if hint == nil || hint.First != (Coordinate{0, 0}) || hint.Second != (Coordinate{0, 1}) {
		t.Fatal(hint)
	}
}

func TestHintSingleRefreshRollbackAndDeadBoardRecovery(t *testing.T) {
	f := newFixture(t)
	user, binding := f.seedUser("refresh", testFunding)
	state := f.startCurrent(user, binding, "6x8", 7100)
	definition, _ := resolveSpec("6x8")
	beforeDead := validBoardWithActive(definition, map[Coordinate]byte{
		{0, 0}: 1, {0, 1}: 2, {1, 0}: 2, {1, 1}: 1, {5, 0}: 3, {5, 1}: 3,
	})
	revision := f.replaceBoard(state.SessionID, beforeDead, definition.totalPairs()-3)
	calls := f.random.callCount()
	matched, err := f.service.Match(context.Background(), MatchInput{
		UserID: user, SessionBinding: binding, SessionID: state.SessionID, ExpectedRevision: revision.Decimal(),
		First: Coordinate{5, 0}, Second: Coordinate{5, 1}, IdempotencyKey: f.key(7101),
	})
	if err != nil || matched.State == nil || f.random.callCount() != calls || f.loadBoard(state.SessionID).hasMove() {
		t.Fatal(matched, err)
	}
	state = *matched.State
	if err := f.service.ValidatePersistedState(context.Background()); err != nil {
		t.Fatal("dead v2 board failed recovery", err)
	}
	before := f.sessionBytes(state.SessionID)
	idemBefore := f.scalar("SELECT COUNT(*) FROM idempotency_records")
	input := hintIntent(f, user, binding, state, 7102)
	f.random.failNext()
	if _, err := f.service.Hint(context.Background(), input); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatal(err)
	}
	f.random.setNoSwap(true)
	f.service.beforeReshuffleCommit = func() error { return errInjected }
	if _, err := f.service.Hint(context.Background(), input); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, f.sessionBytes(state.SessionID)) || f.scalar("SELECT assists_remaining FROM game_linklink_sessions WHERE id=?", state.SessionID) != 2 ||
		f.scalar("SELECT COUNT(*) FROM idempotency_records") != idemBefore {
		t.Fatal("failed refresh was not rolled back")
	}
	f.service.beforeReshuffleCommit = nil
	calls = f.random.callCount()
	result, err := f.service.Hint(context.Background(), input)
	if err != nil || result.Hint != nil || result.Reshuffled == nil || !*result.Reshuffled || result.State.OpportunitiesRemaining != 1 ||
		f.random.callCount()-calls != 3 || f.loadBoard(state.SessionID).hasMove() {
		t.Fatal("refresh retried or rejected a still blocked board", result, err)
	}
	after := f.sessionBytes(state.SessionID)
	if !bytes.Equal(before.Board, after.Board) || !bytes.Equal(before.Removed, after.Removed) || before.Pairs != after.Pairs {
		t.Fatal("no-swap refresh changed occupied/removed cells")
	}
	if err := f.service.ValidatePersistedState(context.Background()); err != nil {
		t.Fatal(err)
	}
	replay, err := f.service.Hint(context.Background(), input)
	if err != nil || !replay.IdempotentReplay || f.random.callCount()-calls != 3 {
		t.Fatal(replay, err)
	}
	// Exercise a real permutation as well; only occupied tile values can move.
	f.random.setNoSwap(false)
	result, err = f.service.Hint(context.Background(), hintIntent(f, user, binding, *result.State, 7103))
	if err != nil || result.State.OpportunitiesRemaining != 0 {
		t.Fatal(result, err)
	}
	board := f.loadBoard(state.SessionID)
	if err := board.validate(); err != nil {
		t.Fatal(err)
	}
	for i := range board.tiles {
		if board.isRemovedIndex(i) && board.tiles[i] != before.Board[i] {
			t.Fatal("refresh moved a removed tile")
		}
	}
	if !bytes.Equal(board.removed, before.Removed) || board.activeCount() != 4 {
		t.Fatal("refresh changed empty positions")
	}
}

func TestHintRevisionLeaseMaintenanceAndDeadlineAuthority(t *testing.T) {
	f := newFixture(t)
	user, binding := f.seedUser("authority", testFunding)
	other, otherBinding := f.seedUser("other", testFunding)
	state := f.startCurrent(user, binding, "6x8", 7200)
	input := hintIntent(f, user, binding, state, 7201)
	wrong := input
	wrong.UserID = other
	wrong.SessionBinding = otherBinding
	if _, err := f.service.Hint(context.Background(), wrong); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	wrong = input
	wrong.SessionBinding = "stale-binding"
	if _, err := f.service.Hint(context.Background(), wrong); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	wrong = input
	wrong.ExpectedRevision = "2"
	if _, err := f.service.Hint(context.Background(), wrong); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	f.setMaintenance(true)
	wrong = input
	wrong.SessionBinding = "stale-binding"
	if _, err := f.service.Hint(context.Background(), wrong); !errors.Is(err, ErrMaintenance) {
		t.Fatal(err)
	}
	result, err := f.service.Hint(context.Background(), input)
	if err != nil || result.State.OpportunitiesRemaining != 1 {
		t.Fatal(result, err)
	}
	f.setMaintenance(false)
	if _, err := f.database.Exec("UPDATE users SET is_banned=1 WHERE id=?", user); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Hint(context.Background(), input); !errors.Is(err, ErrForbidden) {
		t.Fatal("replay skipped final authorization", err)
	}
	if _, err := f.database.Exec("UPDATE users SET is_banned=0 WHERE id=?", user); err != nil {
		t.Fatal(err)
	}
	f.clock.Store(state.Deadline)
	late := hintIntent(f, user, binding, *result.State, 7202)
	if _, err := f.service.Hint(context.Background(), late); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if f.scalar("SELECT assists_remaining FROM game_linklink_summaries WHERE session_id=?", state.SessionID) != 1 ||
		f.scalar("SELECT score FROM game_linklink_summaries WHERE session_id=?", state.SessionID) != 0 {
		t.Fatal("deadline consumed an assist or awarded unused-assist score")
	}
	replay, err := f.service.Hint(context.Background(), input)
	if err != nil || !replay.IdempotentReplay {
		t.Fatal(replay, err)
	}
	a, _ := marshalResult(result)
	b, _ := marshalResult(replay)
	if !bytes.Equal(a, b) {
		t.Fatal("terminal replay changed original hint")
	}
}

func TestHintConcurrentSameRevisionConsumesOneOpportunity(t *testing.T) {
	f := newFixture(t)
	user, binding := f.seedUser("concurrent", testFunding)
	state := f.startCurrent(user, binding, "6x8", 7300)
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			<-start
			_, err := f.service.Hint(context.Background(), hintIntent(f, user, binding, state, 7301+i))
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrServiceUnavailable) {
			t.Fatal(err)
		}
	}
	if success != 1 || f.scalar("SELECT assists_remaining FROM game_linklink_sessions WHERE id=?", state.SessionID) != 1 {
		t.Fatal("concurrent intents consumed more than one opportunity", success)
	}
}

func TestHintCompletedScoreAndExportsKeepFrozenOpportunities(t *testing.T) {
	f := newFixture(t)
	user, binding := f.seedUser("score", testFunding)
	state := f.startCurrent(user, binding, "6x8", 7400)
	result, err := f.service.Hint(context.Background(), hintIntent(f, user, binding, state, 7401))
	if err != nil {
		t.Fatal(err)
	}
	state = *result.State
	definition, _ := resolveSpec("6x8")
	board := validBoardWithActive(definition, map[Coordinate]byte{{0, 0}: 1, {0, 1}: 1})
	revision := f.replaceBoard(state.SessionID, board, definition.totalPairs()-1)
	result, err = f.service.Match(context.Background(), MatchInput{UserID: user, SessionBinding: binding, SessionID: state.SessionID,
		ExpectedRevision: revision.Decimal(), First: Coordinate{0, 0}, Second: Coordinate{0, 1}, IdempotencyKey: f.key(7402)})
	if err != nil || result.Summary == nil || *result.Summary.Score != "2650" || result.Summary.OpportunitiesRemaining != 1 {
		t.Fatal(result, err)
	}
	if f.scalar("SELECT COUNT(*) FROM game_onboarding_completions WHERE user_id=?", user) != 1 {
		t.Fatal("successful hint-assisted game did not award newcomer task")
	}
	tx, err := f.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	export, err := f.service.Lifecycle().ExportUser(context.Background(), tx, user, testNow)
	if err != nil || len(export.Summaries) != 1 || export.Summaries[0].OpportunitiesInitial != 2 || *export.Summaries[0].Score != "2650" {
		t.Fatal(export, err)
	}
}

func TestHintHTTPStrictBodyAndLegacyZeroProjection(t *testing.T) {
	f := newFixture(t)
	user, binding := f.seedUser("http-hint", testFunding)
	state := f.startCurrent(user, binding, "6x8", 7500)
	api := &httpAPI{service: f.service}
	for _, body := range []string{`{"expected_revision":"1","board":[]}`, `{"expected_revision":"1","expected_revision":"1"}`, `{"expected_revision":1}`, `{"expected_revision":null}`} {
		request := httptest.NewRequest(http.MethodPost, RouteHint, strings.NewReader(body))
		request.SetPathValue("id", state.SessionID)
		request.Header.Set("Idempotency-Key", f.key(7501))
		recorder := httptest.NewRecorder()
		api.hint(recorder, request, resources.ContinuationUserPrincipal{UserID: user, SessionBinding: binding})
		if recorder.Code != http.StatusBadRequest {
			t.Fatal(recorder.Code, recorder.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, RouteHint, strings.NewReader(`{"expected_revision":"1"}`))
	request.SetPathValue("id", state.SessionID)
	request.Header.Set("Idempotency-Key", f.key(7501))
	recorder := httptest.NewRecorder()
	api.hint(recorder, request, resources.ContinuationUserPrincipal{UserID: user, SessionBinding: binding})
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"opportunities_remaining":1`) ||
		!strings.Contains(recorder.Body.String(), `"reshuffled":false`) {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
	legacy, legacyBinding := f.seedUser("legacy", testFunding)
	old, err := f.service.startLegacy(context.Background(), StartInput{UserID: legacy, Spec: "6x8", IdempotencyKey: f.key(7510)})
	if err != nil || old.State.OpportunitiesInitial != 0 || old.State.OpportunitiesRemaining != 0 {
		t.Fatal(old, err)
	}
	if _, err := f.service.RenewLease(context.Background(), LeaseInput{UserID: legacy, SessionBinding: legacyBinding, SessionID: old.State.SessionID, LeaseID: f.mustID("gle_")}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Hint(context.Background(), hintIntent(f, legacy, legacyBinding, *old.State, 7511)); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
