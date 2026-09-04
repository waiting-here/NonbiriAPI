package checkin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

type capturedUserRoutes struct {
	routes map[string]AuthorizedUserHandler
	err    error
}

func (registrar *capturedUserRoutes) RegisterUserRoute(method, pattern string, handler AuthorizedUserHandler) error {
	if registrar.err != nil {
		return registrar.err
	}
	if registrar.routes == nil {
		registrar.routes = make(map[string]AuthorizedUserHandler)
	}
	key := method + " " + pattern
	if handler == nil {
		return errors.New("nil handler")
	}
	if _, exists := registrar.routes[key]; exists {
		return errors.New("duplicate route")
	}
	registrar.routes[key] = handler
	return nil
}

func TestRegisterRoutesPublishesFrozenMethods(t *testing.T) {
	fixture := newCheckinFixture(t)
	registrar := &capturedUserRoutes{}
	if err := RegisterRoutes(registrar, fixture.service); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	keys := make([]string, 0, len(registrar.routes))
	for key := range registrar.routes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	want := []string{http.MethodGet + " " + Route, http.MethodPost + " " + Route}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("routes = %v, want %v", keys, want)
	}
	if err := RegisterRoutes(nil, fixture.service); err == nil {
		t.Fatal("nil registrar was accepted")
	}
	registrar.err = errors.New("registration stopped")
	if err := RegisterRoutes(registrar, fixture.service); err == nil {
		t.Fatal("registration error was hidden")
	}
}

func TestHTTPDisabledProjectionIsExact(t *testing.T) {
	fixture := newCheckinFixture(t)
	userID := fixture.seedUser("wire-disabled")
	registrar := &capturedUserRoutes{}
	if err := RegisterRoutes(registrar, fixture.service); err != nil {
		t.Fatal(err)
	}
	response := invokeCheckinRoute(t, registrar, http.MethodGet, Route, nil, userID)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if got := strings.TrimSpace(response.Body.String()); got != `{"enabled":false}` {
		t.Fatalf("disabled body = %s", got)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
}

func TestHTTPEnabledStatusAndMutationUseClosedWire(t *testing.T) {
	fixture := newCheckinFixture(t)
	fixture.configure(db.CheckinModeEnabled, "0", 1_250, 1_250, 9_000)
	userID := fixture.seedUser("wire-enabled")
	registrar := &capturedUserRoutes{}
	if err := RegisterRoutes(registrar, fixture.service); err != nil {
		t.Fatal(err)
	}

	statusResponse := invokeCheckinRoute(t, registrar, http.MethodGet, Route, nil, userID)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%s", statusResponse.Code, statusResponse.Body.String())
	}
	var status map[string]any
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	wantStatus := map[string]any{
		"enabled": true, "checked_in_today": false, "balance": "0",
		"award_min": "1.25", "award_max": "1.25", "balance_cap": "9",
	}
	if !reflect.DeepEqual(status, wantStatus) {
		t.Fatalf("GET body = %#v, want %#v", status, wantStatus)
	}
	for key := range status {
		if strings.Contains(key, "_milli") {
			t.Fatalf("GET leaked milli field %q", key)
		}
	}

	postResponse := invokeCheckinRoute(t, registrar, http.MethodPost, Route, nil, userID)
	if postResponse.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", postResponse.Code, postResponse.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(postResponse.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	wantResult := map[string]any{"award": "1.25", "balance": "1.25"}
	if !reflect.DeepEqual(result, wantResult) {
		t.Fatalf("POST body = %#v, want %#v", result, wantResult)
	}

	duplicate := invokeCheckinRoute(t, registrar, http.MethodPost, Route, nil, userID)
	assertHTTPError(t, duplicate, http.StatusConflict, httperr.CodeAlreadyCheckedIn)
}

func TestHTTPRejectsAnyQueryOrBodyBeforeDomainWork(t *testing.T) {
	for _, test := range []struct {
		name, method, target, body string
	}{
		{name: "GET query", method: http.MethodGet, target: Route + "?x=1"},
		{name: "GET force query", method: http.MethodGet, target: Route + "?"},
		{name: "GET body", method: http.MethodGet, target: Route, body: `{}`},
		{name: "POST query", method: http.MethodPost, target: Route + "?x=1"},
		{name: "POST body", method: http.MethodPost, target: Route, body: `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCheckinFixture(t)
			fixture.configure(db.CheckinModeEnabled, "0", 1, 1, 100)
			userID := fixture.seedUser("strict-wire")
			registrar := &capturedUserRoutes{}
			if err := RegisterRoutes(registrar, fixture.service); err != nil {
				t.Fatal(err)
			}
			var body *strings.Reader
			if test.body != "" {
				body = strings.NewReader(test.body)
			} else {
				body = strings.NewReader("")
			}
			response := invokeCheckinRoute(t, registrar, test.method, test.target, body, userID)
			assertHTTPError(t, response, http.StatusBadRequest, httperr.CodeInvalidRequest)
			if fixture.authorizer.calls.Load() != 0 || fixture.scalar(`SELECT COUNT(*) FROM checkins`) != 0 {
				t.Fatal("invalid wire reached final authorization or domain write")
			}
		})
	}
}

func TestHTTPDomainErrorMapping(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrUnauthorized, http.StatusUnauthorized, httperr.CodeUnauthorized},
		{ErrForbidden, http.StatusForbidden, httperr.CodeForbidden},
		{ErrNotFound, http.StatusNotFound, httperr.CodeNotFound},
		{ErrFeatureDisabled, http.StatusForbidden, httperr.CodeFeatureDisabled},
		{ErrAlreadyCheckedIn, http.StatusConflict, httperr.CodeAlreadyCheckedIn},
		{ErrBalanceCap, http.StatusForbidden, httperr.CodeCheckinCapReached},
		{ErrMaintenance, http.StatusServiceUnavailable, httperr.CodeMaintenance},
		{ErrResourceLimit, http.StatusUnprocessableEntity, httperr.CodeResourceLimitExceeded},
		{ErrUnavailable, http.StatusServiceUnavailable, httperr.CodeServiceUnavailable},
		{ErrInvariant, http.StatusInternalServerError, httperr.CodeInternal},
	} {
		recorder := httptest.NewRecorder()
		writeError(recorder, test.err)
		assertHTTPError(t, recorder, test.status, test.code)
	}
}

func invokeCheckinRoute(t *testing.T, registrar *capturedUserRoutes, method, target string, body *strings.Reader, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	key := method + " " + Route
	handler := registrar.routes[key]
	if handler == nil {
		t.Fatalf("missing handler %s", key)
	}
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, body)
	}
	response := httptest.NewRecorder()
	handler(response, request, UserPrincipal{UserID: userID})
	return response
}

func assertHTTPError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d, body=%s", response.Code, status, response.Body.String())
	}
	var envelope httperr.Envelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.Error.Code != code || envelope.Error.Source != httperr.SourcePlatform {
		t.Fatalf("error = %#v, want code %q platform", envelope.Error, code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
}
