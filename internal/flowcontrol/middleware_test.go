package flowcontrol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

func newTestMiddleware(t *testing.T, config ratelimit.RPMConfig, resolver UserLimitResolver, clock *fakeClock) (*Middleware, *Controller) {
	t.Helper()
	controller := newTestController(t, config, resolver, clock)
	middleware, err := NewMiddleware(controller, func(_ *http.Request) (int64, error) {
		return 7, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return middleware, controller
}

func call(handler http.Handler, method, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "https://gateway.example"+path, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestMiddlewareMetersOnlyChatCompletions(t *testing.T) {
	var calls atomicCount
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusOK)
	})
	middleware, controller := newTestMiddleware(t, testRPMConfig(), nil, nil)
	handler := middleware.Wrap(next)

	// Metered: POST /v1/chat/completions consumes one reservation (committed
	// after the empty-body handler below; no body bytes are written so the
	// reservation is released, which is exactly what the no-output handler
	// deserves).
	recorder := call(handler, http.MethodPost, "/v1/chat/completions")
	if recorder.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("metered request status=%d calls=%d", recorder.Code, calls.Load())
	}
	if decision, err := controller.limiter.Check("7"); err != nil || decision.GlobalCount != 0 || decision.UserCount != 0 {
		t.Fatalf("no-output request must not consume budget: %#v %v", decision, err)
	}

	// Unmetered passthroughs: model listing, method-not-allowed on the chat
	// path, and non-forward paths never touch the limiter. Each passthrough
	// adds exactly one downstream call and no RPM event.
	for index, request := range []struct{ method, path string }{
		{http.MethodGet, "/v1/chat/completions"},
		{http.MethodPost, "/v1/models"},
		{http.MethodGet, "/v1/models"},
		{http.MethodPost, "/healthz"},
	} {
		recorder := call(handler, request.method, request.path)
		want := int64(index + 2) // one metered call plus the passthroughs so far
		if recorder.Code != http.StatusOK || calls.Load() != want {
			t.Fatalf("%s %s must pass through unmetered: status=%d calls=%d want=%d", request.method, request.path, recorder.Code, calls.Load(), want)
		}
	}
	if decision, err := controller.limiter.Check("7"); err != nil || decision.GlobalCount != 0 {
		t.Fatalf("passthrough requests must not consume budget: %#v %v", decision, err)
	}
}

func TestMiddlewareRateLimitedResponseIsStableAndBounded(t *testing.T) {
	config := testRPMConfig()
	config.GlobalLimit = 1
	config.PerUserLimit = 1
	var downstreamCalls atomicCount
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		downstreamCalls.Add(1)
		_, _ = writer.Write([]byte("ok"))
	})
	middleware, _ := newTestMiddleware(t, config, nil, nil)
	handler := middleware.Wrap(next)

	recorder := call(handler, http.MethodPost, "/v1/chat/completions")
	if recorder.Code != http.StatusOK {
		t.Fatalf("first request status=%d", recorder.Code)
	}
	recorder = call(handler, http.MethodPost, "/v1/chat/completions")
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("denied status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	retryAfter := recorder.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("Retry-After header is required")
	}
	seconds := 0
	if parsed, err := strconv.Atoi(retryAfter); err != nil {
		t.Fatalf("Retry-After must be an integer: %q err=%v", retryAfter, err)
	} else {
		seconds = parsed
	}
	if seconds < 1 || seconds > 3600 {
		t.Fatalf("Retry-After out of bounds: %q", retryAfter)
	}
	var envelope httperr.Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, recorder.Body.String())
	}
	if envelope.Error.Code != httperr.CodeRateLimited || envelope.Error.Diag != "" || envelope.Error.RequestID != "" {
		t.Fatalf("unexpected envelope: %#v", envelope.Error)
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{"GlobalCount", "UserCount", "global", "user", "\"7\"", "diag", "request_id"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("429 body leaks internal state %q: %s", forbidden, body)
		}
	}
	if downstreamCalls.Load() != 1 {
		t.Fatal("denied request must never reach the downstream handler")
	}
}

