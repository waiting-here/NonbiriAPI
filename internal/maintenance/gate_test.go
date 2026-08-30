package maintenance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type recorderHandler struct {
	mu    sync.Mutex
	paths []string
}

func (handler *recorderHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.mu.Lock()
	handler.paths = append(handler.paths, request.URL.Path)
	handler.mu.Unlock()
	writer.WriteHeader(http.StatusTeapot)
}

func (handler *recorderHandler) reset() {
	handler.mu.Lock()
	handler.paths = nil
	handler.mu.Unlock()
}

func (handler *recorderHandler) count() int {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return len(handler.paths)
}

func decodeGateError(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error: %v; body=%s", err, recorder.Body.String())
	}
	return envelope.Error.Code
}

func requestGate(handler http.Handler, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func TestGateRequiresCommittedInitializationAndUsesStableCodes(t *testing.T) {
	gate := NewGate()
	next := &recorderHandler{}
	handler := GateMiddleware(gate, next)

	recorder := requestGate(handler, "/api/session")
	if recorder.Code != http.StatusServiceUnavailable || decodeGateError(t, recorder) != "service_unavailable" {
		t.Fatalf("unready response=(%d,%s)", recorder.Code, recorder.Body.String())
	}
	if next.count() != 0 {
		t.Fatal("uninitialized gate admitted a request")
	}

	if err := gate.observeCommitted(State{Enabled: false, Revision: 1, ChangedAt: 10}); err != nil {
		t.Fatal(err)
	}
	recorder = requestGate(handler, "/api/session")
	if recorder.Code != http.StatusTeapot || next.count() != 1 {
		t.Fatalf("disabled response=(%d,count=%d)", recorder.Code, next.count())
	}

	next.reset()
	if err := gate.observeCommitted(State{Enabled: true, Revision: 2, ChangedAt: 20, CurrentEventID: "op_AAAAAAAAAAAAAAAAAAAAAQ"}); err != nil {
		t.Fatal(err)
	}
	recorder = requestGate(handler, "/api/session")
	if recorder.Code != http.StatusServiceUnavailable || decodeGateError(t, recorder) != "maintenance" {
		t.Fatalf("maintenance response=(%d,%s)", recorder.Code, recorder.Body.String())
	}
	if next.count() != 0 {
		t.Fatal("maintenance gate admitted normal API")
	}
	if cache := recorder.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("Cache-Control=%q", cache)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("Content-Type=%q", contentType)
	}

	for _, path := range []string{"/api/config", "/api/auth/logout"} {
		next.reset()
		recorder = requestGate(handler, path)
		if recorder.Code != http.StatusTeapot || next.count() != 1 {
			t.Fatalf("allowlist %s response=(%d,count=%d)", path, recorder.Code, next.count())
		}
	}
	for _, path := range []string{"/api/config/", "/api/auth/logout/extra", "/v1/chat/completions"} {
		next.reset()
		recorder = requestGate(handler, path)
		if recorder.Code != http.StatusServiceUnavailable || decodeGateError(t, recorder) != "maintenance" || next.count() != 0 {
			t.Fatalf("non-allowlist %s response=(%d,count=%d)", path, recorder.Code, next.count())
		}
	}
}

func TestGateCommittedRevisionIsMonotonicAndRaceFree(t *testing.T) {
	gate := NewGate()
	if err := gate.observeCommitted(State{Enabled: false, Revision: 10, ChangedAt: 10}); err != nil {
		t.Fatal(err)
	}
	if err := gate.observeCommitted(State{Enabled: true, Revision: 9, ChangedAt: 9}); err != nil {
		t.Fatal(err)
	}
	state, ready := gate.State()
	if !ready || state.Revision != 10 || state.Enabled {
		t.Fatalf("state after stale observation=%+v ready=%v", state, ready)
	}
	if err := gate.observeCommitted(State{Enabled: true, Revision: 10, ChangedAt: 10}); err == nil {
		t.Fatal("conflicting same revision was accepted")
	}

	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 500; iteration++ {
				_, _ = gate.State()
				_ = gate.Enabled()
			}
		}()
	}
	wait.Wait()
}

func TestGateNilFailsClosedAndPathClassifier(t *testing.T) {
	next := &recorderHandler{}
	recorder := requestGate(GateMiddleware(nil, next), "/api/session")
	if recorder.Code != http.StatusServiceUnavailable || decodeGateError(t, recorder) != "service_unavailable" || next.count() != 0 {
		t.Fatalf("nil gate response=(%d,count=%d)", recorder.Code, next.count())
	}
	for _, path := range []string{"/api", "/api/", "/api/x", "/v1", "/v1/models"} {
		if !isUserStationAPIPath(path) {
			t.Fatalf("isUserStationAPIPath(%q)=false", path)
		}
	}
	for _, path := range []string{"/", "/healthz", "/admin/api"} {
		if isUserStationAPIPath(path) {
			t.Fatalf("isUserStationAPIPath(%q)=true", path)
		}
	}
}
