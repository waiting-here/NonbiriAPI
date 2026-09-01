package maintenance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type maintenanceRouteCapture struct {
	steward map[string]AuthorizedHTTPHandler
	admin   map[string]AuthorizedHTTPHandler
}

func (capture *maintenanceRouteCapture) RegisterStewardRoute(method, pattern string, handler AuthorizedHTTPHandler) error {
	if capture.steward == nil {
		capture.steward = make(map[string]AuthorizedHTTPHandler)
	}
	capture.steward[method+" "+pattern] = handler
	return nil
}

func (capture *maintenanceRouteCapture) RegisterAdminRoute(method, pattern string, handler AuthorizedHTTPHandler) error {
	if capture.admin == nil {
		capture.admin = make(map[string]AuthorizedHTTPHandler)
	}
	capture.admin[method+" "+pattern] = handler
	return nil
}

func maintenanceHTTPResponse(
	t *testing.T,
	handler AuthorizedHTTPHandler,
	method, target, body, key string,
	principal maintenancePrincipal,
) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler(response, request, HTTPPrincipal{Actor: principal.Actor})
	return response
}

func decodeMaintenanceState(t *testing.T, response *httptest.ResponseRecorder) stateWire {
	t.Helper()
	var state stateWire
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state %q: %v", response.Body.String(), err)
	}
	return state
}

func TestMaintenanceHTTPAuthorityCASReplayAndFinalAuthorization(t *testing.T) {
	store := openMaintenanceStore(t)
	service, gate := newMaintenanceService(t, NewRegistry())
	if _, err := service.PrepareListener(context.Background(), store.DB()); err != nil {
		t.Fatal(err)
	}
	admin := insertMaintenancePrincipal(t, store.DB(), "", "http-admin", true, false)
	steward := insertMaintenancePrincipal(t, store.DB(), "steward-http", "http-steward", false, true)
	capture := &maintenanceRouteCapture{}
	if err := RegisterRoutes(capture, capture, HTTPOptions{Database: store.DB(), Service: service}); err != nil {
		t.Fatal(err)
	}
	if len(capture.steward) != 2 || len(capture.admin) != 3 {
		t.Fatalf("registered steward/admin routes=%d/%d", len(capture.steward), len(capture.admin))
	}
	if _, found := capture.steward[http.MethodPost+" "+adminDisableRoute]; found {
		t.Fatal("steward disable route was registered")
	}

	adminRead := maintenanceHTTPResponse(t, capture.admin[http.MethodGet+" "+adminStateRoute], http.MethodGet, adminStateRoute, "", "", admin)
	if adminRead.Code != http.StatusOK || decodeMaintenanceState(t, adminRead) != (stateWire{Enabled: true, Revision: "1"}) {
		t.Fatalf("initial admin state=%d %s", adminRead.Code, adminRead.Body.String())
	}
	wrongRole := maintenanceHTTPResponse(t, capture.admin[http.MethodGet+" "+adminStateRoute], http.MethodGet, adminStateRoute, "", "", steward)
	if wrongRole.Code != http.StatusForbidden {
		t.Fatalf("steward used admin read=%d %s", wrongRole.Code, wrongRole.Body.String())
	}
	for name, test := range map[string][2]string{
		"query": {adminStateRoute + "?unexpected=1", ""},
		"body":  {adminStateRoute, `{}`},
	} {
		response := maintenanceHTTPResponse(t, capture.admin[http.MethodGet+" "+adminStateRoute], http.MethodGet, test[0], test[1], "", admin)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s read=%d %s", name, response.Code, response.Body.String())
		}
	}

	disableKey := strings.Repeat("A", 22)
	disableBody := `{"expected_revision":"1","reason":"fresh configuration reviewed"}`
	disable := maintenanceHTTPResponse(t, capture.admin[http.MethodPost+" "+adminDisableRoute], http.MethodPost, adminDisableRoute, disableBody, disableKey, admin)
	if disable.Code != http.StatusOK || decodeMaintenanceState(t, disable) != (stateWire{Enabled: false, Revision: "2"}) || gate.Enabled() {
		t.Fatalf("disable=%d %s gate=%v", disable.Code, disable.Body.String(), gate.Enabled())
	}
	replay := maintenanceHTTPResponse(t, capture.admin[http.MethodPost+" "+adminDisableRoute], http.MethodPost, adminDisableRoute, disableBody, disableKey, admin)
	if replay.Code != http.StatusOK || replay.Body.String() != disable.Body.String() {
		t.Fatalf("disable replay=%d %s want=%s", replay.Code, replay.Body.String(), disable.Body.String())
	}
	conflict := maintenanceHTTPResponse(t, capture.admin[http.MethodPost+" "+adminDisableRoute], http.MethodPost, adminDisableRoute, `{"expected_revision":"1","reason":"different"}`, disableKey, admin)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("same-key conflict=%d %s", conflict.Code, conflict.Body.String())
	}

	enableKey := strings.Repeat("B", 22)
	enableBody := `{"expected_revision":"2","reason":"urgent maintenance","confirmation":true}`
	enable := maintenanceHTTPResponse(t, capture.steward[http.MethodPost+" "+stewardEnableRoute], http.MethodPost, stewardEnableRoute, enableBody, enableKey, steward)
	if enable.Code != http.StatusOK || decodeMaintenanceState(t, enable) != (stateWire{Enabled: true, Revision: "3"}) || !gate.Enabled() {
		t.Fatalf("steward enable=%d %s gate=%v", enable.Code, enable.Body.String(), gate.Enabled())
	}
	if _, err := store.DB().Exec(`UPDATE users SET level=NULL WHERE id=?`, steward.ID); err != nil {
		t.Fatal(err)
	}
	demotedReplay := maintenanceHTTPResponse(t, capture.steward[http.MethodPost+" "+stewardEnableRoute], http.MethodPost, stewardEnableRoute, enableBody, enableKey, steward)
	if demotedReplay.Code != http.StatusForbidden {
		t.Fatalf("demoted replay=%d %s", demotedReplay.Code, demotedReplay.Body.String())
	}
	secondDisable := maintenanceHTTPResponse(t, capture.admin[http.MethodPost+" "+adminDisableRoute], http.MethodPost, adminDisableRoute,
		`{"expected_revision":"3","reason":"maintenance complete"}`, strings.Repeat("D", 22), admin)
	if secondDisable.Code != http.StatusOK || decodeMaintenanceState(t, secondDisable) != (stateWire{Enabled: false, Revision: "4"}) {
		t.Fatalf("second disable=%d %s", secondDisable.Code, secondDisable.Body.String())
	}
	adminEnable := maintenanceHTTPResponse(t, capture.admin[http.MethodPost+" "+adminEnableRoute], http.MethodPost, adminEnableRoute,
		`{"expected_revision":"4","reason":"administrator maintenance","confirmation":true}`, strings.Repeat("E", 22), admin)
	if adminEnable.Code != http.StatusOK || decodeMaintenanceState(t, adminEnable) != (stateWire{Enabled: true, Revision: "5"}) || !gate.Enabled() {
		t.Fatalf("admin enable=%d %s gate=%v", adminEnable.Code, adminEnable.Body.String(), gate.Enabled())
	}

	var disableEvents, enableEvents int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM maintenance_events WHERE action='disable'`).Scan(&disableEvents); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM maintenance_events WHERE action='enable'`).Scan(&enableEvents); err != nil {
		t.Fatal(err)
	}
	if disableEvents != 2 || enableEvents != 2 {
		t.Fatalf("maintenance events disable/enable=%d/%d", disableEvents, enableEvents)
	}
}

