package linklink

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type capturedUserRoutes struct {
	handlers map[string]resources.AuthorizedUserHandler
}

func (routes *capturedUserRoutes) RegisterUserRoute(method, path string, handler resources.AuthorizedUserHandler) error {
	if routes.handlers == nil {
		routes.handlers = map[string]resources.AuthorizedUserHandler{}
	}
	key := method + " " + path
	if handler == nil || routes.handlers[key] != nil {
		return errors.New("duplicate route")
	}
	routes.handlers[key] = handler
	return nil
}

type capturedContinuationRoutes struct {
	handlers map[string]resources.AuthenticatedContinuationHandler
}

func (routes *capturedContinuationRoutes) RegisterContinuationUserRoute(method, path string, handler resources.AuthenticatedContinuationHandler) error {
	if routes.handlers == nil {
		routes.handlers = map[string]resources.AuthenticatedContinuationHandler{}
	}
	key := method + " " + path
	if handler == nil || routes.handlers[key] != nil {
		return errors.New("duplicate route")
	}
	routes.handlers[key] = handler
	return nil
}

func TestHTTPRoutesUseOrdinaryStartAndContinuationOnlyForExistingFlow(t *testing.T) {
	fixture := newFixture(t)
	userID, binding := fixture.seedUser("http", testFunding)
	ordinary := &capturedUserRoutes{}
	continuation := &capturedContinuationRoutes{}
	if err := RegisterRoutes(ordinary, continuation, fixture.service); err != nil {
		t.Fatal(err)
	}
	if len(ordinary.handlers) != 1 || ordinary.handlers[http.MethodPost+" "+RouteSessions] == nil || len(continuation.handlers) != 4 {
		t.Fatalf("registered ordinary=%v continuation=%v", ordinary.handlers, continuation.handlers)
	}
	for _, key := range []string{
		http.MethodGet + " " + RouteSession,
		http.MethodPost + " " + RouteMatches,
		http.MethodPost + " " + RouteAbandon,
		http.MethodPost + " " + RouteLease,
	} {
		if continuation.handlers[key] == nil {
			t.Fatalf("missing continuation route %s", key)
		}
	}

	start := ordinary.handlers[http.MethodPost+" "+RouteSessions]
	bad := httptest.NewRequest(http.MethodPost, "https://user.example"+RouteSessions, strings.NewReader(`{"spec":"6x8","spec":"8x8"}`))
	bad.Header.Set("Idempotency-Key", fixture.key(150))
	badRecorder := httptest.NewRecorder()
	start(badRecorder, bad, resources.UserPrincipal{UserID: userID})
	if badRecorder.Code != http.StatusBadRequest || fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE user_id=?`, userID) != 0 {
		t.Fatalf("duplicate JSON = %d %s", badRecorder.Code, badRecorder.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "https://user.example"+RouteSessions, strings.NewReader(`{"spec":"6x8"}`))
	request.Header.Set("Idempotency-Key", fixture.key(151))
	recorder := httptest.NewRecorder()
	start(recorder, request, resources.UserPrincipal{UserID: userID})
	if recorder.Code != http.StatusCreated || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("start HTTP = %d %s", recorder.Code, recorder.Body.String())
	}
	var state State
	if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil || state.Spec != game.LinkLinkSpec6x8 {
		t.Fatalf("start body = (%+v,%v)", state, err)
	}

	read := continuation.handlers[http.MethodGet+" "+RouteSession]
	readRequest := httptest.NewRequest(http.MethodGet, "https://user.example"+RouteSession, nil)
	readRecorder := httptest.NewRecorder()
	read(readRecorder, readRequest, resources.ContinuationUserPrincipal{UserID: userID, SessionBinding: binding})
	if readRecorder.Code != http.StatusOK || !strings.Contains(readRecorder.Body.String(), state.SessionID) {
		t.Fatalf("read HTTP = %d %s", readRecorder.Code, readRecorder.Body.String())
	}

	abandon := continuation.handlers[http.MethodPost+" "+RouteAbandon]
	abandonRequest := httptest.NewRequest(http.MethodPost, "https://user.example/api/games/linklink/sessions/ignored/abandon", strings.NewReader(`{"expected_revision":"1","confirmation":true}`))
	abandonRequest.SetPathValue("id", state.SessionID)
	abandonRecorder := httptest.NewRecorder()
	abandon(abandonRecorder, abandonRequest, resources.ContinuationUserPrincipal{UserID: userID, SessionBinding: binding})
	if abandonRecorder.Code != http.StatusBadRequest || fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=?`, state.SessionID) != 1 {
		t.Fatalf("abandon without idempotency key = %d %s", abandonRecorder.Code, abandonRecorder.Body.String())
	}

	match := continuation.handlers[http.MethodPost+" "+RouteMatches]
	first, second := fixture.firstLegalPair(state.SessionID)
	matchBody := fmt.Sprintf(`{"expected_revision":"1","first":{"row":%d,"col":%d},"second":{"row":%d,"col":%d}}`, first.Row, first.Col, second.Row, second.Col)
	malformed := httptest.NewRequest(http.MethodPost, "https://user.example/api/games/linklink/sessions/ignored/matches?client_timer=1", strings.NewReader(matchBody))
	malformed.SetPathValue("id", state.SessionID)
	malformed.Header.Set("Idempotency-Key", fixture.key(152))
	malformedRecorder := httptest.NewRecorder()
	match(malformedRecorder, malformed, resources.ContinuationUserPrincipal{UserID: userID, SessionBinding: binding})
	if malformedRecorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown query = %d %s", malformedRecorder.Code, malformedRecorder.Body.String())
	}
	clientAuthority := httptest.NewRequest(http.MethodPost, "https://user.example/api/games/linklink/sessions/ignored/matches", strings.NewReader(`{"expected_revision":"1","first":{"row":0,"col":0},"second":{"row":0,"col":1},"path":[],"timer":149}`))
	clientAuthority.SetPathValue("id", state.SessionID)
	clientAuthority.Header.Set("Idempotency-Key", fixture.key(153))
	clientRecorder := httptest.NewRecorder()
	match(clientRecorder, clientAuthority, resources.ContinuationUserPrincipal{UserID: userID, SessionBinding: binding})
	if clientRecorder.Code != http.StatusBadRequest || fixture.scalar(`SELECT pairs_removed FROM game_linklink_sessions WHERE id=?`, state.SessionID) != 0 {
		t.Fatalf("client authority fields = %d %s", clientRecorder.Code, clientRecorder.Body.String())
	}

	valid := httptest.NewRequest(http.MethodPost, "https://user.example/api/games/linklink/sessions/ignored/matches", strings.NewReader(matchBody))
	valid.SetPathValue("id", state.SessionID)
	valid.Header.Set("Idempotency-Key", fixture.key(154))
	validRecorder := httptest.NewRecorder()
	match(validRecorder, valid, resources.ContinuationUserPrincipal{UserID: userID, SessionBinding: binding})
	if validRecorder.Code != http.StatusOK || !strings.Contains(validRecorder.Body.String(), `"revision":"2"`) {
		t.Fatalf("match HTTP = %d %s", validRecorder.Code, validRecorder.Body.String())
	}
}

