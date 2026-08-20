package forward

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// addRouteCfg is addRoute with an explicit route strategy and silent_retry
// switch, so retry/routing tests can build models that fail over.
func (f *forwardFixture) addRouteCfg(t *testing.T, userID int64, baseURL, provider, model, upstreamModel, upstreamSecret string, ord int64, strategy string, silentRetry bool) fixtureRoute {
	t.Helper()
	canonical, err := f.stack.ValidateBaseURL(baseURL)
	if err != nil {
		t.Fatalf("ValidateBaseURL: %v", err)
	}
	endpointRow, err := f.store.CreateEndpoint(context.Background(), userID, string(endpoint.ConnectorOpenAICompatible), canonical, "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	keyRow, err := f.store.CreateEndpointKey(context.Background(), userID, endpointRow.ID, []byte(upstreamSecret), "head", "tail", "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := f.store.GetEndpointKeyCiphertext(context.Background(), userID, endpointRow.ID, keyRow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceFetchedModels(context.Background(), userID, endpointRow.ID, keyRow.ID, []db.FetchedModel{{
		EndpointKeyID: keyRow.ID, UpstreamModelID: upstreamModel, Provider: "upstream", Status: "ok",
	}}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	modelRow, err := f.store.CreateModel(context.Background(), userID, provider, model, strategy, silentRetry, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	binding, err := f.store.CreateBinding(context.Background(), userID, modelRow.ID, keyRow.ID, upstreamModel, ord, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	return fixtureRoute{
		modelID: modelRow.ID, endpoint: endpointRow.ID, keyID: keyRow.ID,
		bindingID: binding.ID, cipher: ciphertext, fullName: provider + "/" + model,
	}
}

// addBindingToModel attaches another (Endpoint, Key, fetched cache, binding)
// to an existing platform model, pointing at a different upstream server.
func (f *forwardFixture) addBindingToModel(t *testing.T, userID, modelID int64, baseURL, upstreamSecret, upstreamModel string, ord int64) int64 {
	t.Helper()
	canonical, err := f.stack.ValidateBaseURL(baseURL)
	if err != nil {
		t.Fatalf("ValidateBaseURL: %v", err)
	}
	endpointRow, err := f.store.CreateEndpoint(context.Background(), userID, string(endpoint.ConnectorOpenAICompatible), canonical, "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	keyRow, err := f.store.CreateEndpointKey(context.Background(), userID, endpointRow.ID, []byte(upstreamSecret), "head", "tail", "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceFetchedModels(context.Background(), userID, endpointRow.ID, keyRow.ID, []db.FetchedModel{{
		EndpointKeyID: keyRow.ID, UpstreamModelID: upstreamModel, Provider: "upstream", Status: "ok",
	}}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	binding, err := f.store.CreateBinding(context.Background(), userID, modelID, keyRow.ID, upstreamModel, ord, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	return binding.ID
}

type failServer struct {
	server *httptest.Server
	hits   atomic.Int32
}

func newFailingUpstream(status int, body string) *failServer {
	s := &failServer{}
	s.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		s.hits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		http.Error(writer, body, status)
	}))
	return s
}

func newSuccessUpstream(content string) *failServer {
	s := &failServer{}
	s.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		s.hits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, forwardCompletion(content))
	}))
	return s
}

func (s *failServer) close() { s.server.Close() }

type fixedRouteRepository struct {
	route db.ForwardRoute
}

func (r fixedRouteRepository) ListCallerModels(context.Context, int64, int) ([]db.CallerModel, error) {
	return nil, nil
}

func (r fixedRouteRepository) ResolveForwardRoute(context.Context, int64, string, int) (db.ForwardRoute, error) {
	return r.route, nil
}

type attemptRunnerFunc func(context.Context, http.ResponseWriter, AttemptInput) openai.AttemptResult

func (f attemptRunnerFunc) Run(ctx context.Context, writer http.ResponseWriter, input AttemptInput) openai.AttemptResult {
	return f(ctx, writer, input)
}

func TestForwardAggregateTimeoutReturnsStablePreCommitFailure(t *testing.T) {
	const userID int64 = 42
	repository := fixedRouteRepository{route: db.ForwardRoute{
		ModelID: 1, UserID: userID, FullName: "p/m", RouteStrategy: "ordered", SilentRetry: true,
		Candidates: []db.ForwardCandidate{{
			BindingID: 1, ModelID: 1, EndpointID: 1, EndpointKeyID: 1,
			UpstreamModelID: "upstream/model",
		}},
	}}
	runner := attemptRunnerFunc(func(ctx context.Context, _ http.ResponseWriter, _ AttemptInput) openai.AttemptResult {
		<-ctx.Done()
		return openai.AttemptResult{Failure: openai.FailureCanceled, Diagnostic: "request canceled"}
	})
	service, err := NewService(ServiceConfig{
		Repository:     repository,
		Runner:         runner,
		Backoff:        BackoffConfig{Base: -1, Max: -1},
		ForwardTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	result, err := service.Forward(context.Background(), httptest.NewRecorder(), userID, &openai.ChatRequest{Model: "p/m"})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 10*time.Millisecond || elapsed > time.Second {
		t.Fatalf("aggregate timeout elapsed=%v", elapsed)
	}
	if result.Success || result.Committed || result.Failure != openai.FailureUpstream ||
		result.ClientStatus != http.StatusBadGateway || result.Diagnostic != "forward request timed out" {
		t.Fatalf("aggregate timeout result=%+v", result)
	}

	if _, err := NewService(ServiceConfig{Repository: repository, Runner: runner, ForwardTimeout: -time.Second}); err == nil {
		t.Fatal("negative aggregate timeout was accepted")
	}
}

// TestRetryBoundaryClassification pins the retry boundary state machine: only
// a pre-commit upstream failure under silent_retry is retryable.
func TestRetryBoundaryClassification(t *testing.T) {
	preCommitUpstream := openai.AttemptResult{Failure: openai.FailureUpstream}
	committedUpstream := openai.AttemptResult{Failure: openai.FailureUpstream, Committed: true}
	canceled := openai.AttemptResult{Failure: openai.FailureCanceled}
	sink := openai.AttemptResult{Failure: openai.FailureSink}
	internal := openai.AttemptResult{Failure: openai.FailureInternal}
	success := openai.AttemptResult{Success: true}

	cases := []struct {
		name        string
		result      openai.AttemptResult
		silentRetry bool
		want        bool
	}{
		{"pre-commit upstream with retry", preCommitUpstream, true, true},
		{"pre-commit upstream no retry", preCommitUpstream, false, false},
		{"committed upstream with retry", committedUpstream, true, false},
		{"canceled with retry", canceled, true, false},
		{"sink with retry", sink, true, false},
		{"internal with retry", internal, true, false},
		{"success with retry", success, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryable(tc.result, tc.silentRetry); got != tc.want {
				t.Fatalf("isRetryable(%+v, %v) = %v, want %v", tc.result, tc.silentRetry, got, tc.want)
			}
		})
	}
}

// TestBackoffWaitBoundedAndCancelable verifies the wait is finite, respects
// ctx cancellation, and never spawns a goroutine.
func TestBackoffWaitBoundedAndCancelable(t *testing.T) {
	cfg := BackoffConfig{Base: 10 * time.Millisecond, Max: 30 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	if ok := cfg.wait(ctx, 0); !ok {
		t.Fatal("wait returned false without cancellation")
	}
	if elapsed := time.Since(start); elapsed < 9*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Fatalf("base wait elapsed = %v, want ~10ms", elapsed)
	}

	// Cancellation during the wait aborts immediately.
	cancelCtx, cancelWait := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancelWait()
	}()
	start = time.Now()
	if ok := cfg.wait(cancelCtx, 2); ok {
		t.Fatal("canceled wait returned true")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("canceled wait elapsed = %v, want prompt abort", elapsed)
	}

	// Base <= 0 disables waiting entirely.
	noop := BackoffConfig{Base: 0, Max: 0}
	if !noop.wait(context.Background(), 0) {
		t.Fatal("disabled backoff should proceed")
	}

	// The wait caps at Max for large indices (no overflow / infinite sleep).
	big := BackoffConfig{Base: 5 * time.Millisecond, Max: 12 * time.Millisecond}
	start = time.Now()
	big.wait(context.Background(), 30)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("capped wait elapsed = %v, want <= Max-ish", elapsed)
	}
}

