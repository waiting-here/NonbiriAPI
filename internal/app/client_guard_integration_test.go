package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestClientRuleGuardRejectsFirstModelCallBeforeAcceptance(t *testing.T) {
	for _, model := range []string{"provider/self", "[公益]provider/per_request"} {
		t.Run(model, func(t *testing.T) {
			payload := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Hello"}]}`, model)
			f := newEmbeddingHTTPFixture(t)
			now := time.Now().Unix()
			ruleID, err := db.GenerateOpaqueID("rsk_")
			if err != nil {
				t.Fatal(err)
			}
			f.exec(t, `UPDATE site_config SET value='1' WHERE key='charity_min_chars'`)
			f.exec(t, `INSERT INTO risk_client_rules(id,name,status,enabled,revision,conditions_json,evidence_note,evidence_url,created_by_role,updated_by_role,created_at,updated_at) VALUES(?,'restricted test client','suspected',1,1,?,'','','admin','admin',?,?)`, ruleID, `[{"field":"user_agent","operator":"prefix","value":"Go-http-client/","case_sensitive":false}]`, now, now)
			f.exec(t, `INSERT INTO client_rule_auto_bans(rule_id,enabled,duration_seconds,revision,updated_at) VALUES(?,1,3600,1,?)`, ruleID, now)
			// Discovery does not trigger a live rule action.
			r, _ := http.NewRequest(http.MethodGet, f.server.URL+"/v1/models", nil)
			r.Host = auditUserHost
			r.Header.Set("Authorization", "Bearer "+f.caller)
			response, err := f.server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("model discovery: %d", response.StatusCode)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			active, release, err := f.forward.lifecycle.Admit(ctx, f.userID, "active-fixture", func(context.Context, int64, string) (bool, error) { return true, nil })
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			result := make(chan struct {
				status int
				body   []byte
			}, 1)
			go func() {
				status, body := f.post(t, "/v1/chat/completions", payload)
				result <- struct {
					status int
					body   []byte
				}{status, body}
			}()
			select {
			case <-active.Done():
			case got := <-result:
				t.Fatalf("guard returned without draining: %d %s", got.status, got.body)
			case <-ctx.Done():
				t.Fatal("client rule guard did not cancel the active request")
			}
			var banned int
			if err := f.store.DB().QueryRow(`SELECT is_banned FROM users WHERE id=?`, f.userID).Scan(&banned); err != nil || banned != 0 {
				t.Fatal("ban crossed the active request barrier", banned, err)
			}
			// While the first match drains existing work, another matching request
			// must not enter a second punishment transaction.
			if status, body := f.post(t, "/v1/chat/completions", payload); status != http.StatusUnauthorized {
				t.Fatalf("concurrent call passed the restriction barrier: %d %s", status, body)
			}
			release()
			select {
			case got := <-result:
				if got.status != http.StatusForbidden || !strings.Contains(string(got.body), "account is banned until") || strings.Contains(string(got.body), ruleID) || strings.Contains(string(got.body), "Go-http-client") {
					t.Fatalf("unsafe or incorrect ban response: %d %s", got.status, got.body)
				}
			case <-ctx.Done():
				t.Fatal("guard waited for its own request admission")
			}
			var requestID, stage, accounting string
			var charges, attempts, keyRevoked int
			if err := f.store.DB().QueryRow(`SELECT r.request_id,l.rejection_stage,l.accounting_state,(SELECT count(*) FROM credit_operations WHERE source_type='logical_request' AND source_id=l.id),q.attempt_count FROM client_rule_ban_receipts r JOIN logical_requests l ON l.id=r.request_id JOIN request_logs q ON q.logical_request_id=l.id WHERE r.user_id=?`, f.userID).Scan(&requestID, &stage, &accounting, &charges, &attempts); err != nil {
				t.Fatal(err)
			}
			if !db.ValidateOpaqueID(requestID, "req_") || stage != "preflight" || accounting != "none" || charges != 0 || attempts != 0 {
				t.Fatalf("guard accepted a claim: %s %s %s %d %d", requestID, stage, accounting, charges, attempts)
			}
			var generation, receiptCount int
			if err := f.store.DB().QueryRow(`SELECT u.is_banned,k.key_hash IS NULL,k.generation,(SELECT count(*) FROM client_rule_ban_receipts WHERE user_id=u.id) FROM users u JOIN caller_keys k ON k.user_id=u.id WHERE u.id=?`, f.userID).Scan(&banned, &keyRevoked, &generation, &receiptCount); err != nil || banned != 1 || keyRevoked != 1 || generation != 2 || receiptCount != 1 {
				t.Fatal("guard did not revoke authority exactly once", banned, keyRevoked, generation, receiptCount, err)
			}
			f.mu.Lock()
			dispatched := len(f.upstreamRequests)
			f.mu.Unlock()
			if dispatched != 0 {
				t.Fatalf("blocked request reached upstream: %d", dispatched)
			}
			if status, _ := f.post(t, "/v1/chat/completions", `{"model":"provider/self","messages":[{"role":"user","content":"Hello"}]}`); status != http.StatusUnauthorized {
				t.Fatalf("revoked key still authorized: %d", status)
			}
			retention := riskRetention{repository: f.app.audits.risk, clientGuard: f.forward.clientGuard}
			for count := 0; count < 10; count++ {
				work, err := retention.Retain(ctx, now+91*24*60*60, 2, time.Now().Add(5*time.Second))
				if err != nil || work.Processed > 2 {
					t.Fatal("unbounded receipt cleanup", work, err)
				}
				if !work.More {
					break
				}
			}
			var receipts int
			if err := f.store.DB().QueryRow(`SELECT count(*) FROM client_rule_ban_receipts WHERE request_id=?`, requestID).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatalf("expired receipt remains: %d (%v)", receipts, err)
			}
		})
	}
}

