package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	auditUserHost  = "127.0.0.1"
	auditAdminHost = "127.0.0.2"
)

func auditConfig() *config.Config {
	return &config.Config{
		ListenAddr:        "127.0.0.1:18999",
		UserHost:          auditUserHost,
		AdminHost:         auditAdminHost,
		SiteBaseURL:       "https://" + auditUserHost,
		TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
	}
}

func assertFreshSafeApplication(t *testing.T, app *application) {
	t.Helper()
	for _, tc := range []struct {
		name, host, path string
		want             int
	}{
		{name: "user health", host: auditUserHost, path: "/healthz", want: http.StatusOK},
		{name: "admin health", host: auditAdminHost, path: "/healthz", want: http.StatusOK},
		{name: "public bootstrap", host: auditUserHost, path: "/api/config", want: http.StatusOK},
		{name: "public bootstrap rejects query", host: auditUserHost, path: "/api/config?unexpected=1", want: http.StatusBadRequest},
		{name: "endpoint API dormant", host: auditUserHost, path: "/api/endpoints", want: http.StatusNotFound},
		{name: "game API dormant", host: auditUserHost, path: "/api/games", want: http.StatusNotFound},
		{name: "export API dormant", host: auditUserHost, path: "/api/account/export", want: http.StatusNotFound},
		{name: "caller models dormant", host: auditUserHost, path: "/v1/models", want: http.StatusNotFound},
		{name: "caller chat dormant", host: auditUserHost, path: "/v1/chat/completions", want: http.StatusNotFound},
		{name: "admin bootstrap requires future ADM owner", host: auditAdminHost, path: "/admin/api/config", want: http.StatusNotFound},
		{name: "admin catalog dormant", host: auditAdminHost, path: "/admin/api/site-config", want: http.StatusNotFound},
		{name: "admin cannot reach user bootstrap", host: auditAdminHost, path: "/api/config", want: http.StatusNotFound},
		{name: "user cannot reach admin API", host: auditUserHost, path: "/admin/api/config", want: http.StatusNotFound},
		{name: "unknown host rejected", host: "198.51.100.10", path: "/", want: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := testHTTPResponse(t, app.handler, http.MethodGet, tc.host, tc.path)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if rec.Code == http.StatusServiceUnavailable {
				t.Fatalf("dormant Generation 2 route used a temporary 503: %s", rec.Body.String())
			}
		})
	}

	for _, tc := range []struct {
		host, path string
	}{
		{host: auditUserHost, path: "/"},
		{host: auditAdminHost, path: "/settings/profile"},
	} {
		rec := testHTTPResponse(t, app.handler, http.MethodGet, tc.host, tc.path)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("SPA %s%s status=%d content-type=%q", tc.host, tc.path, rec.Code, rec.Header().Get("Content-Type"))
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("SPA cache-control=%q", rec.Header().Get("Cache-Control"))
		}
	}

	rec := testHTTPResponse(t, app.handler, http.MethodGet, auditUserHost, "/api/config")
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("public config body=%s err=%v", rec.Body.String(), err)
	}
	gotKeys := make([]string, 0, len(body))
	for key := range body {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	wantKeys := []string{
		"announcement_epoch",
		"legal_authoritative_locale",
		"legal_privacy_override_en",
		"legal_privacy_override_zh",
		"legal_terms_override_en",
		"legal_terms_override_zh",
		"maintenance_mode",
		"registration_open",
		"site_logo_url",
		"site_name",
	}
	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("public config keys=%v want=%v body=%s", gotKeys, wantKeys, rec.Body.String())
	}
	if _, ok := body["default_locale"]; ok {
		t.Fatal("public config exposed deleted default_locale")
	}
	epoch, ok := body["announcement_epoch"].(string)
	if !ok || !strings.HasPrefix(epoch, "b1e_") || len(epoch) != 26 {
		t.Fatalf("announcement_epoch=%v", body["announcement_epoch"])
	}
	if _, ok := body["maintenance_mode"].(bool); !ok {
		t.Fatalf("maintenance_mode=%T", body["maintenance_mode"])
	}
	if _, ok := body["registration_open"].(bool); !ok {
		t.Fatalf("registration_open=%T", body["registration_open"])
	}

	bodyRequest := httptest.NewRequest(http.MethodGet, "http://wire.invalid/api/config", strings.NewReader("{}"))
	bodyRequest.Host = auditUserHost
	bodyRequest.RemoteAddr = "198.51.100.20:4242"
	bodyResponse := httptest.NewRecorder()
	app.handler.ServeHTTP(bodyResponse, bodyRequest)
	if bodyResponse.Code != http.StatusBadRequest {
		t.Fatalf("public config body status=%d want=%d body=%s", bodyResponse.Code, http.StatusBadRequest, bodyResponse.Body.String())
	}
}

func TestGenerationTwoFreshAndCurrentApplicationBoot(t *testing.T) {
	key := bytes.Repeat([]byte{0x5a}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vault.Close() }()

	dbPath := filepath.Join(t.TempDir(), "generation-two.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	for pass := 0; pass < 2; pass++ {
		store, err := db.Open(dbPath, vault)
		if err != nil {
			t.Fatalf("pass %d db.Open: %v", pass, err)
		}
		app, err := buildApplication(auditConfig(), store, vault)
		if err != nil {
			_ = store.Close()
			t.Fatalf("pass %d buildApplication: %v", pass, err)
		}
		assertFreshSafeApplication(t, app)

		closed := make(chan error, 1)
		go func() { closed <- app.Close() }()
		select {
		case closeErr := <-closed:
			if closeErr != nil {
				t.Fatalf("pass %d app.Close: %v", pass, closeErr)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("pass %d app.Close blocked; baseline must own no worker", pass)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("pass %d store.Close: %v", pass, err)
		}
	}
}