// TestShuffleCandidatesDistribution verifies the permutation covers every
// position and does not mutate the input or share state.
func TestShuffleCandidatesDistribution(t *testing.T) {
	const n = 6
	candidates := make([]db.ForwardCandidate, n)
	for i := range candidates {
		candidates[i] = db.ForwardCandidate{BindingID: int64(i + 1)}
	}
	first := make(map[int64]int, n)
	const iterations = 4000
	for range iterations {
		out := shuffleCandidates(candidates)
		if len(out) != n {
			t.Fatalf("shuffle length = %d, want %d", len(out), n)
		}
		first[out[0].BindingID]++
		// Input is not mutated.
		for i, c := range candidates {
			if c.BindingID != int64(i+1) {
				t.Fatal("shuffleCandidates mutated its input")
			}
		}
	}
	if len(first) != n {
		t.Fatalf("first-position coverage = %d bindings, want %d", len(first), n)
	}
	for id, count := range first {
		if count < iterations/n/4 {
			t.Fatalf("binding %d first-position count = %d, too skewed", id, count)
		}
	}
}

// TestOrderedRouteFailoverOrderAndHooks verifies ordered failover visits
// candidates in (ord, id) order, emits a failover per advance, and commits
// usage exactly once for the successful attempt.
func TestOrderedRouteFailoverOrderAndHooks(t *testing.T) {
	first := newFailingUpstream(http.StatusServiceUnavailable, "first-failed")
	second := newFailingUpstream(http.StatusBadGateway, "second-failed")
	third := newSuccessUpstream("third-ok")
	defer first.close()
	defer second.close()
	defer third.close()

	var mu sync.Mutex
	var attempts []AttemptRecord
	var usages []UsageRecord
	var failovers []FailoverRecord
	fixture := newForwardFixture(t, []string{first.server.URL, second.server.URL, third.server.URL}, Hooks{
		Attempt:  func(r AttemptRecord) { mu.Lock(); attempts = append(attempts, r); mu.Unlock() },
		Usage:    func(r UsageRecord) { mu.Lock(); usages = append(usages, r); mu.Unlock() },
		Failover: func(r FailoverRecord) { mu.Lock(); failovers = append(failovers, r); mu.Unlock() },
	}, nil, nil)
	user := fixture.addUser(t, "ordered")
	route := fixture.addRouteCfg(t, user.id, first.server.URL, "p", "m", "upstream/model", "sk-first", 10, "ordered", true)
	b2 := fixture.addBindingToModel(t, user.id, route.modelID, second.server.URL, "sk-second", "upstream/model", 20)
	b3 := fixture.addBindingToModel(t, user.id, route.modelID, third.server.URL, "sk-third", "upstream/model", 30)

	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[]}`))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "third-ok") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if first.hits.Load() != 1 || second.hits.Load() != 1 || third.hits.Load() != 1 {
		t.Fatalf("hits first=%d second=%d third=%d", first.hits.Load(), second.hits.Load(), third.hits.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 3 || len(usages) != 1 || len(failovers) != 2 {
		t.Fatalf("attempts=%d usages=%d failovers=%d", len(attempts), len(usages), len(failovers))
	}
	wantOrder := []int64{route.bindingID, b2, b3}
	for i, want := range wantOrder {
		if attempts[i].BindingID != want || attempts[i].AttemptIndex != i {
			t.Fatalf("attempt %d = %+v, want binding %d index %d", i, attempts[i], want, i)
		}
	}
	if !attempts[2].Success || usages[0].BindingID != b3 || !usages[0].Usage.Present {
		t.Fatalf("success/usage mismatch attempts=%+v usages=%+v", attempts, usages)
	}
	id := attempts[0].AttemptID
	for _, a := range attempts {
		if a.AttemptID != id {
			t.Fatal("attempt ids are not shared across the invocation")
		}
	}
	for i, f := range failovers {
		if f.AttemptID != id || f.BindingID != wantOrder[i] || f.StableErrorCode != "upstream" || f.AttemptIndex != i {
			t.Fatalf("failover %d = %+v", i, f)
		}
	}
}

// TestOrderedStopsAtFirstSuccess verifies ordered routing does not dial later
// candidates once an earlier one succeeds.
func TestOrderedStopsAtFirstSuccess(t *testing.T) {
	first := newSuccessUpstream("first-ok")
	second := newSuccessUpstream("second-ok")
	defer first.close()
	defer second.close()
	var attempts []AttemptRecord
	var failovers []FailoverRecord
	var mu sync.Mutex
	fixture := newForwardFixture(t, []string{first.server.URL, second.server.URL}, Hooks{
		Attempt:  func(r AttemptRecord) { mu.Lock(); attempts = append(attempts, r); mu.Unlock() },
		Failover: func(r FailoverRecord) { mu.Lock(); failovers = append(failovers, r); mu.Unlock() },
	}, nil, nil)
	user := fixture.addUser(t, "ordered-stop")
	route := fixture.addRouteCfg(t, user.id, first.server.URL, "p", "m", "upstream/model", "sk-first", 10, "ordered", true)
	fixture.addBindingToModel(t, user.id, route.modelID, second.server.URL, "sk-second", "upstream/model", 20)

	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[]}`))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "first-ok") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if first.hits.Load() != 1 || second.hits.Load() != 0 {
		t.Fatalf("hits first=%d second=%d", first.hits.Load(), second.hits.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 1 || len(failovers) != 0 {
		t.Fatalf("attempts=%d failovers=%d", len(attempts), len(failovers))
	}
}

// TestSilentRetryOffRunsOneAttempt verifies the default (silent_retry off)
// runs exactly one attempt and returns upstream on a pre-commit failure.
func TestSilentRetryOffRunsOneAttempt(t *testing.T) {
	first := newFailingUpstream(http.StatusServiceUnavailable, "first-failed-SECRET")
	second := newSuccessUpstream("second-ok")
	defer first.close()
	defer second.close()
	var attempts []AttemptRecord
	var failovers []FailoverRecord
	var mu sync.Mutex
	fixture := newForwardFixture(t, []string{first.server.URL, second.server.URL}, Hooks{
		Attempt:  func(r AttemptRecord) { mu.Lock(); attempts = append(attempts, r); mu.Unlock() },
		Failover: func(r FailoverRecord) { mu.Lock(); failovers = append(failovers, r); mu.Unlock() },
	}, nil, nil)
	user := fixture.addUser(t, "retry-off")
	route := fixture.addRouteCfg(t, user.id, first.server.URL, "p", "m", "upstream/model", "sk-first", 0, "ordered", false)
	fixture.addBindingToModel(t, user.id, route.modelID, second.server.URL, "sk-second", "upstream/model", 1)

	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[]}`))
	if response.Code != http.StatusBadGateway || responseCode(t, response) != httperr.CodeUpstream {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "first-failed-SECRET") || strings.Contains(response.Body.String(), "sk-first") {
		t.Fatalf("upstream failure leaked content: %s", response.Body.String())
	}
	if first.hits.Load() != 1 || second.hits.Load() != 0 {
		t.Fatalf("hits first=%d second=%d", first.hits.Load(), second.hits.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 1 || len(failovers) != 0 {
		t.Fatalf("attempts=%d failovers=%d", len(attempts), len(failovers))
	}
}

// TestSilentRetryOnAllExhaustedReturnsUpstream verifies that exhausting every
// candidate under silent retry returns a stable upstream error without leaking
// raw upstream bodies or credentials.
func TestSilentRetryOnAllExhaustedReturnsUpstream(t *testing.T) {
	first := newFailingUpstream(http.StatusServiceUnavailable, "RAW-FIRST-SECRET-MARKER")
	second := newFailingUpstream(http.StatusBadGateway, "RAW-SECOND-SECRET-MARKER")
	defer first.close()
	defer second.close()
	var attempts []AttemptRecord
	var failovers []FailoverRecord
	var mu sync.Mutex
	fixture := newForwardFixture(t, []string{first.server.URL, second.server.URL}, Hooks{
		Attempt:  func(r AttemptRecord) { mu.Lock(); attempts = append(attempts, r); mu.Unlock() },
		Failover: func(r FailoverRecord) { mu.Lock(); failovers = append(failovers, r); mu.Unlock() },
	}, nil, nil)
	user := fixture.addUser(t, "retry-exhausted")
	route := fixture.addRouteCfg(t, user.id, first.server.URL, "p", "m", "upstream/model", "sk-first-SECRET", 0, "ordered", true)
	fixture.addBindingToModel(t, user.id, route.modelID, second.server.URL, "sk-second-SECRET", "upstream/model", 1)

	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[]}`))
	if response.Code != http.StatusBadGateway || responseCode(t, response) != httperr.CodeUpstream {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{"RAW-FIRST-SECRET-MARKER", "RAW-SECOND-SECRET-MARKER", "sk-first-SECRET", "sk-second-SECRET", route.cipher} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("exhausted response leaked %q: %s", forbidden, body)
		}
	}
	if first.hits.Load() != 1 || second.hits.Load() != 1 {
		t.Fatalf("hits first=%d second=%d", first.hits.Load(), second.hits.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 2 || len(failovers) != 1 {
		t.Fatalf("attempts=%d failovers=%d", len(attempts), len(failovers))
	}
}

// TestNoRetryAfterStreamCommit verifies that once a streaming attempt commits a
// body byte, a later mid-stream failure is not retried to another candidate.
func TestNoRetryAfterStreamCommit(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		serveForwardSSE(writer, "data: "+forwardChunk("partial")+"\n\n", `data: {"error":{"message":"RAW-STREAM-ERROR-MARKER"}}`+"\n\n")
	}))
	second := newSuccessUpstream("second-ok")
	defer first.Close()
	defer second.close()
	var attempts []AttemptRecord
	var failovers []FailoverRecord
	var mu sync.Mutex
	fixture := newForwardFixture(t, []string{first.URL, second.server.URL}, Hooks{
		Attempt:  func(r AttemptRecord) { mu.Lock(); attempts = append(attempts, r); mu.Unlock() },
		Failover: func(r FailoverRecord) { mu.Lock(); failovers = append(failovers, r); mu.Unlock() },
	}, nil, nil)
	user := fixture.addUser(t, "stream-commit")
	route := fixture.addRouteCfg(t, user.id, first.URL, "p", "m", "upstream/model", "sk-first", 0, "ordered", true)
	fixture.addBindingToModel(t, user.id, route.modelID, second.server.URL, "sk-second", "upstream/model", 1)

	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[],"stream":true}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "partial") || !strings.Contains(body, `"error"`) {
		t.Fatalf("expected committed stream + in-stream error: %q", body)
	}
	if strings.Contains(body, "RAW-STREAM-ERROR-MARKER") || strings.Contains(body, "second-ok") {
		t.Fatalf("stream leaked raw error or retried to second: %q", body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 1 || len(failovers) != 0 {
		t.Fatalf("committed stream retried: attempts=%d failovers=%d", len(attempts), len(failovers))
	}
	if attempts[0].Committed != true {
		t.Fatalf("committed stream attempt not marked committed: %+v", attempts[0])
	}
}

// TestNoRetryOnCancel verifies a client cancellation during an attempt stops
// the loop without retrying or writing a terminal frame.
func TestNoRetryOnCancel(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		serveForwardSSE(writer, "data: "+forwardChunk("partial")+"\n\n")
		close(started)
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	second := newSuccessUpstream("second-ok")
	defer first.Close()
	defer second.close()
	defer close(release)
	var attempts []AttemptRecord
	var failovers []FailoverRecord
	var mu sync.Mutex
	fixture := newForwardFixture(t, []string{first.URL, second.server.URL}, Hooks{
		Attempt:  func(r AttemptRecord) { mu.Lock(); attempts = append(attempts, r); mu.Unlock() },
		Failover: func(r FailoverRecord) { mu.Lock(); failovers = append(failovers, r); mu.Unlock() },
	}, nil, nil)
	user := fixture.addUser(t, "cancel")
	route := fixture.addRouteCfg(t, user.id, first.URL, "p", "m", "upstream/model", "sk-first", 0, "ordered", true)
	fixture.addBindingToModel(t, user.id, route.modelID, second.server.URL, "sk-second", "upstream/model", 1)

	request := callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","stream":true}`)
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		fixture.handler.ServeHTTP(recorder, request)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first upstream did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not return after cancel")
	}
	if strings.Contains(recorder.Body.String(), "[DONE]") || strings.Contains(recorder.Body.String(), "second-ok") {
		t.Fatalf("canceled request wrote terminal/retried frame: %q", recorder.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 1 || len(failovers) != 0 {
		t.Fatalf("cancel retried: attempts=%d failovers=%d", len(attempts), len(failovers))
	}
	if attempts[0].BindingID != route.bindingID {
		t.Fatalf("cancel attempted wrong binding: %+v", attempts[0])
	}
}

// TestNoRetryOnCancelDuringBackoff verifies that canceling during the backoff
// between two attempts stops the loop and records no failover (no actual
// advance happened).
func TestNoRetryOnCancelDuringBackoff(t *testing.T) {
	first := newFailingUpstream(http.StatusServiceUnavailable, "first-failed")
	second := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("second upstream dialed after cancel during backoff")
	}))
	defer first.close()
	defer second.Close()
	var attempts []AttemptRecord
	var failovers []FailoverRecord
	var mu sync.Mutex
	fixture := newForwardFixtureBackoff(t, []string{first.server.URL, second.URL}, Hooks{
		Attempt:  func(r AttemptRecord) { mu.Lock(); attempts = append(attempts, r); mu.Unlock() },
		Failover: func(r FailoverRecord) { mu.Lock(); failovers = append(failovers, r); mu.Unlock() },
	}, nil, BackoffConfig{Base: 120 * time.Millisecond, Max: 300 * time.Millisecond})
	user := fixture.addUser(t, "cancel-backoff")
	route := fixture.addRouteCfg(t, user.id, first.server.URL, "p", "m", "upstream/model", "sk-first", 0, "ordered", true)
	fixture.addBindingToModel(t, user.id, route.modelID, second.URL, "sk-second", "upstream/model", 1)

	request := callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[]}`)
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		fixture.handler.ServeHTTP(recorder, request)
		close(done)
	}()
	// Wait for the first (fast-failing) attempt to land, then cancel during
	// the 120ms backoff before the second attempt.
	waitFor := func(target int32, timeout time.Duration) {
		deadline := time.After(timeout)
		for {
			if first.hits.Load() >= target {
				return
			}
			select {
			case <-deadline:
				t.Fatalf("first hits=%d after timeout", first.hits.Load())
			default:
				time.Sleep(2 * time.Millisecond)
			}
		}
	}
	waitFor(1, time.Second)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not return after cancel during backoff")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 1 || len(failovers) != 0 {
		t.Fatalf("cancel-during-backoff retried or recorded failover: attempts=%d failovers=%d", len(attempts), len(failovers))
	}
}

// TestRevalidationRaceAdvancesToNextNoDial verifies that when a selected
// binding is disabled between selection and dispatch, silent retry advances to
// the next candidate without dialing the vanished one.
func TestRevalidationRaceAdvancesToNextNoDial(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("disabled binding dialed upstream")
	}))
	second := newSuccessUpstream("second-ok")
	defer first.Close()
	defer second.close()
	var fixture *forwardFixture
	selector := selectorFunc(func(_ context.Context, selection Selection) ([]int64, error) {
		firstCandidate := selection.Candidates[0]
		if _, err := fixture.store.DB().Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, firstCandidate.EndpointKeyID); err != nil {
			return nil, err
		}
		ids := make([]int64, len(selection.Candidates))
		for i, c := range selection.Candidates {
			ids[i] = c.BindingID
		}
		return ids, nil
	})
	fixture = newForwardFixture(t, []string{first.URL, second.server.URL}, Hooks{}, selector, nil)
	user := fixture.addUser(t, "revalidation")
	route := fixture.addRouteCfg(t, user.id, first.URL, "p", "m", "upstream/model", "sk-first", 0, "ordered", true)
	fixture.addBindingToModel(t, user.id, route.modelID, second.server.URL, "sk-second", "upstream/model", 1)
	fixture.codec.opens.Store(0)

	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[]}`))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "second-ok") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	// The disabled (vanished) binding is never decrypted or dialed; only the
	// second binding's credential is opened.
	if fixture.codec.opens.Load() != 1 {
		t.Fatalf("opens=%d, want exactly 1 (only the advanced binding)", fixture.codec.opens.Load())
	}
}