func TestMiddlewareCommitOnBodyBytesReleaseWithout(t *testing.T) {
	middleware, controller := newTestMiddleware(t, testRPMConfig(), nil, nil)

	wroteBody := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("answer"))
	})
	handler := middleware.Wrap(wroteBody)
	if recorder := call(handler, http.MethodPost, "/v1/chat/completions"); recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	if decision, err := controller.limiter.Check("7"); err != nil || decision.GlobalCount != 1 || decision.UserCount != 1 {
		t.Fatalf("body write must commit the reservation: %#v %v", decision, err)
	}

	noBody := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	middleware2, controller2 := newTestMiddleware(t, testRPMConfig(), nil, nil)
	handler2 := middleware2.Wrap(noBody)
	if recorder := call(handler2, http.MethodPost, "/v1/chat/completions"); recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d", recorder.Code)
	}
	if decision, err := controller2.limiter.Check("7"); err != nil || decision.GlobalCount != 0 || decision.UserCount != 0 {
		t.Fatalf("no-output request must release the reservation: %#v %v", decision, err)
	}
}

func TestMiddlewareClientCancelReleasesBeforeAnyByte(t *testing.T) {
	var downstreamStarted atomicCount
	next := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		downstreamStarted.Add(1)
		<-request.Context().Done()
		// No byte was ever written: the reservation must be released.
	})
	middleware, controller := newTestMiddleware(t, testRPMConfig(), nil, nil)
	handler := middleware.Wrap(next)

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "https://gateway.example/v1/chat/completions", nil)
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()
	go func() {
		for downstreamStarted.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	handler.ServeHTTP(recorder, request)
	if decision, err := controller.limiter.Check("7"); err != nil || decision.GlobalCount != 0 {
		t.Fatalf("cancel before bytes must release: %#v %v", decision, err)
	}
}

func TestMiddlewareCanceledContextBeforeAdmitWritesNothing(t *testing.T) {
	var downstreamCalls atomicCount
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		downstreamCalls.Add(1)
		_, _ = writer.Write([]byte("ok"))
	})
	middleware, _ := newTestMiddleware(t, testRPMConfig(), nil, nil)
	handler := middleware.Wrap(next)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "https://gateway.example/v1/chat/completions", nil)
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Body.Len() != 0 || recorder.Header().Get("Cache-Control") != "" || downstreamCalls.Load() != 0 {
		t.Fatalf("canceled request must abort silently: body=%q cache=%q calls=%d", recorder.Body.String(), recorder.Header().Get("Cache-Control"), downstreamCalls.Load())
	}
}

func TestMiddlewarePanicReleasesOrCommitsByBytes(t *testing.T) {
	t.Run("panic without bytes releases", func(t *testing.T) {
		middleware, controller := newTestMiddleware(t, testRPMConfig(), nil, nil)
		next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom")
		})
		handler := middleware.Wrap(next)
		func() {
			defer func() { _ = recover() }()
			call(handler, http.MethodPost, "/v1/chat/completions")
		}()
		if decision, err := controller.limiter.Check("7"); err != nil || decision.GlobalCount != 0 {
			t.Fatalf("panic without output must release: %#v %v", decision, err)
		}
		// The budget is usable again.
		if _, _, err := controller.Admit(context.Background(), 7); err != nil {
			t.Fatalf("admit after panic release: %v", err)
		}
	})

	t.Run("panic after bytes commits", func(t *testing.T) {
		middleware, controller := newTestMiddleware(t, testRPMConfig(), nil, nil)
		next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte("partial"))
			panic("boom-after-write")
		})
		handler := middleware.Wrap(next)
		func() {
			defer func() { _ = recover() }()
			call(handler, http.MethodPost, "/v1/chat/completions")
		}()
		if decision, err := controller.limiter.Check("7"); err != nil || decision.GlobalCount != 1 {
			t.Fatalf("panic after bytes must commit: %#v %v", decision, err)
		}
	})
}

