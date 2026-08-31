package linklink

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestCurrentHTTPWireIsRawNullStateOrSummaryAndActiveWins(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("current-wire", testFunding)
	api := &httpAPI{service: fixture.service}

	nullResponse := readCurrentHTTP(t, api, userID, binding)
	if nullResponse.Code != http.StatusOK || nullResponse.Body.String() != "null" || nullResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("empty current = %d %q", nullResponse.Code, nullResponse.Body.String())
	}

	started, err := fixture.service.Start(context.Background(), StartInput{
		UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(200),
	})
	if err != nil {
		t.Fatal(err)
	}
	stateResponse := readCurrentHTTP(t, api, userID, binding)
	var state State
	if stateResponse.Code != http.StatusOK || json.Unmarshal(stateResponse.Body.Bytes(), &state) != nil || state.SessionID != started.State.SessionID {
		t.Fatalf("active current = %d %s", stateResponse.Code, stateResponse.Body.String())
	}
	var stateObject map[string]json.RawMessage
	if err := json.Unmarshal(stateResponse.Body.Bytes(), &stateObject); err != nil {
		t.Fatal(err)
	}
	if _, ok := stateObject["board"]; !ok || stateObject["State"] != nil || stateObject["Summary"] != nil || stateObject["terminal_reason"] != nil {
		t.Fatalf("state was wrapped or malformed: %s", stateResponse.Body.String())
	}

	abandoned, err := fixture.service.Abandon(context.Background(), AbandonInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID,
		ExpectedRevision: "1", Confirmation: true, IdempotencyKey: fixture.key(201),
	})
	if err != nil {
		t.Fatal(err)
	}
	summaryResponse := readCurrentHTTP(t, api, userID, binding)
	var summary Summary
	if summaryResponse.Code != http.StatusOK || json.Unmarshal(summaryResponse.Body.Bytes(), &summary) != nil || !reflect.DeepEqual(summary, abandoned) {
		t.Fatalf("terminal current = %d %s", summaryResponse.Code, summaryResponse.Body.String())
	}
	var summaryObject map[string]json.RawMessage
	if err := json.Unmarshal(summaryResponse.Body.Bytes(), &summaryObject); err != nil {
		t.Fatal(err)
	}
	expectedSummaryKeys := []string{
		"session_id", "spec", "price", "terminal_reason", "started_at", "deadline", "terminal_at", "pairs_removed", "total_pairs", "score",
	}
	if len(summaryObject) != len(expectedSummaryKeys) {
		t.Fatalf("summary wire fields = %v", summaryObject)
	}
	for _, key := range expectedSummaryKeys {
		if _, ok := summaryObject[key]; !ok {
			t.Fatalf("summary missing %q: %s", key, summaryResponse.Body.String())
		}
	}
	for _, forbidden := range []string{"board", "tiles", "removed", "revision", "server_now", "State", "Summary"} {
		if _, ok := summaryObject[forbidden]; ok {
			t.Fatalf("summary leaked %q: %s", forbidden, summaryResponse.Body.String())
		}
	}

	fresh, err := fixture.service.Start(context.Background(), StartInput{
		UserID: userID, Spec: game.LinkLinkSpec8x8, IdempotencyKey: fixture.key(202),
	})
	if err != nil || fresh.State == nil || fresh.HTTPStatus != http.StatusCreated {
		t.Fatalf("start with retained summary = (%+v,%v)", fresh, err)
	}
	priorityResponse := readCurrentHTTP(t, api, userID, binding)
	var priority State
	if priorityResponse.Code != http.StatusOK || json.Unmarshal(priorityResponse.Body.Bytes(), &priority) != nil || priority.SessionID != fresh.State.SessionID || strings.Contains(priorityResponse.Body.String(), "terminal_reason") {
		t.Fatalf("active did not win over summary = %d %s", priorityResponse.Code, priorityResponse.Body.String())
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, abandoned.SessionID) != 1 {
		t.Fatal("starting a new session removed the retained summary")
	}

	if _, err := json.Marshal(CurrentResult{State: &State{}, Summary: &Summary{}}); err == nil {
		t.Fatal("current union accepted state and summary together")
	}
}

