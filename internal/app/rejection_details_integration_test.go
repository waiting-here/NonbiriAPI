package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
)

type rejectionStewardReader struct{ denied bool }

func (a rejectionStewardReader) AuthorizeStewardRead(context.Context, *sql.Tx, int64) error {
	if a.denied {
		return logapi.ErrForbidden
	}
	return nil
}

func TestHTTPRejectionDetailsPersistAndRemainManagementOnly(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	f.exec(t, "UPDATE users SET rpm_limit=100")
	login := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	if login.Code != 200 {
		t.Fatal(login.Code, login.Body)
	}
	cookies := []*http.Cookie{responseCookieNamed(t, login, auth.AdminSessionCookieName)}
	for _, tc := range []struct{ name, path, body, contentType, field, reason string }{
		{"media", "/v1/chat/completions", `{}`, "text/plain", "Content-Type", "expected application/json with optional UTF-8 charset"},
		{"invalid JSON", "/v1/chat/completions", `{"PRIVATE_INPUT":`, "application/json", "body", "expected one valid UTF-8 JSON object"},
		{"duplicate unknown field", "/v1/chat/completions", `{"PRIVATE_NAME":1,"PRIVATE_NAME":2}`, "application/json", "body", "duplicate top-level field"},
		{"charity messages type", "/v1/chat/completions", `{"model":"[公益]provider/per_request","messages":{"PRIVATE_NAME":"PRIVATE_INPUT"}}`, "application/json", "messages", "expected an array of message objects"},
		{"charity text block type", "/v1/chat/completions", `{"model":"[公益]provider/per_request","messages":[{"role":"user","content":[{"type":"text","text":123}]}]}`, "application/json", "messages[].content[]", "content block type and text must be strings"},
		{"missing model", "/v1/chat/completions", `{"messages":[{"content":"PRIVATE_INPUT"}]}`, "application/json", "model", "required field is missing"},
		{"model type", "/v1/chat/completions", `{"model":123,"PRIVATE_NAME":"PRIVATE_INPUT"}`, "application/json", "model", "expected a nonempty model name of at most 133 characters without control characters"},
		{"stream value", "/v1/chat/completions", `{"model":"provider/self","stream":"PRIVATE_INPUT"}`, "application/json", "stream", "expected a boolean or null"},
		{"missing embedding input", "/v1/embeddings", `{"model":"provider/self"}`, "application/json", "input", "required field is missing"},
		{"embedding format", "/v1/embeddings", `{"model":"provider/self","input":"PRIVATE_INPUT","encoding_format":"PRIVATE_INPUT"}`, "application/json", "encoding_format", "expected float or base64"},
		{"embedding dimensions", "/v1/embeddings", `{"model":"provider/self","input":"PRIVATE_INPUT","dimensions":0}`, "application/json", "dimensions", "expected a positive integer up to 2147483647"},
		{"query", "/v1/chat/completions?PRIVATE_NAME=PRIVATE_INPUT", `{}`, "application/json", "query", "query parameters are not supported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := http.NewRequest("POST", f.server.URL+tc.path, strings.NewReader(tc.body))
			r.Host = auditUserHost
			r.Header.Set("Authorization", "Bearer "+f.caller)
			r.Header.Set("Content-Type", tc.contentType)
			response, err := f.server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 400 || !strings.Contains(string(body), "invalid_request") {
				t.Fatal(response.StatusCode, string(body))
			}
			id := response.Header.Get("X-Request-ID")
			var diag string
			var attempts, charges int
			if err := f.store.DB().QueryRow(`SELECT error_diag,attempt_count,(SELECT count(*) FROM credit_operations WHERE source_type='logical_request' AND source_id=logical_request_id) FROM request_logs WHERE logical_request_id=?`, id).Scan(&diag, &attempts, &charges); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(diag, "PRIVATE_") || strings.Contains(diag, f.caller) || attempts != 0 || charges != 0 {
				t.Fatal("unsafe or billed rejection", diag, attempts, charges)
			}
			// Reopen each projection from persisted rows, without the ingress context.
			read := testApplicationRequest(t, f.app.handler, "GET", auditAdminHost, "/admin/api/logs/"+id, "", cookies, nil)
			var admin logapi.AdminLogDetail
			if read.Code != 200 || json.Unmarshal(read.Body.Bytes(), &admin) != nil {
				t.Fatal(read.Code, read.Body)
			}
			denied := testApplicationRequest(t, f.app.handler, "GET", auditAdminHost, "/admin/api/logs/"+id, "", nil, map[string]string{"Authorization": "Bearer " + f.caller})
			if denied.Code != 401 {
				t.Fatal("caller key accessed administrator detail", denied.Code)
			}
			detail := admin.Request.RejectionDetail
			if detail == nil || detail.Field != tc.field || detail.Reason != tc.reason {
				t.Fatal("wrong persisted check", detail)
			}
			steward, err := f.app.logs.GetSteward(context.Background(), f.userID, id, logapi.AttemptFilter{}, rejectionStewardReader{})
			if err != nil || steward.Request.RejectionDetail == nil || *steward.Request.RejectionDetail != *detail {
				t.Fatal(steward, err)
			}
			if _, err := f.app.logs.GetSteward(context.Background(), f.userID, id, logapi.AttemptFilter{}, rejectionStewardReader{denied: true}); !errors.Is(err, logapi.ErrForbidden) {
				t.Fatal("unauthorized diagnostic", err)
			}
			own, err := f.app.logs.GetUser(context.Background(), f.userID, id, logapi.AttemptFilter{})
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(own)
			if strings.Contains(string(encoded), "rejection_detail") || strings.Contains(string(encoded), tc.reason) {
				t.Fatal("ordinary user received management detail", string(encoded))
			}
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.upstreamRequests) != 0 {
		t.Fatal("rejection dispatched")
	}
}