func TestMiddlewareClosedControllerFailsClosed(t *testing.T) {
	var downstreamCalls atomicCount
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		downstreamCalls.Add(1)
		_, _ = writer.Write([]byte("ok"))
	})
	middleware, controller := newTestMiddleware(t, testRPMConfig(), nil, nil)
	handler := middleware.Wrap(next)
	if err := controller.Close(); err != nil {
		t.Fatal(err)
	}
	recorder := call(handler, http.MethodPost, "/v1/chat/completions")
	if recorder.Code != http.StatusServiceUnavailable || downstreamCalls.Load() != 0 {
		t.Fatalf("closed controller must fail closed: status=%d calls=%d body=%s", recorder.Code, downstreamCalls.Load(), recorder.Body.String())
	}
	var envelope httperr.Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != httperr.CodeServiceUnavailable {
		t.Fatalf("unexpected envelope: %#v err=%v", envelope, err)
	}
}

func TestMiddlewareWithoutIdentityFailsClosed(t *testing.T) {
	var downstreamCalls atomicCount
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		downstreamCalls.Add(1)
		httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
	})
	controller := newTestController(t, testRPMConfig(), nil, nil)
	middleware, err := NewMiddleware(controller, func(_ *http.Request) (int64, error) {
		return 0, http.ErrNoCookie
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := call(middleware.Wrap(next), http.MethodPost, "/v1/chat/completions")
	if recorder.Code != http.StatusServiceUnavailable || downstreamCalls.Load() != 0 {
		t.Fatalf("identity failure must fail closed: status=%d calls=%d", recorder.Code, downstreamCalls.Load())
	}
	if decision, err := controller.limiter.Check("7"); err != nil || decision.GlobalCount != 0 {
		t.Fatalf("identity failure must not consume budget: %#v %v", decision, err)
	}
}

func TestMiddlewareNilWiring(t *testing.T) {
	if _, err := NewMiddleware(nil, func(*http.Request) (int64, error) { return 1, nil }); err == nil {
		t.Fatal("nil controller must be rejected")
	}
	controller := newTestController(t, testRPMConfig(), nil, nil)
	if _, err := NewMiddleware(controller, nil); err == nil {
		t.Fatal("nil identity resolver must be rejected")
	}
	middleware, err := NewMiddleware(controller, func(*http.Request) (int64, error) { return 1, nil })
	if err != nil {
		t.Fatal(err)
	}
	recorder := call(middleware.Wrap(nil), http.MethodPost, "/v1/chat/completions")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil next must fail closed: status=%d", recorder.Code)
	}
}

func TestMiddlewareTrackerPreservesFlusherAndResponseController(t *testing.T) {
	middleware, controller := newTestMiddleware(t, testRPMConfig(), nil, nil)
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		// The streaming connector asserts http.Flusher directly...
		flusher, ok := writer.(http.Flusher)
		if !ok {
			t.Error("tracker must satisfy the direct http.Flusher assertion")
			return
		}
		// ...and the shared code path uses ResponseController for flush.
		controller := http.NewResponseController(writer)
		if err := controller.Flush(); err != nil {
			t.Errorf("ResponseController flush: %v", err)
		}
		flusher.Flush()
		_, _ = writer.Write([]byte("frame"))
	})
	recorder := call(middleware.Wrap(next), http.MethodPost, "/v1/chat/completions")
	if recorder.Code != http.StatusOK || recorder.Body.String() != "frame" {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if decision, err := controller.limiter.Check("7"); err != nil || decision.GlobalCount != 1 {
		t.Fatalf("bytes via tracked writer must commit: %#v %v", decision, err)
	}
}

type atomicCount struct{ value atomic.Int64 }

func (c *atomicCount) Add(delta int64) { c.value.Add(delta) }
func (c *atomicCount) Load() int64     { return c.value.Load() }
