package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
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
	if err := f.store.DB().QueryRow(`SELECT u.is_banned,k.key_hash IS NULL FROM users u JOIN caller_keys k ON k.user_id=u.id WHERE u.id=?`, f.userID).Scan(&banned, &keyRevoked); err != nil || banned != 1 || keyRevoked != 1 {
		t.Fatal("guard did not revoke authority", banned, keyRevoked, err)
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
