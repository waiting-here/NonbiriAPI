package activities

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

type activityUserRoutes struct {
	handlers map[string]AuthorizedUserHandler
}

func (routes *activityUserRoutes) RegisterUserRoute(method, pattern string, handler AuthorizedUserHandler) error {
	if routes.handlers == nil {
		routes.handlers = make(map[string]AuthorizedUserHandler)
	}
	routes.handlers[method+" "+pattern] = handler
	return nil
}

type activityAdminRoutes struct {
	handlers map[string]AuthorizedAdminHandler
}

func (routes *activityAdminRoutes) RegisterAdminRoute(method, pattern string, handler AuthorizedAdminHandler) error {
	if routes.handlers == nil {
		routes.handlers = make(map[string]AuthorizedAdminHandler)
	}
	routes.handlers[method+" "+pattern] = handler
	return nil
}

func TestActivitiesHTTPStrictRoutesAndZeroConfig(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_400_000)
	userID, _ := fixture.seedUser("http", false)
	service, _ := NewService(ServiceConfig{Repository: fixture.repository})
	users, admins := &activityUserRoutes{}, &activityAdminRoutes{}
	if err := RegisterRoutes(users, admins, service); err != nil {
		t.Fatal(err)
	}
	if len(users.handlers) != 4 || len(admins.handlers) != 7 {
		t.Fatalf("registered user=%d admin=%d", len(users.handlers), len(admins.handlers))
	}

	request := httptest.NewRequest(http.MethodGet, routeActivities, nil)
	recorder := httptest.NewRecorder()
	users.handlers[http.MethodGet+" "+routeActivities](recorder, request, UserPrincipal{UserID: userID})
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte(`"reason":"disabled"`)) {
		t.Fatalf("fresh activities status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, routeThursdayContributions,
		bytes.NewBufferString(`{"period_id":"thu_AAAAAAAAAAAAAAAAAAAAAA","period_id":"thu_AAAAAAAAAAAAAAAAAAAAAA","expected_revision":"1"}`))
	request.Header.Set("Idempotency-Key", "http-duplicate-field-001")
	recorder = httptest.NewRecorder()
	users.handlers[http.MethodPost+" "+routeThursdayContributions](recorder, request, UserPrincipal{UserID: userID})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("duplicate field status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, routeWelfareClaims, bytes.NewBufferString(`{}`))
	request.Header.Set("Idempotency-Key", "http-unwanted-body-0001")
	recorder = httptest.NewRecorder()
	users.handlers[http.MethodPost+" "+routeWelfareClaims](recorder, request, UserPrincipal{UserID: userID})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected body status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPatch, routeAdminActivityConfig,
		bytes.NewBufferString(`{"expected_revision":"1","welfare":{"threshold":"0","cap":"0"}}`))
	request.Header.Set("Idempotency-Key", "http-zero-config-values1")
	recorder = httptest.NewRecorder()
	admins.handlers[http.MethodPatch+" "+routeAdminActivityConfig](recorder, request, AdminPrincipal{UserID: fixture.adminID})
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte(`"threshold":"0"`)) || !bytes.Contains(recorder.Body.Bytes(), []byte(`"cap":"0"`)) {
		t.Fatalf("zero config status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, routeAdminPools+"?unknown=1", nil)
	recorder = httptest.NewRecorder()
	admins.handlers[http.MethodGet+" "+routeAdminPools](recorder, request, AdminPrincipal{UserID: fixture.adminID})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown query status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, routeAdminPools+"?pool_type=", nil)
	recorder = httptest.NewRecorder()
	admins.handlers[http.MethodGet+" "+routeAdminPools](recorder, request, AdminPrincipal{UserID: fixture.adminID})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty enum query status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, routeThursdayContributions,
		bytes.NewBufferString(`{"period_id":"thu_AAAAAAAAAAAAAAAAAAAAAA","expected_revision":"1","count":2}`))
	request.Header.Set("Idempotency-Key", "http-forbidden-batch-001")
	recorder = httptest.NewRecorder()
	users.handlers[http.MethodPost+" "+routeThursdayContributions](recorder, request, UserPrincipal{UserID: userID})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("forbidden contribution batch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