func TestClientRuleGuardPrecedesCharityBusinessRejections(t *testing.T) {
	const chat = `{"model":"[公益]provider/per_request","messages":[{"role":"user","content":"x"}]}`
	for _, tc := range []struct {
		name, path, body, minimum, pattern, code   string
		bindingEnabled, expensive, unbound, banned bool
		status                                     int
	}{
		{"insufficient balance", "/v1/chat/completions", chat, "1", "Go-http-client/", httperr.CodeForbidden, true, true, false, true, http.StatusForbidden},
		{"short content", "/v1/chat/completions", chat, "10", "Go-http-client/", httperr.CodeForbidden, true, false, false, true, http.StatusForbidden},
		{"embedding balance", "/v1/embeddings", `{"model":"[公益]provider/per_request","input":"x"}`, "10", "Go-http-client/", httperr.CodeForbidden, true, true, false, true, http.StatusForbidden},
		{"image balance", "/v1/images/generations", `{"model":"[公益]provider/per_request","prompt":"x"}`, "10", "Go-http-client/", httperr.CodeForbidden, true, true, false, true, http.StatusForbidden},
		{"no available binding", "/v1/chat/completions", chat, "1", "Go-http-client/", httperr.CodeForbidden, true, false, true, true, http.StatusForbidden},
		{"unmatched balance", "/v1/chat/completions", chat, "1", "Other-client/", httperr.CodeInsufficientCredits, true, true, false, false, http.StatusForbidden},
		{"disabled automatic ban", "/v1/chat/completions", chat, "1", "Go-http-client/", httperr.CodeInsufficientCredits, false, true, false, false, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEmbeddingHTTPFixture(t)
			f.exec(t, `UPDATE site_config SET value=? WHERE key='charity_min_chars'`, tc.minimum)
			ruleID := configureClientGuardRule(t, f, tc.pattern, tc.bindingEnabled)
			f.exec(t, `UPDATE charity_models SET model_types=7 WHERE full_name='[公益]provider/per_request'`)
			if tc.expensive {
				f.exec(t, `UPDATE charity_models SET request_user_price=2000000 WHERE full_name='[公益]provider/per_request'`)
			}
			if tc.unbound {
				f.exec(t, `DELETE FROM charity_model_bindings`)
			}
			var operationsBefore int
			if err := f.store.DB().QueryRow(`SELECT count(*) FROM credit_operations`).Scan(&operationsBefore); err != nil {
				t.Fatal(err)
			}
			status, body := f.post(t, tc.path, tc.body)
			if status != tc.status || !strings.Contains(string(body), fmt.Sprintf(`"code":%q`, tc.code)) {
				t.Fatalf("response=%d %s, want %d %s", status, body, tc.status, tc.code)
			}
			if strings.Contains(string(body), ruleID) || strings.Contains(string(body), "Go-http-client") {
				t.Fatalf("caller learned a client rule: %s", body)
			}
			var banned, keyRevoked, receipts, charges, requests, logs, attempts, penalties int
			err := f.store.DB().QueryRow(`SELECT u.is_banned,k.key_hash IS NULL,
(SELECT count(*) FROM client_rule_ban_receipts WHERE user_id=u.id),
(SELECT count(*) FROM credit_operations),
(SELECT count(*) FROM logical_requests WHERE user_id=u.id),
(SELECT count(*) FROM request_logs WHERE user_id=u.id),
(SELECT count(*) FROM dispatch_claims),
(SELECT count(*) FROM credit_operations WHERE kind='anti_abuse_penalty')
FROM users u JOIN caller_keys k ON k.user_id=u.id WHERE u.id=?`, f.userID).
				Scan(&banned, &keyRevoked, &receipts, &charges, &requests, &logs, &attempts, &penalties)
			if err != nil {
				t.Fatal(err)
			}
			wantBan := 0
			if tc.banned {
				wantBan = 1
			}
			if banned != wantBan || keyRevoked != wantBan || receipts != wantBan || charges != operationsBefore || requests != 1 || logs != 1 || attempts != 0 || penalties != 0 {
				t.Fatalf("effects: banned=%d keyRevoked=%d receipts=%d operations=%d/%d requests=%d logs=%d attempts=%d penalties=%d", banned, keyRevoked, receipts, charges, operationsBefore, requests, logs, attempts, penalties)
			}
			var accounting, reason string
			var reserved int64
			if err := f.store.DB().QueryRow(`SELECT accounting_state,account_reserved_milli,rejection_reason FROM logical_requests WHERE user_id=?`, f.userID).Scan(&accounting, &reserved, &reason); err != nil {
				t.Fatal(err)
			}
			if accounting != "none" || reserved != 0 || reason != tc.code {
				t.Fatalf("incorrect rejection accounting=%s reserved=%d reason=%s", accounting, reserved, reason)
			}
			f.mu.Lock()
			dispatched := len(f.upstreamRequests)
			f.mu.Unlock()
			if dispatched != 0 {
				t.Fatalf("rejected request reached upstream %d times", dispatched)
			}
			if tc.banned {
				if status, _ := f.post(t, tc.path, tc.body); status != http.StatusUnauthorized {
					t.Fatalf("revoked credential accepted repeated request: %d", status)
				}
				var after int
				if err := f.store.DB().QueryRow(`SELECT count(*) FROM client_rule_ban_receipts WHERE user_id=?`, f.userID).Scan(&after); err != nil || after != 1 {
					t.Fatal("repeated request changed punishment", after, err)
				}
			}
		})
	}
}

