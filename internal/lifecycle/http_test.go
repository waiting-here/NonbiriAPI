package lifecycle

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type lifecycleRouteRecorder struct {
	users  map[string]AuthorizedUserHandler
	admins map[string]AuthorizedAdminHandler
}

func newLifecycleRouteRecorder() *lifecycleRouteRecorder {
	return &lifecycleRouteRecorder{
		users: make(map[string]AuthorizedUserHandler), admins: make(map[string]AuthorizedAdminHandler),
	}
}

func (routes *lifecycleRouteRecorder) RegisterUserRoute(method, pattern string, handler AuthorizedUserHandler) error {
	routes.users[method+" "+pattern] = handler
	return nil
}

func (routes *lifecycleRouteRecorder) RegisterAdminRoute(method, pattern string, handler AuthorizedAdminHandler) error {
	routes.admins[method+" "+pattern] = handler
	return nil
}

func TestRegisterRoutesAndAccountLifecycleHTTP(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	coordinator := mustNewLifecycleCoordinator(t, fixture.config)
	routes := newLifecycleRouteRecorder()
	if err := RegisterRoutes(routes, routes, coordinator); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	if len(routes.users) != 2 || len(routes.admins) != 4 {
		t.Fatalf("registered routes users=%v admins=%v", routes.users, routes.admins)
	}

	exportRequest := httptest.NewRequest(http.MethodPost, accountExportRoute, nil)
	exportResponse := httptest.NewRecorder()
	routes.users[http.MethodPost+" "+accountExportRoute](
		exportResponse, exportRequest, UserPrincipal{UserID: 7},
	)
	if exportResponse.Code != http.StatusOK ||
		exportResponse.Header().Get("Content-Disposition") != `attachment; filename="nonbiriapi-account-export-v4.json"` ||
		exportResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("export response status=%d headers=%v body=%s",
			exportResponse.Code, exportResponse.Header(), exportResponse.Body.String())
	}
	var exported map[string]any
	if err := json.Unmarshal(exportResponse.Body.Bytes(), &exported); err != nil || exported["schema_version"] != float64(4) {
		t.Fatalf("export body=%v error=%v", exported, err)
	}

	invalidExport := httptest.NewRecorder()
	routes.users[http.MethodPost+" "+accountExportRoute](
		invalidExport, httptest.NewRequest(http.MethodPost, accountExportRoute+"?extra=1", nil),
		UserPrincipal{UserID: 7},
	)
	if invalidExport.Code != http.StatusBadRequest || fixture.auth.userCalls != 1 {
		t.Fatalf("invalid export status=%d fresh calls=%d", invalidExport.Code, fixture.auth.userCalls)
	}
}

func TestAccountDeleteHTTPRequiresExactClosedConfirmation(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	coordinator := mustNewLifecycleCoordinator(t, fixture.config)
	routes := newLifecycleRouteRecorder()
	if err := RegisterRoutes(routes, routes, coordinator); err != nil {
		t.Fatal(err)
	}
	handler := routes.users[http.MethodPost+" "+accountDeleteRoute]
	for _, body := range []string{
		`{"confirm":"delete"}`,
		`{"confirm":"DELETE","extra":true}`,
		`{"confirm":"DELETE","confirm":"DELETE"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, accountDeleteRoute, bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler(response, request, UserPrincipal{UserID: 9})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid delete %s status=%d body=%s", body, response.Code, response.Body.String())
		}
	}
	if fixture.auth.userCalls != 0 {
		t.Fatalf("invalid delete consumed fresh authorization %d times", fixture.auth.userCalls)
	}

	request := httptest.NewRequest(http.MethodPost, accountDeleteRoute, bytes.NewBufferString(`{"confirm":"DELETE"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler(response, request, UserPrincipal{UserID: 9})
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 ||
		response.Header().Get("Cache-Control") != "no-store" || fixture.auth.userCalls != 1 {
		t.Fatalf("delete status=%d headers=%v body=%q fresh calls=%d",
			response.Code, response.Header(), response.Body.String(), fixture.auth.userCalls)
	}
}

func TestLegalHoldHTTPUsesStrictWireAndExactReplayBody(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	adminID := seedLifecycleUser(t, fixture.store.DB(), "http-admin", true, 100)
	rootID := maintenanceObjectID("H", 'A')
	holdID := legalHoldID("H", 'A')
	seedMaintenanceHoldRoot(t, fixture.store.DB(), adminID, rootID, 100)
	coordinator := mustNewLifecycleCoordinator(t, configureMaintenanceLegalHolds(fixture.config, holdID))
	routes := newLifecycleRouteRecorder()
	if err := RegisterRoutes(routes, routes, coordinator); err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRecorder()
	routes.admins[http.MethodGet+" "+legalHoldListRoute](
		list, httptest.NewRequest(http.MethodGet, legalHoldListRoute+"?state=active&limit=1", nil),
		AdminPrincipal{UserID: adminID},
	)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}

	createBody := []byte(`{"object_kind":"maintenance_event","object_ref":"` + rootID +
		`","basis":"preserve evidence","expires_at":200,"confirmation":true}`)
	create := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, legalHoldListRoute, bytes.NewReader(createBody))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", legalHoldKey("http-create"))
		response := httptest.NewRecorder()
		routes.admins[http.MethodPost+" "+legalHoldListRoute](response, request, AdminPrincipal{UserID: adminID})
		return response
	}
	first := create()
	replay := create()
	if first.Code != http.StatusCreated || replay.Code != http.StatusCreated ||
		!bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatalf("create/replay first=%d %s replay=%d %s",
			first.Code, first.Body.String(), replay.Code, replay.Body.String())
	}

	releaseRequest := httptest.NewRequest(http.MethodPost, "/admin/api/legal-holds/"+holdID+"/release",
		bytes.NewBufferString(`{"expected_revision":"1","reason":"complete","confirmation":true}`))
	releaseRequest.SetPathValue("id", holdID)
	releaseRequest.Header.Set("Content-Type", "application/json")
	releaseRequest.Header.Set("Idempotency-Key", legalHoldKey("http-release"))
	release := httptest.NewRecorder()
	routes.admins[http.MethodPost+" "+legalHoldReleaseRoute](
		release, releaseRequest, AdminPrincipal{UserID: adminID},
	)
	if release.Code != http.StatusOK {
		t.Fatalf("release status=%d body=%s", release.Code, release.Body.String())
	}
	if fixture.auth.freshCalls != 3 {
		t.Fatalf("create/replay/release fresh authorizations=%d", fixture.auth.freshCalls)
	}

	invalid := httptest.NewRequest(http.MethodPost, legalHoldListRoute,
		bytes.NewBufferString(`{"object_kind":"maintenance_event","object_ref":"`+rootID+
			`","basis":"x","expires_at":200,"confirmation":true,"extra":1}`))
	invalid.Header.Set("Content-Type", "application/json")
	invalid.Header.Set("Idempotency-Key", legalHoldKey("http-invalid"))
	invalidResponse := httptest.NewRecorder()
	routes.admins[http.MethodPost+" "+legalHoldListRoute](
		invalidResponse, invalid, AdminPrincipal{UserID: adminID},
	)
	if invalidResponse.Code != http.StatusBadRequest || fixture.auth.freshCalls != 3 {
		t.Fatalf("invalid create status=%d fresh calls=%d", invalidResponse.Code, fixture.auth.freshCalls)
	}
}
