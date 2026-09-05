package adminapi

import (
	"bytes"
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
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type siteConfigMuxRegistrar struct {
	mux       *http.ServeMux
	principal SiteConfigAdminPrincipal
	routes    []string
	seen      map[string]struct{}
	failAt    int
}

func newSiteConfigMuxRegistrar() *siteConfigMuxRegistrar {
	return &siteConfigMuxRegistrar{
		mux: http.NewServeMux(), principal: SiteConfigAdminPrincipal{UserID: 1}, seen: make(map[string]struct{}), failAt: -1,
	}
}

func (registrar *siteConfigMuxRegistrar) RegisterAdminRoute(method, pattern string, handler SiteConfigAuthorizedAdminHandler) error {
	if registrar == nil || registrar.mux == nil || handler == nil {
		return errors.New("invalid test registration")
	}
	if registrar.failAt >= 0 && len(registrar.routes) == registrar.failAt {
		return errors.New("forced registration failure")
	}
	route := method + " " + pattern
	if _, exists := registrar.seen[route]; exists {
		return errors.New("duplicate test route")
	}
	registrar.seen[route] = struct{}{}
	registrar.routes = append(registrar.routes, route)
	registrar.mux.HandleFunc(route, func(writer http.ResponseWriter, request *http.Request) {
		handler(writer, request, registrar.principal)
	})
	return nil
}

func newSiteConfigHTTPFixture(t *testing.T) (*db.Store, *siteConfigMuxRegistrar) {
	t.Helper()
	store := openGenerationTwoPublicConfigStore(t)
	repository := newSiteConfigTestRepository(t, store, &siteConfigTestFinalAuthorizer{})
	runtime, err := NewSiteConfigRuntime(repository)
	if err != nil {
		t.Fatalf("new site configuration HTTP runtime: %v", err)
	}
	registrar := newSiteConfigMuxRegistrar()
	if err := RegisterSiteConfigRoutes(registrar, runtime); err != nil {
		t.Fatalf("register site configuration routes: %v", err)
	}
	return store, registrar
}

func siteConfigHTTPRequest(t *testing.T, handler http.Handler, method, path string, body []byte, headers map[string][]string) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, "https://admin.example"+path, nil)
	} else {
		request = httptest.NewRequest(method, "https://admin.example"+path, bytes.NewReader(body))
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func assertSiteConfigHTTPError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d, want %d; body=%s", recorder.Code, status, recorder.Body.String())
	}
	var envelope httperr.Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v; body=%q", err, recorder.Body.String())
	}
	if envelope.Error.Code != code || envelope.Error.Source != httperr.SourcePlatform {
		t.Fatalf("error=%+v, want code=%q platform source", envelope.Error, code)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("error Cache-Control=%q, want no-store", recorder.Header().Get("Cache-Control"))
	}
}

func TestRegisterSiteConfigRoutesIsIndependentAndExact(t *testing.T) {
	store := openGenerationTwoPublicConfigStore(t)
	repository := newSiteConfigTestRepository(t, store, &siteConfigTestFinalAuthorizer{})
	runtime, err := NewSiteConfigRuntime(repository)
	if err != nil {
		t.Fatal(err)
	}
	registrar := newSiteConfigMuxRegistrar()
	if err := RegisterSiteConfigRoutes(registrar, runtime); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	want := []string{
		"GET " + RouteAdminBootstrapConfig,
		"GET " + RouteAdminSiteConfig,
		"GET " + RouteAdminSiteConfigCatalog,
		"PATCH " + RouteAdminSiteConfigKey,
		"PATCH " + RouteAdminSiteConfig,
	}
	if !reflect.DeepEqual(registrar.routes, want) {
		t.Fatalf("routes=%v, want %v", registrar.routes, want)
	}
	if err := RegisterSiteConfigRoutes(registrar, runtime); err == nil {
		t.Fatal("duplicate route registration unexpectedly succeeded")
	}
	failing := newSiteConfigMuxRegistrar()
	failing.failAt = 2
	if err := RegisterSiteConfigRoutes(failing, runtime); err == nil || !strings.Contains(err.Error(), RouteAdminSiteConfigCatalog) {
		t.Fatalf("registration failure=%v, want catalog route context", err)
	}
	if err := RegisterSiteConfigRoutes(nil, runtime); err == nil {
		t.Fatal("nil registrar unexpectedly succeeded")
	}
}