func configureClientGuardRule(t *testing.T, f *embeddingHTTPFixture, pattern string, enabled bool) string {
	t.Helper()
	now := time.Now().Unix()
	ruleID, err := db.GenerateOpaqueID("rsk_")
	if err != nil {
		t.Fatal(err)
	}
	conditions := fmt.Sprintf(`[{"field":"user_agent","operator":"prefix","value":%q,"case_sensitive":false}]`, pattern)
	f.exec(t, `INSERT INTO risk_client_rules(id,name,status,enabled,revision,conditions_json,evidence_note,evidence_url,created_by_role,updated_by_role,created_at,updated_at) VALUES(?,'restricted test client','suspected',1,1,?,'','','admin','admin',?,?)`, ruleID, conditions, now, now)
	f.exec(t, `INSERT INTO client_rule_auto_bans(rule_id,enabled,duration_seconds,revision,updated_at) VALUES(?,?,3600,1,?)`, ruleID, enabled, now)
	return ruleID
}

func TestPersonalClientRuleGuardScopeAndEntryBoundaries(t *testing.T) {
	const chat = `{"model":"provider/self","messages":[{"role":"user","content":"Hello"}]}`
	for _, tc := range []struct {
		name, path, body string
		setup            func(*testing.T, *embeddingHTTPFixture, string)
		status           int
		banned           bool
	}{
		{"chat", "/v1/chat/completions", chat, nil, 403, true},
		{"model ID", "/v1/chat/completions", chat, nil, 403, true},
		{"stream", "/v1/chat/completions", `{"model":"provider/self","messages":[{"role":"user","content":"Hello"}],"stream":true}`, nil, 403, true},
		{"embeddings", "/v1/embeddings", `{"model":"provider/self","input":"Hello"}`, nil, 403, true},
		{"images", "/v1/images/generations", `{"model":"provider/self","prompt":"Hello"}`, nil, 403, true},
		{"no candidate", "/v1/chat/completions", chat, func(t *testing.T, f *embeddingHTTPFixture, _ string) {
			f.exec(t, `DELETE FROM model_bindings`)
		}, 403, true},
		{"disabled binding", "/v1/chat/completions", chat, func(t *testing.T, f *embeddingHTTPFixture, rule string) {
			f.exec(t, `UPDATE client_rule_auto_bans SET enabled=0 WHERE rule_id=?`, rule)
		}, 200, false},
		{"disabled rule", "/v1/chat/completions", chat, func(t *testing.T, f *embeddingHTTPFixture, rule string) {
			f.exec(t, `UPDATE risk_client_rules SET enabled=0 WHERE id=?`, rule)
		}, 200, false},
		{"unmatched", "/v1/chat/completions", chat, func(t *testing.T, f *embeddingHTTPFixture, rule string) {
			f.exec(t, `UPDATE risk_client_rules SET conditions_json='[{"field":"user_agent","operator":"equals","value":"Other/1","case_sensitive":false}]' WHERE id=?`, rule)
		}, 200, false},
		{"malformed", "/v1/chat/completions", `{"model":"provider/self","messages":`, nil, 400, false},
		{"missing model", "/v1/chat/completions", `{"model":"provider/missing","messages":[]}`, nil, 404, false},
		{"another owner", "/v1/chat/completions", chat, func(t *testing.T, f *embeddingHTTPFixture, _ string) {
			f.exec(t, `UPDATE models SET user_id=(SELECT id FROM users WHERE id<>? AND is_admin=0 LIMIT 1) WHERE user_id=?`, f.userID, f.userID)
		}, 404, false},
		{"invalid credential", "/v1/chat/completions", chat, func(_ *testing.T, f *embeddingHTTPFixture, _ string) {
			f.caller = "invalid-caller-key"
		}, 401, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEmbeddingHTTPFixture(t)
			f.exec(t, `UPDATE models SET model_types=7`)
			rule := configureClientGuardRule(t, f, "Go-http-client/", true)
			if tc.setup != nil {
				tc.setup(t, f, rule)
			}
			payload := tc.body
			if tc.name == "model ID" {
				var modelID int64
				if err := f.store.DB().QueryRow("SELECT id FROM models WHERE user_id=?", f.userID).Scan(&modelID); err != nil {
					t.Fatal(err)
				}
				payload = strings.Replace(payload, "provider/self", fmt.Sprint(modelID), 1)
			}
			status, body := f.post(t, tc.path, payload)
			if status != tc.status {
				t.Fatalf("response=%d %s, want %d", status, body, tc.status)
			}
			var banned, receipts, revoked int
			if err := f.store.DB().QueryRow(`SELECT u.is_banned,k.key_hash IS NULL,(SELECT count(*) FROM client_rule_ban_receipts WHERE user_id=u.id) FROM users u JOIN caller_keys k ON k.user_id=u.id WHERE u.id=?`, f.userID).Scan(&banned, &revoked, &receipts); err != nil {
				t.Fatal(err)
			}
			want := 0
			if tc.banned {
				want = 1
			}
			if banned != want || revoked != want || receipts != want {
				t.Fatalf("ban=%d revoked=%d receipts=%d want=%d", banned, revoked, receipts, want)
			}
			if tc.status != 200 {
				f.mu.Lock()
				dispatched := len(f.upstreamRequests)
				f.mu.Unlock()
				var claims, credits int
				if err := f.store.DB().QueryRow(`SELECT (SELECT count(*) FROM dispatch_claims),(SELECT count(*) FROM credit_operations WHERE source_type='logical_request')`).Scan(&claims, &credits); err != nil {
					t.Fatal(err)
				}
				if dispatched != 0 || claims != 0 || credits != 0 {
					t.Fatalf("rejected request effects: sends=%d claims=%d credits=%d", dispatched, claims, credits)
				}
			}
		})
	}
}