// TestRevalidationCacheClearedAdvancesToNext verifies the same failover when a
// binding's fetched cache is removed between selection and dispatch.
func TestRevalidationCacheClearedAdvancesToNext(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("uncached binding dialed upstream")
	}))
	second := newSuccessUpstream("second-ok")
	defer first.Close()
	defer second.close()
	var fixture *forwardFixture
	selector := selectorFunc(func(_ context.Context, selection Selection) ([]int64, error) {
		firstCandidate := selection.Candidates[0]
		if _, err := fixture.store.DB().Exec(`DELETE FROM fetched_models WHERE endpoint_key_id=?`, firstCandidate.EndpointKeyID); err != nil {
			return nil, err
		}
		ids := make([]int64, len(selection.Candidates))
		for i, c := range selection.Candidates {
			ids[i] = c.BindingID
		}
		return ids, nil
	})
	fixture = newForwardFixture(t, []string{first.URL, second.server.URL}, Hooks{}, selector, nil)
	user := fixture.addUser(t, "revalidation-cache")
	route := fixture.addRouteCfg(t, user.id, first.URL, "p", "m", "upstream/model", "sk-first", 0, "ordered", true)
	fixture.addBindingToModel(t, user.id, route.modelID, second.server.URL, "sk-second", "upstream/model", 1)

	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[]}`))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "second-ok") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// TestFailoverHookBoundedMetadataNoLeak verifies the failover hook carries only
// the bounded non-sensitive metadata and never the URL, credential, raw
// upstream body, or request content.
func TestFailoverHookBoundedMetadataNoLeak(t *testing.T) {
	first := newFailingUpstream(http.StatusServiceUnavailable, "RAW-UPSTREAM-BODY-MARKER")
	second := newSuccessUpstream("second-ok")
	defer first.close()
	defer second.close()
	var failovers []FailoverRecord
	var mu sync.Mutex
	fixture := newForwardFixture(t, []string{first.server.URL, second.server.URL}, Hooks{
		Failover: func(r FailoverRecord) { mu.Lock(); failovers = append(failovers, r); mu.Unlock() },
	}, nil, nil)
	user := fixture.addUser(t, "hook-leak")
	route := fixture.addRouteCfg(t, user.id, first.server.URL, "p", "m", "upstream/model", "sk-SECRET-KEY", 0, "ordered", true)
	fixture.addBindingToModel(t, user.id, route.modelID, second.server.URL, "sk-other-SECRET", "upstream/model", 1)

	requestBody := `{"model":"p/m","messages":[{"role":"user","content":"PRIVATE-REQUEST-MARKER"}]}`
	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, requestBody))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(failovers) != 1 {
		t.Fatalf("failovers=%d, want 1", len(failovers))
	}
	// The record must carry only the bounded metadata fields.
	r := failovers[0]
	if r.StableErrorCode != "upstream" || r.BindingID != route.bindingID || r.AttemptIndex != 0 || r.FullName != "p/m" {
		t.Fatalf("failover record = %+v", r)
	}
	// None of the sensitive material may appear in any field of the record.
	for _, forbidden := range []string{"RAW-UPSTREAM-BODY-MARKER", "sk-SECRET-KEY", "sk-other-SECRET", route.cipher, "PRIVATE-REQUEST-MARKER", first.server.URL, second.server.URL, "169.254"} {
		if strings.Contains(r.AttemptID, forbidden) ||
			strings.Contains(strconv.FormatInt(r.UserID, 10), forbidden) ||
			strings.Contains(strconv.FormatInt(r.ModelID, 10), forbidden) ||
			strings.Contains(r.FullName, forbidden) ||
			strings.Contains(strconv.FormatInt(r.BindingID, 10), forbidden) ||
			strings.Contains(r.StableErrorCode, forbidden) {
			t.Fatalf("failover record leaked %q: %+v", forbidden, r)
		}
	}
}

// TestZeroCandidatesUnboundModel verifies a draft model with no usable binding
// returns unbound_model without dialing.
func TestZeroCandidatesUnboundModel(t *testing.T) {
	upstream := newSuccessUpstream("never")
	defer upstream.close()
	var attempts []AttemptRecord
	var mu sync.Mutex
	fixture := newForwardFixture(t, []string{upstream.server.URL}, Hooks{
		Attempt: func(r AttemptRecord) { mu.Lock(); attempts = append(attempts, r); mu.Unlock() },
	}, nil, nil)
	user := fixture.addUser(t, "draft")
	if _, err := fixture.store.CreateModel(context.Background(), user.id, "p", "m", "ordered", true, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[]}`))
	if response.Code != http.StatusServiceUnavailable || responseCode(t, response) != httperr.CodeUnboundModel {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if upstream.hits.Load() != 0 {
		t.Fatalf("draft model dialed upstream %d times", upstream.hits.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 0 {
		t.Fatalf("draft model fired attempts=%d", len(attempts))
	}
}

// TestSelectorEmptyOrderUnboundModel verifies a selector returning an empty
// order yields unbound_model rather than an internal error.
func TestSelectorEmptyOrderUnboundModel(t *testing.T) {
	upstream := newSuccessUpstream("never")
	defer upstream.close()
	selector := selectorFunc(func(context.Context, Selection) ([]int64, error) { return nil, nil })
	fixture := newForwardFixture(t, []string{upstream.server.URL}, Hooks{}, selector, nil)
	user := fixture.addUser(t, "empty-order")
	fixture.addRouteCfg(t, user.id, upstream.server.URL, "p", "m", "upstream/model", "sk", 0, "ordered", true)
	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[]}`))
	if response.Code != http.StatusServiceUnavailable || responseCode(t, response) != httperr.CodeUnboundModel {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

// TestRandomRouteStatelessDistribution verifies random routing is stateless:
// each call independently permutes the candidates, every candidate appears in
// the first position across many calls, and concurrent calls do not share
// routing state (race-checked).
func TestRandomRouteStatelessDistribution(t *testing.T) {
	const bindings = 3
	servers := make([]*failServer, bindings)
	urls := make([]string, bindings)
	for i := range servers {
		servers[i] = newFailingUpstream(http.StatusServiceUnavailable, "fail-"+strconv.Itoa(i))
		urls[i] = servers[i].server.URL
	}
	defer func() {
		for _, s := range servers {
			s.close()
		}
	}()

	var mu sync.Mutex
	firstPicks := make(map[int64]int)
	bindingIDs := make([]int64, 0, bindings)

	var callBufMu sync.Mutex
	var callBuf []int64
	fixture := newForwardFixture(t, urls, Hooks{
		Attempt: func(r AttemptRecord) {
			if r.AttemptIndex == 0 {
				callBufMu.Lock()
				callBuf = append(callBuf, r.BindingID)
				callBufMu.Unlock()
			}
		},
	}, nil, nil)
	user := fixture.addUser(t, "random")
	route := fixture.addRouteCfg(t, user.id, urls[0], "p", "m", "upstream/model", "sk-0", 0, "random", true)
	bindingIDs = append(bindingIDs, route.bindingID)
	for i := 1; i < bindings; i++ {
		bindingIDs = append(bindingIDs, fixture.addBindingToModel(t, user.id, route.modelID, urls[i], "sk-"+strconv.Itoa(i), "upstream/model", int64(i)))
	}

	const total = 360
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range total / 2 {
				performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[]}`))
			}
		}()
	}
	wg.Wait()

	callBufMu.Lock()
	picks := callBuf
	callBufMu.Unlock()
	if len(picks) != total {
		t.Fatalf("recorded first-attempts = %d, want %d", len(picks), total)
	}
	mu.Lock()
	for _, b := range picks {
		firstPicks[b]++
	}
	mu.Unlock()

	if len(firstPicks) != bindings {
		t.Fatalf("random first-position coverage = %d bindings, want %d", len(firstPicks), bindings)
	}
	for _, id := range bindingIDs {
		if firstPicks[id] < total/bindings/5 {
			t.Fatalf("random binding %d picked first only %d/%d, too skewed", id, firstPicks[id], total)
		}
	}
	// Not all first-picks are the same binding (rules out ordered/deterministic).
	if len(firstPicks) == 1 {
		t.Fatalf("random routing is deterministic: firstPicks=%v", firstPicks)
	}
}