func TestSiteConfigHTTPReadContracts(t *testing.T) {
	_, registrar := newSiteConfigHTTPFixture(t)
	for _, path := range []string{RouteAdminBootstrapConfig, RouteAdminSiteConfig, RouteAdminSiteConfigCatalog} {
		recorder := siteConfigHTTPRequest(t, registrar.mux, http.MethodGet, path, nil, nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
		if recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("GET %s headers=%v", path, recorder.Header())
		}
	}

	bootstrap := siteConfigHTTPRequest(t, registrar.mux, http.MethodGet, RouteAdminBootstrapConfig, nil, nil)
	var bootstrapWire map[string]any
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &bootstrapWire); err != nil {
		t.Fatal(err)
	}
	if len(bootstrapWire) != 12 {
		t.Fatalf("bootstrap field count=%d, want 12: %v", len(bootstrapWire), bootstrapWire)
	}
	if _, stale := bootstrapWire["default_locale"]; stale {
		t.Fatal("stale default_locale leaked into administrator bootstrap")
	}

	configuration := siteConfigHTTPRequest(t, registrar.mux, http.MethodGet, RouteAdminSiteConfig, nil, nil)
	var configurationWire map[string]json.RawMessage
	if err := json.Unmarshal(configuration.Body.Bytes(), &configurationWire); err != nil {
		t.Fatal(err)
	}
	configurationKeys := make([]string, 0, len(configurationWire))
	for key := range configurationWire {
		configurationKeys = append(configurationKeys, key)
	}
	sort.Strings(configurationKeys)
	if !reflect.DeepEqual(configurationKeys, []string{"revision", "values"}) {
		t.Fatalf("site configuration fields=%v", configurationKeys)
	}

	catalog := siteConfigHTTPRequest(t, registrar.mux, http.MethodGet, RouteAdminSiteConfigCatalog, nil, nil)
	var catalogWire map[string]json.RawMessage
	if err := json.Unmarshal(catalog.Body.Bytes(), &catalogWire); err != nil {
		t.Fatal(err)
	}
	if len(catalogWire) != 1 || len(catalogWire["data"]) == 0 {
		t.Fatalf("catalog envelope=%v", catalogWire)
	}

	for _, path := range []string{RouteAdminBootstrapConfig, RouteAdminSiteConfig, RouteAdminSiteConfigCatalog} {
		assertSiteConfigHTTPError(t, siteConfigHTTPRequest(t, registrar.mux, http.MethodGet, path+"?extra=1", nil, nil), http.StatusBadRequest, httperr.CodeInvalidRequest)
		assertSiteConfigHTTPError(t, siteConfigHTTPRequest(t, registrar.mux, http.MethodGet, path+"?", nil, nil), http.StatusBadRequest, httperr.CodeInvalidRequest)
		assertSiteConfigHTTPError(t, siteConfigHTTPRequest(t, registrar.mux, http.MethodGet, path, []byte(`{}`), nil), http.StatusBadRequest, httperr.CodeInvalidRequest)
	}
	wrongMethod := siteConfigHTTPRequest(t, registrar.mux, http.MethodPost, RouteAdminBootstrapConfig, nil, nil)
	if wrongMethod.Code != http.StatusMethodNotAllowed || !strings.Contains(wrongMethod.Header().Get("Allow"), http.MethodGet) {
		t.Fatalf("wrong method status=%d Allow=%q body=%q", wrongMethod.Code, wrongMethod.Header().Get("Allow"), wrongMethod.Body.String())
	}
	head := siteConfigHTTPRequest(t, registrar.mux, http.MethodHead, RouteAdminBootstrapConfig, nil, nil)
	assertSiteConfigHTTPError(t, head, http.StatusMethodNotAllowed, httperr.CodeMethodNotAllowed)
	if head.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("HEAD Allow=%q, want GET", head.Header().Get("Allow"))
	}
}