func TestMaintenanceHTTPRejectsMalformedMutations(t *testing.T) {
	store := openMaintenanceStore(t)
	service, _ := newMaintenanceService(t, NewRegistry())
	admin := insertMaintenancePrincipal(t, store.DB(), "", "http-malformed-admin", true, false)
	capture := &maintenanceRouteCapture{}
	if err := RegisterRoutes(capture, capture, HTTPOptions{Database: store.DB(), Service: service}); err != nil {
		t.Fatal(err)
	}
	handler := capture.admin[http.MethodPost+" "+adminEnableRoute]
	key := strings.Repeat("C", 22)
	for name, test := range map[string][3]string{
		"missing key":      {adminEnableRoute, `{"expected_revision":"1","reason":"reason","confirmation":true}`, ""},
		"unknown field":    {adminEnableRoute, `{"expected_revision":"1","reason":"reason","confirmation":true,"extra":1}`, key},
		"false confirm":    {adminEnableRoute, `{"expected_revision":"1","reason":"reason","confirmation":false}`, key},
		"leading revision": {adminEnableRoute, `{"expected_revision":"01","reason":"reason","confirmation":true}`, key},
		"query":            {adminEnableRoute + "?unexpected=1", `{"expected_revision":"1","reason":"reason","confirmation":true}`, key},
	} {
		response := maintenanceHTTPResponse(t, handler, http.MethodPost, test[0], test[1], test[2], admin)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s=%d %s", name, response.Code, response.Body.String())
		}
	}
	oversized := `{"expected_revision":"1","reason":"` + strings.Repeat("x", maxHTTPBodyBytes) + `","confirmation":true}`
	response := maintenanceHTTPResponse(t, handler, http.MethodPost, adminEnableRoute, oversized, key, admin)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized=%d %s", response.Code, response.Body.String())
	}
}
