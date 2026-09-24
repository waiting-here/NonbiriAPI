package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/riskaudit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

const auditBrowserIP = "198.51.100.73"
const auditBrowserClient = "AuditFixture/1.0"
const auditBrowserJSON = `{"error":{"message":"audit-original-only <img src=x onerror=\"document.documentElement.dataset.auditExecuted='yes'\">","details":{"retry":false,"code":"synthetic_busy"}}}`
const auditBrowserText = `<script>document.documentElement.dataset.auditExecuted='yes'</script>
audit-text-only: model discovery failed
<img src=x onerror="document.documentElement.dataset.auditExecuted='yes'">`

// The opt-in browser fixture uses the real application and canonical schema.
// Historical observations are synthetic; ledger changes use the real ledger.
func TestAuditBrowserFixture(t *testing.T) {
	statePath := os.Getenv("NONBIRI_AUDIT_BROWSER_STATE")
	if statePath == "" {
		t.Skip("browser fixture is opt-in")
	}
	vault, err := secret.New(bytes.Repeat([]byte{0x37}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	f := &imageBrowserFixture{t: t, vault: vault, cfg: auditConfig(),
		path: filepath.Join(t.TempDir(), "audit.db")}
	dbfixture.Materialize(t, f.path)
	f.upstreamState = newImageFixtureUpstream(t)
	f.upstream = httptest.NewServer(f.upstreamState)
	defer f.upstream.Close()
	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.app.Load().handler.ServeHTTP(w, r)
	})
	users, admins := httptest.NewUnstartedServer(proxy), httptest.NewUnstartedServer(proxy)
	_, adminPort, err := net.SplitHostPort(admins.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.UserHost, f.cfg.AdminHost = users.Listener.Addr().String(), "localhost:"+adminPort
	f.cfg.ListenAddr, f.cfg.SiteBaseURL = f.cfg.UserHost, "http://"+f.cfg.UserHost
	f.cfg.DBPath = f.path
	if err := f.open(); err != nil {
		t.Fatal(err)
	}
	users.Start()
	admins.Start()
	admins.URL = "http://" + f.cfg.AdminHost
	defer func() {
		users.Close()
		admins.Close()
		if err := f.closeApplication(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := f.store.DB().Exec("INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','0',0) ON CONFLICT(key) DO UPDATE SET value='0'"); err != nil {
		t.Fatal(err)
	}
	f.initialize()
	now := time.Now().Unix()
	requestIDs := make([]string, len(f.users))
	for i, user := range f.users {
		kind, route := "self", "openai_chat_completions"
		if i%2 == 1 {
			kind, route = "charity", "charity_chat_completions"
		}
		requestIDs[i] = seedAuditBrowserRequest(t, f, user.ID, route, kind, now-120-int64(i))
	}
	discovery := seedAuditBrowserRequest(t, f, f.users[0].ID, "model_discovery", "discovery", now-180)
	upstreamerror.CaptureEvent(f.app.Load().audits.observations.ErrorScope(context.Background(),
		observability.DiagnosticRef{RequestID: requestIDs[0], AttemptSeq: 1}), 503, "application/json", []byte(auditBrowserJSON))
	upstreamerror.CaptureEvent(f.app.Load().audits.observations.ErrorScope(context.Background(),
		observability.DiagnosticRef{RequestID: discovery, AttemptSeq: 1}), 502, "text/html", []byte(auditBrowserText))
	var diagnosticID string
	if err := f.store.DB().QueryRow(`SELECT CAST(e.id AS TEXT) FROM request_error_bodies e JOIN request_logs l ON l.id=e.request_log_id WHERE l.logical_request_id=?`, discovery).Scan(&diagnosticID); err != nil {
		t.Fatal(err)
	}
	userID, _ := strconv.ParseInt(f.users[0].ID, 10, 64)
	minutes := make([]riskaudit.Minute, 5)
	start := now/60*60 - 360
	for i := range minutes {
		minutes[i] = riskaudit.Minute{Epoch: "synthetic-browser-epoch", UserID: userID,
			Minute: start + int64(i)*60, Kind: "total", RPMCommitted: 8,
			RPMLimit: 10, ConcurrencyLimit: 2, OccupancyMillis: 96000, Peak: 2,
			ConfigRevision: 1, UpdatedAt: start + int64(i+1)*60}
	}
	if err := f.app.Load().audits.risk.SaveMinutes(context.Background(), minutes, nil); err != nil {
		t.Fatal(err)
	}
	seedAuditBrowserGameBalances(t, f, now)
	// A real failed queued image task exercises its diagnostic root and refund.
	models := f.call("GET", "/api/limited-activities/picture-book/models", nil, f.users[0].Cookie, false)["data"].([]any)
	model := models[0].(map[string]any)
	task := f.call("POST", "/api/limited-activities/picture-book/tasks", map[string]any{
		"model_id": model["id"], "expected_model_revision": model["revision"],
		"prompt": "fail synthetic audit sample", "n": 1,
	}, f.users[0].Cookie, false)["task"].(map[string]any)["id"].(string)
	deadline := time.Now().Add(20 * time.Second)
	for {
		status := f.call("GET", "/api/limited-activities/picture-book/tasks/"+task, nil, f.users[0].Cookie, false)
		if status["status"] == "failed" {
			if status["billing_state"] != "refunded" {
				t.Fatal("failed sample did not refund")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("synthetic image failure did not settle")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for attempt := 0; ; attempt++ {
		if attempt == 10 {
			t.Fatal("projection did not reach ledger head")
		}
		ready, err := f.app.Load().audits.economy.Advance(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
	}
	summaries := make(map[string]any)
	for _, asset := range ledger.Assets() {
		summary := f.call("GET", "/admin/api/economy-audit/summary?asset="+string(asset), nil, f.adminCookie, true)
		if summary["reconciliation"].(map[string]any)["status"] != "matched" {
			t.Fatalf("unreconciled fixture asset %s: %v", asset, summary)
		}
		summaries[string(asset)] = summary
	}
	stop := make(chan struct{})
	var stopped sync.Once
	controlToken := "synthetic-audit-control"
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+controlToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/shutdown" {
			http.NotFound(w, r)
			return
		}
		stopped.Do(func() { close(stop) })
		writeImageFixtureJSON(w, map[string]bool{"ok": true})
	}))
	defer control.Close()
	state := map[string]any{
		"user_url": users.URL, "admin_url": admins.URL, "control_url": control.URL,
		"control_token": controlToken, "users": f.users, "admin_cookie": f.adminCookie,
		"request_ids": requestIDs, "discovery_id": discovery, "diagnostic_id": diagnosticID,
		"image_task_id": task, "summaries": summaries,
		"source_ip": auditBrowserIP, "source_client": auditBrowserClient,
		"json_body": auditBrowserJSON, "text_body": auditBrowserText,
		"private_markers": []string{auditBrowserIP, auditBrowserClient, "audit-original-only", "audit-text-only"},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stop:
	case <-time.After(8 * time.Minute):
		t.Fatal("browser fixture was not shut down")
	}
}

func seedAuditBrowserRequest(t *testing.T, f *imageBrowserFixture, owner, route, kind string, at int64) string {
	t.Helper()
	id, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := db.GenerateOpaqueID("clm_")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,caller_error_code,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at) VALUES(?,?,?,'synthetic/audit-model','terminal',1,'failed',503,'upstream','none','user',zeroblob(16),?,?)`, id, owner, route, at, at+1)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tx.Exec(`INSERT INTO request_logs(logical_request_id,user_id,route_kind,model,started_at,completed_at,caller_result_class,caller_status,caller_error_code,status_code,attempt_count,error_source,error_code,error_diag,duration_ms) VALUES(?,?,?,'synthetic/audit-model',?,?,'failed',503,'upstream',503,1,'upstream','upstream','Synthetic safe failure summary',1000)`, id, owner, route, at, at+1)
	if err != nil {
		t.Fatal(err)
	}
	logID, _ := result.LastInsertId()
	_, err = tx.Exec(`INSERT INTO request_attempts(claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,result_kind,upstream_status,diag,started_at,completed_at) VALUES(?,?,1,'openai-compatible','https://synthetic.invalid/v1','synthetic/audit-model','response',503,'Synthetic safe failure summary',?,?)`, claim, logID, at, at+1)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "http://"+f.cfg.UserHost+"/v1/chat/completions", nil)
	request.RemoteAddr = auditBrowserIP + ":32000"
	request.Header.Set("User-Agent", auditBrowserClient)
	request.Header.Set("Origin", "https://audit-source.invalid")
	request.Header.Set("Referer", "https://audit-source.invalid/private-path?discard=this")
	request.Header.Set("X-Stainless-Lang", "go")
	ctx := observability.WithSource(context.Background(), observability.CaptureSource(request))
	userID, _ := strconv.ParseInt(owner, 10, 64)
	if err := f.app.Load().audits.observations.RecordSourceTx(ctx, tx, id, userID, kind, at); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedAuditBrowserGameBalances(t *testing.T, f *imageBrowserFixture, at int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var admin int64
	if err := tx.QueryRow("SELECT id FROM users WHERE is_admin=1").Scan(&admin); err != nil {
		t.Fatal(err)
	}
	external, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.Game)
	if err != nil {
		t.Fatal(err)
	}
	for i, amount := range []int64{5000, -1000} {
		user, _ := strconv.ParseInt(f.users[i].ID, 10, 64)
		wallet, err := ledger.UserAssetAccount(ctx, tx, user, ledger.Game)
		if err != nil {
			t.Fatal(err)
		}
		op, err := db.GenerateOpaqueID("op_")
		if err != nil {
			t.Fatal(err)
		}
		plan, err := ledger.NewAdminGameAdjustment(ledger.Meta{OperationID: op, ActorUserID: admin, CreatedAt: at},
			wallet.ID, external.ID, ledger.AmountFromMilli(amount), fmt.Sprintf("Synthetic game balance %d", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Apply(ctx, tx, plan); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