func TestSiteConfigHTTPPatchStrictnessReplayAndErrors(t *testing.T) {
	store, registrar := newSiteConfigHTTPFixture(t)
	headers := map[string][]string{"Idempotency-Key": {"site-config-http-valid-key-01"}}
	first := siteConfigHTTPRequest(t, registrar.mux, http.MethodPatch, "/admin/api/site-config/site_name", []byte(`{"value":"HTTP Site"}`), headers)
	if first.Code != http.StatusOK {
		t.Fatalf("valid patch status=%d body=%s", first.Code, first.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	responseKeys := make([]string, 0, len(response))
	for key := range response {
		responseKeys = append(responseKeys, key)
	}
	sort.Strings(responseKeys)
	if !reflect.DeepEqual(responseKeys, []string{"key", "revision", "value"}) || response["key"] != KeySiteName || response["value"] != "HTTP Site" {
		t.Fatalf("patch response=%v", response)
	}
	if first.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("patch Cache-Control=%q", first.Header().Get("Cache-Control"))
	}
	firstRevision := siteConfigRevision(t, store)
	replay := siteConfigHTTPRequest(t, registrar.mux, http.MethodPatch, "/admin/api/site-config/site_name", []byte(` { "value" : "HTTP Site" } `), headers)
	if replay.Code != http.StatusOK || !bytes.Equal(replay.Body.Bytes(), first.Body.Bytes()) || siteConfigRevision(t, store) != firstRevision {
		t.Fatalf("replay status=%d body=%q first=%q revision=%d", replay.Code, replay.Body.Bytes(), first.Body.Bytes(), siteConfigRevision(t, store))
	}

	invalidRequests := []struct {
		name, path string
		body       []byte
		headers    map[string][]string
		status     int
		code       string
	}{
		{"query", "/admin/api/site-config/site_name?x=1", []byte(`{"value":"x"}`), headers, 400, httperr.CodeInvalidRequest},
		{"missing idempotency", "/admin/api/site-config/site_name", []byte(`{"value":"x"}`), nil, 400, httperr.CodeInvalidRequest},
		{"duplicate idempotency", "/admin/api/site-config/site_name", []byte(`{"value":"x"}`), map[string][]string{"Idempotency-Key": {"site-config-duplicate-key-a", "site-config-duplicate-key-b"}}, 400, httperr.CodeInvalidRequest},
		{"short idempotency", "/admin/api/site-config/site_name", []byte(`{"value":"x"}`), map[string][]string{"Idempotency-Key": {"short"}}, 400, httperr.CodeInvalidRequest},
		{"invalid idempotency", "/admin/api/site-config/site_name", []byte(`{"value":"x"}`), map[string][]string{"Idempotency-Key": {"site config invalid key 001"}}, 400, httperr.CodeInvalidRequest},
		{"empty", "/admin/api/site-config/site_name", []byte{}, headers, 400, httperr.CodeInvalidRequest},
		{"missing value", "/admin/api/site-config/site_name", []byte(`{}`), headers, 400, httperr.CodeInvalidRequest},
		{"unknown field", "/admin/api/site-config/site_name", []byte(`{"value":"x","extra":1}`), headers, 400, httperr.CodeInvalidRequest},
		{"duplicate field", "/admin/api/site-config/site_name", []byte(`{"value":"x","value":"y"}`), headers, 400, httperr.CodeInvalidRequest},
		{"escaped duplicate", "/admin/api/site-config/site_name", []byte(`{"value":"x","\u0076alue":"y"}`), headers, 400, httperr.CodeInvalidRequest},
		{"trailing", "/admin/api/site-config/site_name", []byte(`{"value":"x"} {}`), headers, 400, httperr.CodeInvalidRequest},
		{"array", "/admin/api/site-config/site_name", []byte(`[{"value":"x"}]`), headers, 400, httperr.CodeInvalidRequest},
		{"null root", "/admin/api/site-config/site_name", []byte(`null`), headers, 400, httperr.CodeInvalidRequest},
		{"invalid utf8", "/admin/api/site-config/site_name", []byte{'{', '"', 'v', 'a', 'l', 'u', 'e', '"', ':', '"', 0xff, '"', '}'}, headers, 400, httperr.CodeInvalidRequest},
		{"invalid scalar", "/admin/api/site-config/default_endpoint_limit", []byte(`{"value":10001}`), headers, 400, httperr.CodeInvalidRequest},
		{"dedicated", "/admin/api/site-config/maintenance_mode", []byte(`{"value":true}`), headers, 409, httperr.CodeConflict},
		{"unknown key", "/admin/api/site-config/default_locale", []byte(`{"value":"zh"}`), headers, 404, httperr.CodeNotFound},
	}
	for _, test := range invalidRequests {
		t.Run(test.name, func(t *testing.T) {
			beforeRevision := siteConfigRevision(t, store)
			beforeRecords := siteConfigIdempotencyCount(t, store)
			recorder := siteConfigHTTPRequest(t, registrar.mux, http.MethodPatch, test.path, test.body, test.headers)
			assertSiteConfigHTTPError(t, recorder, test.status, test.code)
			if siteConfigRevision(t, store) != beforeRevision || siteConfigIdempotencyCount(t, store) != beforeRecords {
				t.Fatal("rejected HTTP request changed revision or idempotency records")
			}
		})
	}

	large := []byte(`{"value":"` + strings.Repeat("x", idempotency.MaxControlBodyBytes) + `"}`)
	assertSiteConfigHTTPError(t, siteConfigHTTPRequest(t, registrar.mux, http.MethodPatch, "/admin/api/site-config/site_name", large, headers), http.StatusRequestEntityTooLarge, httperr.CodePayloadTooLarge)
	wrongMethod := siteConfigHTTPRequest(t, registrar.mux, http.MethodPut, "/admin/api/site-config/site_name", []byte(`{"value":"x"}`), headers)
	if wrongMethod.Code != http.StatusMethodNotAllowed || wrongMethod.Header().Get("Allow") != http.MethodPatch {
		t.Fatalf("wrong PATCH method status=%d Allow=%q", wrongMethod.Code, wrongMethod.Header().Get("Allow"))
	}

	differentDigest := siteConfigHTTPRequest(t, registrar.mux, http.MethodPatch, "/admin/api/site-config/site_name", []byte(`{"value":"Different"}`), headers)
	assertSiteConfigHTTPError(t, differentDigest, http.StatusConflict, httperr.CodeConflict)
	if got, _ := siteConfigRawValue(t, store, KeySiteName); got != "HTTP Site" {
		t.Fatalf("different digest changed site name to %q", got)
	}

	if revision, ok := response["revision"].(string); !ok || revision == "" {
		t.Fatalf("patch response revision=%#v, want non-empty string", response["revision"])
	}
}

func TestSiteConfigHTTPBatchRejectsAmbiguousBodiesWithoutWrites(t *testing.T) {
	store, registrar := newSiteConfigHTTPFixture(t)
	headers := map[string][]string{"Idempotency-Key": {"site-config-batch-http-key-01"}}
	for _, body := range []string{
		`{}`, `null`, `[]`,
		`{"expected_revision":0,"values":{"site_name":"x"}}`,
		`{"expected_revision":"00","values":{"site_name":"x"}}`,
		`{"expected_revision":"0","values":null}`,
		`{"expected_revision":"0","values":{}}`,
		`{"expected_revision":"0","values":{"site_name":"x"},"extra":true}`,
		`{"expected_revision":"0","expected_revision":"1","values":{"site_name":"x"}}`,
		`{"expected_revision":"0","values":{"site_name":"x","site_name":"y"}}`,
		`{"expected_revision":"0","values":{"site_name":"x","\u0073ite_name":"y"}}`,
		`{"expected_revision":"0","values":{"site_name":{"nested":true}}}`,
		`{"expected_revision":"0","values":{"site_name":"x"}} {}`,
		`{"expected_revision":"0","values":{"site_name":"` + strings.Repeat("x", idempotency.MaxControlBodyBytes) + `"}}`,
	} {
		beforeRevision, beforeRecords := siteConfigRevision(t, store), siteConfigIdempotencyCount(t, store)
		recorder := siteConfigHTTPRequest(t, registrar.mux, http.MethodPatch, RouteAdminSiteConfig, []byte(body), headers)
		if len(body) > idempotency.MaxControlBodyBytes {
			assertSiteConfigHTTPError(t, recorder, http.StatusRequestEntityTooLarge, httperr.CodePayloadTooLarge)
		} else {
			assertSiteConfigHTTPError(t, recorder, http.StatusBadRequest, httperr.CodeInvalidRequest)
		}
		if siteConfigRevision(t, store) != beforeRevision || siteConfigIdempotencyCount(t, store) != beforeRecords {
			t.Fatal("rejected batch changed configuration or receipts")
		}
	}
	valid := []byte(`{"expected_revision":"0","values":{"site_name":"Batch Site"}}`)
	for _, requestHeaders := range []map[string][]string{nil, {"Idempotency-Key": {"site-config-batch-header-a", "site-config-batch-header-b"}}} {
		assertSiteConfigHTTPError(t, siteConfigHTTPRequest(t, registrar.mux, http.MethodPatch, RouteAdminSiteConfig, valid, requestHeaders), http.StatusBadRequest, httperr.CodeInvalidRequest)
	}
	assertSiteConfigHTTPError(t, siteConfigHTTPRequest(t, registrar.mux, http.MethodPatch, RouteAdminSiteConfig+"?x=1", valid, headers), http.StatusBadRequest, httperr.CodeInvalidRequest)
}

func TestSiteConfigHTTPDonationNoticeRoundTrip(t *testing.T) {
	store, registrar := newSiteConfigHTTPFixture(t)
	headers := map[string][]string{"Idempotency-Key": {"site-config-donation-notice-01"}}
	const notice = "Welcome donors.\nPlease review costs before sharing."
	body, err := json.Marshal(map[string]string{"value": notice})
	if err != nil {
		t.Fatal(err)
	}
	recorder := siteConfigHTTPRequest(t, registrar.mux, http.MethodPatch, "/admin/api/site-config/"+KeyCharityDonationNoticeEn, body, headers)
	if recorder.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	public, err := ReadPublicConfig(store)
	if err != nil {
		t.Fatal(err)
	}
	if public.CharityDonationNoticeEn != notice {
		t.Fatalf("public donation notice=%q want %q", public.CharityDonationNoticeEn, notice)
	}
}
