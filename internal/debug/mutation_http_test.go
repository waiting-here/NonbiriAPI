package debug

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestMutationRepositoryPersistentExactReplayConflictActorAndExpiry(t *testing.T) {
	database := newDebugMutationDB(t)
	clock := newDebugTestClock(130_000)
	repository, err := newMutationRepository(database, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := idempotency.CanonicalJSON(modeMutation{Mode: ModeLive, ExpectedRevision: "1", LiveConfirmation: true})
	if err != nil {
		t.Fatal(err)
	}
	input := resources.ControlMutation{
		IdempotencyKey: debugMutationKey('A'), Method: http.MethodPut,
		Route: "/api/debug/session/mode", CanonicalBody: canonical,
	}
	var calls atomic.Int64
	operation := func() (int, []byte, error) {
		calls.Add(1)
		return http.StatusOK, []byte("{\"safe\":true}\n"), nil
	}
	status, body, replayed, err := repository.Execute(context.Background(), 1, input, operation)
	if err != nil || status != 200 || replayed || string(body) != "{\"safe\":true}\n" || calls.Load() != 1 {
		t.Fatalf("first Execute = (%d,%q,%v,%v), calls=%d", status, body, replayed, err, calls.Load())
	}

	// A new repository instance proves replay is persisted, not a Hub map.
	restarted, _ := newMutationRepository(database, clock.Now)
	status, body, replayed, err = restarted.Execute(context.Background(), 1, input, operation)
	if err != nil || status != 200 || !replayed || string(body) != "{\"safe\":true}\n" || calls.Load() != 1 {
		t.Fatalf("replay Execute = (%d,%q,%v,%v), calls=%d", status, body, replayed, err, calls.Load())
	}

	conflict := input
	conflict.Route = "/api/debug/session/replace"
	if _, _, _, err := repository.Execute(context.Background(), 1, conflict, operation); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-route key reuse = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("conflict executed operation %d times", calls.Load())
	}

	// Actor scope is an explicit digest dimension and key namespace.
	if _, _, replayed, err := repository.Execute(context.Background(), 2, input, operation); err != nil || replayed {
		t.Fatalf("other actor = replayed:%v err:%v", replayed, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("other actor operation calls = %d", calls.Load())
	}

	clock.Advance(24 * time.Hour)
	if _, _, replayed, err := repository.Execute(context.Background(), 1, input, operation); err != nil || replayed {
		t.Fatalf("exact expiry = replayed:%v err:%v", replayed, err)
	}
	if calls.Load() != 3 {
		t.Fatalf("expired operation calls = %d", calls.Load())
	}
}

func TestMutationRepositoryRollsBackFailureAndBoundsResponse(t *testing.T) {
	database := newDebugMutationDB(t)
	clock := newDebugTestClock(140_000)
	repository, _ := newMutationRepository(database, clock.Now)
	input := resources.ControlMutation{IdempotencyKey: debugMutationKey('B'), Method: http.MethodPost, Route: "/api/debug/session"}
	if _, _, _, err := repository.Execute(context.Background(), 1, input, func() (int, []byte, error) {
		return 0, nil, ErrConflict
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("domain failure = %v", err)
	}
	called := 0
	if _, _, replayed, err := repository.Execute(context.Background(), 1, input, func() (int, []byte, error) {
		called++
		return http.StatusCreated, []byte("{}\n"), nil
	}); err != nil || replayed || called != 1 {
		t.Fatalf("retry after rollback = replayed:%v calls:%d err:%v", replayed, called, err)
	}

	oversized := resources.ControlMutation{IdempotencyKey: debugMutationKey('C'), Method: http.MethodPost, Route: "/api/debug/session"}
	if _, _, _, err := repository.Execute(context.Background(), 1, oversized, func() (int, []byte, error) {
		return http.StatusOK, make([]byte, idempotency.MaxResponseBytes+1), nil
	}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("oversized replay response = %v", err)
	}
	if _, _, replayed, err := repository.Execute(context.Background(), 1, oversized, func() (int, []byte, error) {
		return http.StatusOK, []byte("{}\n"), nil
	}); err != nil || replayed {
		t.Fatalf("oversized response left replay = replayed:%v err:%v", replayed, err)
	}

	invalid := input
	invalid.IdempotencyKey = "short"
	if _, _, _, err := repository.Execute(context.Background(), 1, invalid, func() (int, []byte, error) {
		t.Fatal("invalid key reached operation")
		return 0, nil, nil
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid key = %v", err)
	}
}

func TestMutationRepositoryCallerCancellationAfterOperationStillCommitsReplay(t *testing.T) {
	database := newDebugMutationDB(t)
	clock := newDebugTestClock(145_000)
	repository, _ := newMutationRepository(database, clock.Now)
	input := resources.ControlMutation{
		IdempotencyKey: debugMutationKey('D'), Method: http.MethodPost,
		Route: "/api/debug/session/stop", CanonicalBody: []byte(`{"confirm_inflight":true,"expected_revision":"1"}`),
	}

	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	status, body, replayed, err := repository.Execute(ctx, 1, input, func() (int, []byte, error) {
		calls++
		cancel() // Simulate response loss immediately after the in-memory mutation.
		return http.StatusNoContent, nil, nil
	})
	if err != nil || status != http.StatusNoContent || len(body) != 0 || replayed || calls != 1 {
		t.Fatalf("cancelled first Execute = (%d,%q,%v,%v), calls=%d", status, body, replayed, err, calls)
	}

	status, body, replayed, err = repository.Execute(context.Background(), 1, input, func() (int, []byte, error) {
		calls++
		return http.StatusInternalServerError, nil, nil
	})
	if err != nil || status != http.StatusNoContent || len(body) != 0 || !replayed || calls != 1 {
		t.Fatalf("cancelled replay = (%d,%q,%v,%v), calls=%d", status, body, replayed, err, calls)
	}
}

type debugRoute struct {
	method  string
	pattern string
	handler AuthorizedUserHandler
}

type debugRegistrar struct{ routes []debugRoute }

func (registrar *debugRegistrar) RegisterDebugUserRoute(method, pattern string, handler AuthorizedUserHandler) error {
	registrar.routes = append(registrar.routes, debugRoute{method, pattern, handler})
	return nil
}

func TestDebugHTTPStrictCanonicalReplayAttachAndAuthorization(t *testing.T) {
	clock := newDebugTestClock(150_000)
	verifier := &debugTestVerifier{state: IdentityActive}
	hub, _ := newDebugTestHub(t, clock, verifier)
	mutations, _ := newMutationRepository(newDebugMutationDB(t), clock.Now)
	api, err := NewHTTPAPI(hub, mutations)
	if err != nil {
		t.Fatal(err)
	}
	principal := UserPrincipal{UserID: 150, SessionBinding: "binding-150"}
	request := func(method, target, body, key string) (*httptest.ResponseRecorder, *http.Request) {
		recorder := httptest.NewRecorder()
		var reader *strings.Reader
		if body != "" {
			reader = strings.NewReader(body)
		} else {
			reader = strings.NewReader("")
		}
		req := httptest.NewRequest(method, target, reader)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		return recorder, req
	}

	createKey := debugMutationKey('D')
	createRecorder, createRequest := request(http.MethodPost, "/api/debug/session", "", createKey)
	api.createSession(createRecorder, createRequest, principal)
	if createRecorder.Code != http.StatusCreated || createRecorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create = %d %v %s", createRecorder.Code, createRecorder.Header(), createRecorder.Body.String())
	}
	createdBody := createRecorder.Body.String()

	attachRecorder, attachRequest := request(http.MethodPost, "/api/debug/session", "", debugMutationKey('E'))
	api.createSession(attachRecorder, attachRequest, principal)
	if attachRecorder.Code != http.StatusOK || attachRecorder.Body.String() != createdBody {
		t.Fatalf("attach = %d %s, create=%s", attachRecorder.Code, attachRecorder.Body.String(), createdBody)
	}

	modeKey := debugMutationKey('F')
	modeOne, modeRequestOne := request(http.MethodPut, "/api/debug/session/mode",
		`{"mode":"live","expected_revision":"1","live_confirmation":true}`, modeKey)
	api.changeMode(modeOne, modeRequestOne, principal)
	if modeOne.Code != http.StatusOK {
		t.Fatalf("mode first = %d %s", modeOne.Code, modeOne.Body.String())
	}
	modeTwo, modeRequestTwo := request(http.MethodPut, "/api/debug/session/mode",
		"{ \"live_confirmation\" : true, \"expected_revision\" : \"1\", \"mode\" : \"live\" }", modeKey)
	api.changeMode(modeTwo, modeRequestTwo, principal)
	if modeTwo.Code != http.StatusOK || modeTwo.Body.String() != modeOne.Body.String() {
		t.Fatalf("canonical replay = %d %s, want %s", modeTwo.Code, modeTwo.Body.String(), modeOne.Body.String())
	}

	conflictRecorder, conflictRequest := request(http.MethodPost, "/api/debug/session/replace",
		`{"expected_revision":"2","confirm_inflight":false}`, modeKey)
	api.replaceSession(conflictRecorder, conflictRequest, principal)
	if conflictRecorder.Code != http.StatusConflict {
		t.Fatalf("cross-route key = %d %s", conflictRecorder.Code, conflictRecorder.Body.String())
	}

	for _, body := range []string{
		`{"mode":"dry","expected_revision":"2","live_confirmation":false,"extra":1}`,
		`{"mode":"dry","mode":"live","expected_revision":"2","live_confirmation":false}`,
		`{"mode":"dry","expected_revision":"2","live_confirmation":false}{}`,
		`{"mode":"dry","expected_revision":"2"}`,
		`{"mode":"dry","expected_revision":"2","live_confirmation":null}`,
	} {
		recorder, req := request(http.MethodPut, "/api/debug/session/mode", body, debugMutationKey('G'))
		api.changeMode(recorder, req, principal)
		if recorder.Code != http.StatusBadRequest || strings.Count(recorder.Body.String(), "[NonbiriAPI] ") != 1 {
			t.Fatalf("strict body %q = %d %s", body, recorder.Code, recorder.Body.String())
		}
	}
	for _, body := range []string{
		`{"expected_revision":"2"}`,
		`{"expected_revision":"2","confirm_inflight":null}`,
	} {
		recorder, req := request(http.MethodPost, "/api/debug/session/stop", body, debugMutationKey('H'))
		api.stopSession(recorder, req, principal)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("required stop body %q = %d %s", body, recorder.Code, recorder.Body.String())
		}
	}

	for _, test := range []struct {
		name   string
		invoke func(*httptest.ResponseRecorder, *http.Request)
	}{
		{"get query", func(recorder *httptest.ResponseRecorder, req *http.Request) { api.getSession(recorder, req, principal) }},
		{"create query", func(recorder *httptest.ResponseRecorder, req *http.Request) {
			api.createSession(recorder, req, principal)
		}},
		{"events query", func(recorder *httptest.ResponseRecorder, req *http.Request) { api.events(recorder, req, principal) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder, req := request(http.MethodGet, "/api/debug/session?unexpected=1", "", debugMutationKey('I'))
			test.invoke(recorder, req)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("query status = %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	getBody, getBodyRequest := request(http.MethodGet, "/api/debug/session", "x", "")
	api.getSession(getBody, getBodyRequest, principal)
	if getBody.Code != http.StatusBadRequest {
		t.Fatalf("GET body = %d %s", getBody.Code, getBody.Body.String())
	}

	missingKey, missingRequest := request(http.MethodPost, "/api/debug/session", "", "")
	api.createSession(missingKey, missingRequest, principal)
	if missingKey.Code != http.StatusBadRequest {
		t.Fatalf("missing key = %d %s", missingKey.Code, missingKey.Body.String())
	}

	// Completed replay is never an authorization ticket.
	verifier.set(IdentityRevoked, nil)
	denied, deniedRequest := request(http.MethodPost, "/api/debug/session", "", createKey)
	api.createSession(denied, deniedRequest, principal)
	if denied.Code != http.StatusUnauthorized || denied.Body.String() == createdBody {
		t.Fatalf("replay after revoke = %d %s", denied.Code, denied.Body.String())
	}
	deniedMalformed, deniedMalformedRequest := request(http.MethodGet, "/api/debug/session?unexpected=1", "x", "")
	api.getSession(deniedMalformed, deniedMalformedRequest, principal)
	if deniedMalformed.Code != http.StatusUnauthorized {
		t.Fatalf("authorization did not win malformed read = %d %s", deniedMalformed.Code, deniedMalformed.Body.String())
	}
}

func TestDebugHTTPDefiniteIdentityLossImmediatelyTerminatesSession(t *testing.T) {
	for _, test := range []struct {
		name   string
		state  IdentityState
		status int
		reason EndReason
	}{
		{name: "revoked", state: IdentityRevoked, status: http.StatusUnauthorized, reason: EndAuthRevoked},
		{name: "banned", state: IdentityBanned, status: http.StatusForbidden, reason: EndAccountBanned},
		{name: "deleted", state: IdentityDeleted, status: http.StatusUnauthorized, reason: EndAccountDeleted},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := newDebugTestClock(155_000)
			verifier := &debugTestVerifier{state: IdentityActive}
			hub, _ := newDebugTestHub(t, clock, verifier)
			mutations, _ := newMutationRepository(newDebugMutationDB(t), clock.Now)
			api, _ := NewHTTPAPI(hub, mutations)
			principal := UserPrincipal{UserID: 155, SessionBinding: "binding-155"}
			mustStartDebug(t, hub, principal.UserID, principal.SessionBinding)
			if _, err := hub.ChangeMode(principal.UserID, "1", ModeLive, true); err != nil {
				t.Fatal(err)
			}
			decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
				UserID: principal.UserID, RouteKind: RouteOpenAIChat, Model: "m",
				MediaType: "application/json", Body: []byte(`{}`), IdentityCertain: true,
			})
			if err != nil || decision.Trace == nil || decision.Mode != ModeLive {
				t.Fatalf("live trace = (%+v,%v)", decision, err)
			}
			subscription, err := hub.Subscribe(context.Background(), principal.UserID, principal.SessionBinding, "")
			if err != nil {
				t.Fatal(err)
			}
			_ = mustNextDebug(t, subscription)
			old := subscription.session
			verifier.set(test.state, nil)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/debug/session?malformed=1", strings.NewReader("x"))
			api.getSession(recorder, request, principal)
			if recorder.Code != test.status {
				t.Fatalf("identity loss response = %d %s", recorder.Code, recorder.Body.String())
			}
			select {
			case <-decision.Trace.Context().Done():
			case <-time.After(time.Second):
				t.Fatal("identity loss did not cancel live trace")
			}
			endEvent := mustNextDebug(t, subscription)
			end := decodeDebugData[SessionEndData](t, endEvent)
			if endEvent.Kind != EventSessionEnd || end.Reason != test.reason || end.CancelledInflightCount != 1 {
				t.Fatalf("identity loss end = %+v data=%+v", endEvent, end)
			}
			if len(old.traces) != 0 || old.traceBytes != 0 || old.inflight != 0 {
				t.Fatalf("identity loss retained traces: traces=%d bytes=%d inflight=%d",
					len(old.traces), old.traceBytes, old.inflight)
			}
		})
	}
}

func TestRegisterDebugRoutesIsExactClosedSet(t *testing.T) {
	clock := newDebugTestClock(160_000)
	hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
	mutations, _ := newMutationRepository(newDebugMutationDB(t), clock.Now)
	registrar := &debugRegistrar{}
	if err := RegisterRoutes(registrar, hub, mutations); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /api/debug/session", "POST /api/debug/session", "PUT /api/debug/session/mode",
		"POST /api/debug/session/stop", "POST /api/debug/session/replace", "GET /api/debug/events",
	}
	if len(registrar.routes) != len(want) {
		t.Fatalf("route count = %d", len(registrar.routes))
	}
	for index, route := range registrar.routes {
		if got := route.method + " " + route.pattern; got != want[index] || route.handler == nil {
			t.Fatalf("route %d = %q", index, got)
		}
	}
}