func TestCurrentHTTPRejectsBodyAndQueryBeforeDeadlineCatchup(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("current-http-strict", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{
		UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(205),
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(started.State.Deadline)
	api := &httpAPI{service: fixture.service}
	principal := resources.ContinuationUserPrincipal{UserID: userID, SessionBinding: binding}
	requests := []*http.Request{
		httptest.NewRequest(http.MethodGet, "https://user.example"+RouteSession+"?history=1", nil),
		httptest.NewRequest(http.MethodGet, "https://user.example"+RouteSession, strings.NewReader(`{}`)),
	}
	for _, request := range requests {
		recorder := httptest.NewRecorder()
		api.read(recorder, request, principal)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid current request = %d %s", recorder.Code, recorder.Body.String())
		}
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=?`, started.State.SessionID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, started.State.SessionID) != 0 {
		t.Fatal("invalid current request materialized the deadline")
	}
	valid := readCurrentHTTP(t, api, userID, binding)
	if valid.Code != http.StatusOK || !strings.Contains(valid.Body.String(), `"terminal_reason":"timed_out"`) {
		t.Fatalf("valid deadline current = %d %s", valid.Code, valid.Body.String())
	}
}

func TestCurrentReadDeadlineMinusOneEqualAndPlusOne(t *testing.T) {
	for index, offset := range []int64{-1, 0, 1} {
		t.Run(time.Duration(offset).String(), func(t *testing.T) {
			fixture := newFixture(t)
			userID, binding := fixture.seedUser("current-deadline-"+time.Duration(offset).String(), testFunding)
			started, err := fixture.service.Start(context.Background(), StartInput{
				UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(210 + index),
			})
			if err != nil {
				t.Fatal(err)
			}
			fixture.clock.Store(started.State.Deadline + offset)
			current, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
			if err != nil {
				t.Fatal(err)
			}
			if offset < 0 {
				if current.State == nil || current.Summary != nil || current.State.ServerNow != started.State.Deadline-1 ||
					fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, started.State.SessionID) != 0 {
					t.Fatalf("deadline-1 current = %+v", current)
				}
				return
			}
			if current.State != nil || current.Summary == nil || current.Summary.SessionID != started.State.SessionID ||
				current.Summary.TerminalReason != TerminalTimedOut || current.Summary.TerminalAt != started.State.Deadline+offset ||
				fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=?`, started.State.SessionID) != 0 {
				t.Fatalf("deadline%+d current = %+v", offset, current)
			}
		})
	}
}

func TestCurrentLatestSummaryUsesStableOrdering(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("current-order", testFunding)
	terminalAt := testNow - 100
	olderID := fixture.mustID("ll_")
	firstTieID := fixture.mustID("ll_")
	secondTieID := fixture.mustID("ll_")
	insertAbandonedSummary(t, fixture, userID, olderID, terminalAt-1)
	insertAbandonedSummary(t, fixture, userID, firstTieID, terminalAt)
	insertAbandonedSummary(t, fixture, userID, secondTieID, terminalAt)
	want := firstTieID
	if secondTieID > want {
		want = secondTieID
	}

	for attempt := 0; attempt < 2; attempt++ {
		current, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
		if err != nil || current.State != nil || current.Summary == nil || current.Summary.SessionID != want {
			t.Fatalf("latest attempt %d = (%+v,%v), want %s", attempt, current, err, want)
		}
	}
}

