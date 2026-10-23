package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/returnmarker"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestApplicationReturnedMarkerContinuesWithoutExposingToken(t *testing.T) {
	markers := make(chan string, 3)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		markers <- r.Header.Get(returnmarker.Header)
		w.Header().Set(returnmarker.Header, r.Header.Get(returnmarker.Header))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"result","object":"chat.completion","created":1,"model":"private-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	}))
	defer upstream.Close()
	vault, err := secret.New(bytes.Repeat([]byte{0x63}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	path := filepath.Join(t.TempDir(), "returns.sqlite")
	dbfixture.Materialize(t, path, vault)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{upstream.URL}})
	if err != nil {
		t.Fatal(err)
	}
	app, err := buildApplicationWithRuntimeOptions(context.Background(), auditConfig(), store, vault, applicationRuntimeOptions{Egress: stack})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	login := testApplicationRequest(t, app.handler, "POST", auditAdminHost, "/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	cookie := responseCookieNamed(t, login, auth.AdminSessionCookieName)
	disable := testApplicationRequest(t, app.handler, "POST", auditAdminHost, "/admin/api/maintenance/disable", `{"expected_revision":"1","reason":"inference test"}`, []*http.Cookie{cookie}, map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditAdminHost, "Idempotency-Key": strings.Repeat("M", 22)})
	if disable.Code != 200 {
		t.Fatal("disable maintenance", disable.Code, disable.Body.String())
	}
	f := &embeddingHTTPFixture{store: store, app: app, caller: seedRootCallerIdentity(t, store)}
	if err := store.DB().QueryRow(`SELECT user_id FROM caller_keys WHERE generation=1`).Scan(&f.userID); err != nil {
		t.Fatal(err)
	}
	f.seedModels(t, vault, upstream.URL+"/v1", "openai-compatible")
	f.exec(t, `UPDATE site_config SET value='0' WHERE key='charity_min_chars'`)
	call := func(model, marker string) *httptest.ResponseRecorder {
		t.Helper()
		headers := map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + f.caller}
		if marker != "" {
			headers[returnmarker.Header] = marker
		}
		body, _ := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "hello"}}})
		response := testApplicationRequest(t, app.handler, "POST", auditUserHost, "/v1/chat/completions", string(body), nil, headers)
		if response.Code != 200 || response.Header().Get(returnmarker.Header) != "" || marker != "" && strings.Contains(response.Body.String(), marker) {
			t.Fatal("request rejected or exposed marker", response.Code, response.Body.String())
		}
		return response
	}
	first := call("[公益]provider/per_request", "")
	marker := <-markers
	verifier, _ := returnmarker.New(vault)
	source, ok := verifier.Verify(http.Header{returnmarker.Header: {marker}})
	if !ok || source.RequestID != first.Header().Get("X-Request-ID") || source.EndpointKeyID != f.keyID || source.DonationKeyID == 0 {
		t.Fatal("source is not the selected charity claim")
	}
	var count int
	if err := store.DB().QueryRow(`SELECT count(*) FROM admin_alerts WHERE ref LIKE 'return-detected:%'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("normal outbound call raised alert", err)
	}
	second := call("provider/self", marker)
	<-markers
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := store.DB().QueryRow(`SELECT count(*) FROM admin_alerts WHERE ref LIKE 'return-detected:%' AND resolved=0 AND subject_user_id=?`, f.userID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("return alert was not persisted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var body string
	if err := store.DB().QueryRow(`SELECT context_json FROM admin_alerts WHERE ref LIKE 'return-detected:%'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, source.RequestID) || !strings.Contains(body, second.Header().Get("X-Request-ID")) || strings.Contains(body, marker) {
		t.Fatal("alert omitted source or retained live token")
	}
	call("provider/self", marker)
	<-markers
	if err := store.DB().QueryRow(`SELECT count(*) FROM admin_alerts WHERE ref LIKE 'return-detected:%'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("same source was not deduplicated", err)
	}
}
