package main

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestAuthenticatedPrehandlerFactsHaveOneIdentityAndZeroCharge(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	if _, err := f.store.DB().Exec(`UPDATE users SET rpm_limit=100 WHERE id=?`, f.userID); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, tc := range []struct{ method, path, body, route string }{
		{"POST", "/v1/embeddings", `{"model":"provider/self"}`, "openai_embeddings"},
		{"POST", "/v1/chat/completions", `{"messages":[]}`, "openai_chat_completions"},
		{"GET", "/v1/models?unexpected=1", "", "model_discovery"},
	} {
		r, err := http.NewRequest(tc.method, f.server.URL+tc.path, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		r.Host = auditUserHost
		r.Header.Set("Authorization", "Bearer "+f.caller)
		r.Header.Set("X-Request-ID", "req_client_supplied")
		r.Header.Set("Content-Type", "application/json")
		response, err := f.server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		id := response.Header.Get("X-Request-ID")
		if response.StatusCode != 400 || !db.ValidateOpaqueID(id, "req_") || ids[id] {
			t.Fatal(response.StatusCode, id, string(body))
		}
		ids[id] = true
		var route, stage, method, path, accounting string
		var attempts, unknown, rows int
		if err := f.store.DB().QueryRow(`SELECT r.route_kind,r.rejection_stage,r.request_method,r.request_path,r.accounting_state,l.attempt_count,l.usage_unknown,(SELECT count(*) FROM credit_operations WHERE source_type='logical_request' AND source_id=r.id) FROM logical_requests r JOIN request_logs l ON l.logical_request_id=r.id WHERE r.id=? AND r.user_id=?`, id, f.userID).Scan(&route, &stage, &method, &path, &accounting, &attempts, &unknown, &rows); err != nil {
			t.Fatal(err)
		}
		if route != tc.route || stage != "preflight" || method != tc.method || path != strings.Split(tc.path, "?")[0] || accounting != "none" || attempts != 0 || unknown != 0 || rows != 0 {
			t.Fatal("invalid refusal fact", route, stage, method, path, accounting, attempts, unknown, rows)
		}
	}
	var total []byte
	if err := f.store.DB().QueryRow(`SELECT total_requests FROM users WHERE id=?`, f.userID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	count, err := db.DecodeU128(total)
	if err != nil || count.Big().String() != "3" {
		t.Fatal("request count", count, err)
	}
	for _, tc := range []struct{ method, path, key string }{{"GET", "/v1/models", "invalid"}, {"PUT", "/v1/embeddings", f.caller}, {"POST", "/v1/unknown", f.caller}} {
		r, _ := http.NewRequest(tc.method, f.server.URL+tc.path, nil)
		r.Host = auditUserHost
		r.Header.Set("Authorization", "Bearer "+tc.key)
		response, err := f.server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.Header.Get("X-Request-ID") != "" {
			t.Fatal("unattributable route created an attempt")
		}
	}
	var logs int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM request_logs`).Scan(&logs); err != nil || logs != 3 {
		t.Fatal("unattributable request counted", logs, err)
	}
}

func TestPrehandlerStorageFailureRollsBackFactAndTotal(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	if _, err := f.store.DB().Exec(`CREATE TRIGGER fail_rejection BEFORE INSERT ON request_logs BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	r, _ := http.NewRequest("POST", f.server.URL+"/v1/embeddings", strings.NewReader(`{}`))
	r.Host = auditUserHost
	r.Header.Set("Authorization", "Bearer "+f.caller)
	response, err := f.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 503 || !strings.Contains(string(body), "service_unavailable") {
		t.Fatal(response.StatusCode, string(body))
	}
	var facts int
	if err := f.store.DB().QueryRow(`SELECT (SELECT count(*) FROM logical_requests)+(SELECT count(*) FROM request_logs)`).Scan(&facts); err != nil || facts != 0 {
		t.Fatal("partial fact", facts, err)
	}
	var raw []byte
	if err := f.store.DB().QueryRow(`SELECT total_requests FROM users WHERE id=?`, f.userID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	total, err := db.DecodeU128(raw)
	if err != nil || total.Big().Sign() != 0 {
		t.Fatal("partial counter", total, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.upstreamRequests) != 0 {
		t.Fatal("failed rejection went upstream")
	}
}