func TestPersonalClientRuleGuardUsesTrustedIP(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprint(trusted), func(t *testing.T) {
			f := newEmbeddingHTTPFixture(t)
			rule := configureClientGuardRule(t, f, "Other/", true)
			f.exec(t, `UPDATE risk_client_rules SET conditions_json=? WHERE id=?`, `[{"field":"effective_ip","operator":"ip_in","value":"","values":["203.0.113.9"],"case_sensitive":false}]`, rule)
			r := httptest.NewRequest(http.MethodPost, "http://"+auditUserHost+"/v1/chat/completions", strings.NewReader(`{"model":"provider/self","messages":[{"role":"user","content":"Hello"}]}`))
			r.RemoteAddr = "198.51.100.20:4242"
			if trusted {
				r.RemoteAddr = "127.0.0.1:4242"
			}
			r.Header.Set("Authorization", "Bearer "+f.caller)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Forwarded-For", "203.0.113.9")
			w := httptest.NewRecorder()
			f.server.Config.Handler.ServeHTTP(w, r)
			wantStatus, wantBan := http.StatusOK, 0
			if trusted {
				wantStatus, wantBan = http.StatusForbidden, 1
			}
			var banned, receipts int
			if err := f.store.DB().QueryRow(`SELECT is_banned,(SELECT count(*) FROM client_rule_ban_receipts WHERE user_id=?) FROM users WHERE id=?`, f.userID, f.userID).Scan(&banned, &receipts); err != nil {
				t.Fatal(err)
			}
			if w.Code != wantStatus || banned != wantBan || receipts != wantBan {
				t.Fatalf("response=%d %s banned=%d receipts=%d", w.Code, w.Body.String(), banned, receipts)
			}
		})
	}
}