func TestCurrentSummaryThirtyDayBoundaryDoesNotRefresh(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("current-retention", testFunding)
	sessionID := fixture.mustID("ll_")
	terminalAt := testNow
	insertAbandonedSummary(t, fixture, userID, sessionID, terminalAt)
	expiresAt := terminalAt + int64(summaryWindow/time.Second)

	fixture.clock.Store(expiresAt - 1)
	current, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
	if err != nil || current.State != nil || current.Summary == nil || current.Summary.SessionID != sessionID {
		t.Fatalf("summary at 30d-1 = (%+v,%v)", current, err)
	}
	for _, now := range []int64{expiresAt, expiresAt + 1} {
		fixture.clock.Store(now)
		current, err = fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
		if err != nil || current.State != nil || current.Summary != nil {
			t.Fatalf("summary at expiry offset %d = (%+v,%v)", now-expiresAt, current, err)
		}
	}
	var storedTerminalAt int64
	if err := fixture.database.QueryRow(`SELECT terminal_at FROM game_linklink_summaries WHERE session_id=?`, sessionID).Scan(&storedTerminalAt); err != nil || storedTerminalAt != terminalAt {
		t.Fatalf("current read refreshed or removed retention authority: terminal_at=%d err=%v", storedTerminalAt, err)
	}
}

func TestCurrentAfterWorkerRecoveryAndRestart(t *testing.T) {
	t.Run("worker rail", func(t *testing.T) {
		fixture := newFixture(t)
		userID, binding := fixture.seedUser("current-worker", testFunding)
		started, err := fixture.service.Start(context.Background(), StartInput{
			UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(220),
		})
		if err != nil {
			t.Fatal(err)
		}
		fixture.clock.Store(started.State.Deadline + 1)
		processed, err := fixture.service.runDue(context.Background(), fixture.clock.Load())
		if err != nil || processed != 1 {
			t.Fatalf("run due = (%d,%v)", processed, err)
		}
		current, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
		if err != nil || current.State != nil || current.Summary == nil || current.Summary.SessionID != started.State.SessionID || current.Summary.TerminalAt != started.State.Deadline+1 {
			t.Fatalf("current after worker = (%+v,%v)", current, err)
		}
	})

	t.Run("listener recovery and restart", func(t *testing.T) {
		fixture := newFixture(t)
		userID, binding := fixture.seedUser("current-recovery", testFunding)
		started, err := fixture.service.Start(context.Background(), StartInput{
			UserID: userID, Spec: game.LinkLinkSpec8x8, IdempotencyKey: fixture.key(221),
		})
		if err != nil {
			t.Fatal(err)
		}
		fixture.clock.Store(started.State.Deadline + 30)
		if err := fixture.service.RecoverBeforeListen(context.Background()); err != nil {
			t.Fatal(err)
		}
		beforeRestart, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
		if err != nil || beforeRestart.State != nil || beforeRestart.Summary == nil || beforeRestart.Summary.SessionID != started.State.SessionID {
			t.Fatalf("current after recovery = (%+v,%v)", beforeRestart, err)
		}
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
		afterRestart, err := restarted.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
		if err != nil || afterRestart.State != nil || !reflect.DeepEqual(afterRestart.Summary, beforeRestart.Summary) {
			t.Fatalf("current after restart = (%+v,%v), want %+v", afterRestart, err, beforeRestart)
		}
	})
}