func TestHTTPStrictHeadersBodiesQueriesAndErrorCodes(t *testing.T) {
	fixture := newFixture(t)
	userID, _ := fixture.seedUser("http-strict", testFunding)
	ordinary := &capturedUserRoutes{}
	continuation := &capturedContinuationRoutes{}
	if err := RegisterRoutes(ordinary, continuation, fixture.service); err != nil {
		t.Fatal(err)
	}
	start := ordinary.handlers[http.MethodPost+" "+RouteSessions]
	tests := []struct {
		name string
		url  string
		body string
		keys []string
	}{
		{name: "unknown query", url: RouteSessions + "?x=1", body: `{"spec":"6x8"}`, keys: []string{fixture.key(160)}},
		{name: "missing key", url: RouteSessions, body: `{"spec":"6x8"}`},
		{name: "duplicate key", url: RouteSessions, body: `{"spec":"6x8"}`, keys: []string{fixture.key(161), fixture.key(162)}},
		{name: "unknown field", url: RouteSessions, body: `{"spec":"6x8","price":"0"}`, keys: []string{fixture.key(163)}},
		{name: "null root", url: RouteSessions, body: `null`, keys: []string{fixture.key(164)}},
		{name: "trailing JSON", url: RouteSessions, body: `{"spec":"6x8"}{}`, keys: []string{fixture.key(165)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "https://user.example"+test.url, strings.NewReader(test.body))
			for _, key := range test.keys {
				request.Header.Add("Idempotency-Key", key)
			}
			recorder := httptest.NewRecorder()
			start(recorder, request, resources.UserPrincipal{UserID: userID})
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_request"`) {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE user_id=?`, userID) != 0 {
		t.Fatal("strict HTTP rejections wrote a session")
	}
}

func TestStrictJSONRejectsInvalidUTF8(t *testing.T) {
	body := []byte{'{', '"', 's', 'p', 'e', 'c', '"', ':', '"', 0xff, '"', '}'}
	if err := validateStrictJSON(body); err == nil {
		t.Fatal("strict JSON accepted invalid UTF-8")
	}
}
