package duel_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type registrar struct {
	mux      *http.ServeMux
	identity duel.Identity
}

func (a *registrar) RegisterUserRoute(method, pattern string, h resources.AuthorizedUserHandler) error {
	a.mux.HandleFunc(method+" "+pattern, func(w http.ResponseWriter, r *http.Request) {
		h(w, r, resources.UserPrincipal{UserID: a.identity.UserID})
	})
	return nil
}
func (a *registrar) RegisterContinuationUserRoute(method, pattern string, h resources.AuthenticatedContinuationHandler) error {
	a.mux.HandleFunc(method+" "+pattern, func(w http.ResponseWriter, r *http.Request) {
		h(w, r, resources.ContinuationUserPrincipal{UserID: a.identity.UserID, SessionBinding: a.identity.SessionBinding})
	})
	return nil
}

func TestHTTPStrictBodyQueriesAndSafeProjection(t *testing.T) {
	f := newFixture(t, "likes")
	state := f.matched()
	routes := &registrar{mux: http.NewServeMux(), identity: f.identity(0)}
	if err := f.s.RegisterRoutes(routes, routes); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string, keys ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		for _, key := range keys {
			r.Header.Add("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		routes.mux.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		method, path, body string
		code               int
	}{
		{"GET", "/api/games/likes/state?unknown=1", "", 400},
		{"GET", "/api/games/likes/state", "{}", 400},
		{"GET", "/api/games/likes/history?limit=1&limit=2", "", 400},
		{"GET", "/api/games/likes/history?limit=01", "", 400},
		{"POST", "/api/games/likes/sessions/" + state.ID + "/actions", `{"phase_seq":"1","phase_seq":"2","action":{}}`, 400},
		{"POST", "/api/games/likes/sessions/" + state.ID + "/actions", `{"phase_seq":"1","action":{"kind":"plan","kind":"bid"}}`, 400},
		{"POST", "/api/games/likes/sessions/" + state.ID + "/actions", `{"Phase_seq":"1","action":{}}`, 400},
		{"POST", "/api/games/likes/sessions/" + state.ID + "/actions", `{"phase_seq":"1","action":null}`, 400},
		{"POST", "/api/games/likes/queue", "null", 400},
		{"POST", "/api/games/likes/queue", strings.Repeat(" ", 32769), 413},
	} {
		got := request(tc.method, tc.path, tc.body, f.key())
		if got.Code != tc.code {
			t.Fatalf("%s %s: status=%d body=%s", tc.method, tc.path, got.Code, got.Body.String())
		}
	}
	path := "/api/games/likes/sessions/" + state.ID + "/actions"
	empty := `{"phase_seq":"` + state.PhaseSeq + `","action":{"kind":"plan","plan":{"purchases":[],"main":null,"extra":[]}}}`
	if got := request("POST", path, empty, f.key()); got.Code != 400 {
		t.Fatal("normal player skipped through HTTP", got.Code)
	}
	if f.read(0).Current.Locked[state.You] {
		t.Fatal("rejected skip acquired a lock")
	}
	body := `{"phase_seq":"` + state.PhaseSeq + `","action":` + basicPlan + `}`
	if got := request("POST", path, body); got.Code != 400 {
		t.Fatal("missing idempotency key", got.Code)
	}
	if got := request("POST", path, body, f.key(), f.key()); got.Code != 400 {
		t.Fatal("duplicate idempotency key", got.Code)
	}
	key := f.key()
	got := request("POST", path, body, key)
	if got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	replay := request("POST", path, body, key)
	if replay.Code != 200 || replay.Body.String() != got.Body.String() {
		t.Fatal("receipt replay changed")
	}
	stateReply := request("GET", "/api/games/likes/state", "")
	if stateReply.Code != 200 || stateReply.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(stateReply.Code, stateReply.Header())
	}
	for _, secret := range []string{"user_id", "general_account_id", "device_hash", "ip_hash", "session-binding", "initial_state_json"} {
		if strings.Contains(stateReply.Body.String(), secret) {
			t.Fatal("private state field exposed", secret)
		}
	}
	f.ledger()
}
