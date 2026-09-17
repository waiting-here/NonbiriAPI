package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPublicAPICORSHTTP(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	for _, path := range []string{"/v1/models", "/v1/chat/completions", "/v1/embeddings"} {
		method := http.MethodPost
		if path == "/v1/models" {
			method = http.MethodGet
		}
		r, err := http.NewRequest(http.MethodOptions, f.server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Host = auditUserHost
		r.Header.Set("Origin", "https://browser.example")
		r.Header.Set("Access-Control-Request-Method", method)
		r.Header.Set("Access-Control-Request-Headers", "authorization,content-type,x-stainless-lang")
		response, err := f.server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusNoContent || len(body) != 0 || response.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("preflight %s: status=%d headers=%v body=%s", path, response.StatusCode, response.Header, body)
		}
	}
	var logs int
	if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM request_logs").Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if logs != 0 || len(f.upstreamRequests) != 0 {
		t.Fatal("preflight created a call log or dispatched to the upstream")
	}
	for _, test := range []struct {
		name, key, body string
		status          int
	}{
		{"missing key", "", `{"model":"provider/self","input":"ping"}`, 401},
		{"invalid key", "nbk_invalid", `{"model":"provider/self","input":"ping"}`, 401},
		{"invalid body", f.caller, `{"model":"provider/self"}`, 400},
		{"embedding", f.caller, `{"model":"provider/self","input":"ping"}`, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, err := http.NewRequest(http.MethodPost, f.server.URL+"/v1/embeddings", strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			r.Host = auditUserHost
			r.Header.Set("Origin", "https://browser.example")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Cookie", "nb_session=unrelated-browser-session")
			if test.key != "" {
				r.Header.Set("Authorization", "Bearer "+test.key)
			}
			response, err := f.server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.status || response.Header.Get("Access-Control-Allow-Origin") != "*" || response.Header.Get("Access-Control-Allow-Credentials") != "" {
				t.Fatalf("status=%d headers=%v body=%s", response.StatusCode, response.Header, body)
			}
		})
	}
	if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM request_logs").Scan(&logs); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if logs != 1 || len(f.upstreamRequests) != 1 {
		t.Fatalf("logs=%d upstream requests=%d; only authenticated embedding should dispatch", logs, len(f.upstreamRequests))
	}
}