func TestCurrentSummaryOwnershipMaintenanceAndAccountDeletion(t *testing.T) {
	fixture := newFixture(t)
	ownerID, ownerBinding := fixture.seedUser("current-owner", testFunding)
	otherID, otherBinding := fixture.seedUser("current-other", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{
		UserID: ownerID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(230),
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := fixture.service.Abandon(context.Background(), AbandonInput{
		UserID: ownerID, SessionBinding: ownerBinding, SessionID: started.State.SessionID,
		ExpectedRevision: "1", Confirmation: true, IdempotencyKey: fixture.key(231),
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerCurrent, err := fixture.service.Read(context.Background(), ReadInput{UserID: ownerID, SessionBinding: ownerBinding})
	if err != nil || !reflect.DeepEqual(ownerCurrent.Summary, &summary) {
		t.Fatalf("owner current = (%+v,%v)", ownerCurrent, err)
	}
	otherCurrent, err := fixture.service.Read(context.Background(), ReadInput{UserID: otherID, SessionBinding: otherBinding})
	if err != nil || otherCurrent.State != nil || otherCurrent.Summary != nil {
		t.Fatalf("foreign current = (%+v,%v)", otherCurrent, err)
	}

	fixture.setMaintenance(true)
	if current, err := fixture.service.Read(context.Background(), ReadInput{UserID: ownerID, SessionBinding: ownerBinding}); current.State != nil || current.Summary != nil || !errors.Is(err, ErrMaintenance) {
		t.Fatalf("maintenance summary read = (%+v,%v)", current, err)
	}
	if current, err := fixture.service.Read(context.Background(), ReadInput{UserID: otherID, SessionBinding: otherBinding}); current.State != nil || current.Summary != nil || !errors.Is(err, ErrMaintenance) {
		t.Fatalf("maintenance empty read = (%+v,%v)", current, err)
	}
	fixture.setMaintenance(false)

	tx, err := fixture.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	finalizer, err := fixture.service.Lifecycle().PrepareUserDeletion(context.Background(), tx, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, ownerID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil || !finalizer.Commit() {
		t.Fatalf("account deletion = %v", err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, summary.SessionID) != 0 {
		t.Fatal("account deletion retained the LinkLink summary")
	}
	if current, err := fixture.service.Read(context.Background(), ReadInput{UserID: ownerID, SessionBinding: ownerBinding}); current.State != nil || current.Summary != nil || !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted owner current = (%+v,%v)", current, err)
	}
}

func TestMaintenanceExpiredActiveTerminalizesWithoutGrantingSummaryRead(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("current-maintenance-expired", testFunding)
	started, err := fixture.service.Start(context.Background(), StartInput{
		UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(240),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.RenewLease(context.Background(), LeaseInput{
		UserID: userID, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: fixture.mustID("gle_"),
	}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store(started.State.Deadline)
	fixture.setMaintenance(true)
	beforeContinuationCalls := fixture.continuation.calls.Load()
	current, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
	if current.State != nil || current.Summary != nil || !errors.Is(err, ErrMaintenance) {
		t.Fatalf("expired maintenance current = (%+v,%v)", current, err)
	}
	if fixture.continuation.calls.Load() != beforeContinuationCalls+1 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=?`, started.State.SessionID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=? AND terminal_reason='timed_out'`, started.State.SessionID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_online_leases WHERE session_id=?`, started.State.SessionID) != 0 {
		t.Fatal("maintenance timeout did not use system continuation and physically terminalize")
	}
	fixture.setMaintenance(false)
	current, err = fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding})
	if err != nil || current.State != nil || current.Summary == nil || current.Summary.SessionID != started.State.SessionID {
		t.Fatalf("post-maintenance summary = (%+v,%v)", current, err)
	}
}

func readCurrentHTTP(t *testing.T, api *httpAPI, userID int64, binding string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "https://user.example"+RouteSession, nil)
	recorder := httptest.NewRecorder()
	api.read(recorder, request, resources.ContinuationUserPrincipal{UserID: userID, SessionBinding: binding})
	return recorder
}

func insertAbandonedSummary(t *testing.T, fixture *fixture, userID int64, sessionID string, terminalAt int64) {
	t.Helper()
	definition, ok := resolveSpec(game.LinkLinkSpec6x8)
	if !ok || terminalAt < 10 {
		t.Fatal("invalid summary fixture")
	}
	startedAt := terminalAt - 10
	deadline := startedAt + definition.Seconds
	if _, err := fixture.database.Exec(`
INSERT INTO game_linklink_summaries(
 session_id,user_id,spec,price_milli,terminal_reason,started_at,deadline,terminal_at,pairs_removed,score
) VALUES(?,?,?,?,?,?,?,?,?,NULL)`,
		sessionID, userID, game.LinkLinkSpec6x8, 1000, TerminalAbandoned, startedAt, deadline, terminalAt, 0); err != nil {
		t.Fatal(err)
	}
}
