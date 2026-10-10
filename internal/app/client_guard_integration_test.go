package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestClientRuleGuardRejectsFirstCharityCallBeforeAcceptance(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	now := time.Now().Unix()
	ruleID, err := db.GenerateOpaqueID("rsk_")
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE site_config SET value='1' WHERE key='charity_min_chars'`)
	f.exec(t, `INSERT INTO risk_client_rules(id,name,status,enabled,revision,conditions_json,evidence_note,evidence_url,created_by_role,updated_by_role,created_at,updated_at) VALUES(?,'restricted test client','suspected',1,1,?,'','','admin','admin',?,?)`, ruleID, `[{"field":"user_agent","operator":"prefix","value":"Go-http-client/","case_sensitive":false}]`, now, now)
	f.exec(t, `INSERT INTO client_rule_auto_bans(rule_id,enabled,duration_seconds,revision,updated_at) VALUES(?,1,3600,1,?)`, ruleID, now)
	// The same client may use personal endpoints and ordinary discovery.
	if status, body := f.post(t, "/v1/chat/completions", `{"model":"provider/self","messages":[{"role":"user","content":"Hello"}]}`); status != http.StatusOK {
		t.Fatalf("personal call: %d %s", status, body)
	}
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
		status, body := f.post(t, "/v1/chat/completions", `{"model":"[公益]provider/per_request","messages":[{"role":"user","content":"Hello"}]}`)
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
	if status, body := f.post(t, "/v1/chat/completions", `{"model":"[公益]provider/per_request","messages":[{"role":"user","content":"Hello"}]}`); status != http.StatusUnauthorized {
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
	if dispatched != 1 {
		t.Fatalf("charity request reached upstream: %d", dispatched)
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