func TestDebugEventsHTTPStrictHeadersAndObserverCleanup(t *testing.T) {
	clock := newDebugTestClock(170_000)
	hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
	mutations, _ := newMutationRepository(newDebugMutationDB(t), clock.Now)
	api, _ := NewHTTPAPI(hub, mutations)
	principal := UserPrincipal{UserID: 170, SessionBinding: "binding-170"}
	mustStartDebug(t, hub, principal.UserID, principal.SessionBinding)
	addDryTrace(t, hub, principal.UserID)

	missingAccept := httptest.NewRecorder()
	api.events(missingAccept, httptest.NewRequest(http.MethodGet, "/api/debug/events", nil), principal)
	if missingAccept.Code != http.StatusBadRequest {
		t.Fatalf("missing Accept = %d %s", missingAccept.Code, missingAccept.Body.String())
	}
	duplicate := httptest.NewRequest(http.MethodGet, "/api/debug/events", nil)
	duplicate.Header.Set("Accept", "text/event-stream")
	duplicate.Header.Add("Last-Event-ID", testOpaqueID("dbe_", 1))
	duplicate.Header.Add("Last-Event-ID", testOpaqueID("dbe_", 2))
	duplicateRecorder := httptest.NewRecorder()
	api.events(duplicateRecorder, duplicate, principal)
	if duplicateRecorder.Code != http.StatusBadRequest {
		t.Fatalf("duplicate Last-Event-ID = %d %s", duplicateRecorder.Code, duplicateRecorder.Body.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := httptest.NewRequest(http.MethodGet, "/api/debug/events", nil).WithContext(ctx)
	stream.Header.Set("Accept", "application/json, text/event-stream; charset=utf-8")
	stream.Header.Set("Last-Event-ID", "not-an-event")
	streamRecorder := httptest.NewRecorder()
	api.events(streamRecorder, stream, principal)
	body := streamRecorder.Body.String()
	if streamRecorder.Code != http.StatusOK || streamRecorder.Header().Get("X-Nonbiri-Event-Version") != "2" ||
		strings.Count(body, "event: gap\n") != 1 || strings.Count(body, "event: snapshot\n") != 1 {
		t.Fatalf("SSE recovery = %d headers=%v body=%q", streamRecorder.Code, streamRecorder.Header(), body)
	}
	noForbiddenDebugWire(t, streamRecorder.Body.Bytes(), "authorization", "set-cookie", "raw-upstream", "secret")
	metadata, err := hub.Metadata(principal.UserID)
	if err != nil || !metadata.Active || metadata.ConnectedSubscribers != 0 {
		t.Fatalf("observer cleanup = (%+v,%v)", metadata, err)
	}
}
