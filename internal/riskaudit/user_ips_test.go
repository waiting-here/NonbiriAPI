package riskaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserIPsClosedWindowPrefixAndBatchResume(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	user := f.user(1)
	anchor := f.now - 60
	from := anchor - 10
	seed := func(ip string, at int64) {
		id := f.source(user, "self", ip, "direct_peer", "Example/1", "model", "success", 0)
		f.exec(`UPDATE request_source_facts SET occurred_at=? WHERE request_log_id=?`, at, id)
	}
	seed("192.0.2.1", anchor-86400)
	// A duplicate-heavy prefix crosses the 500-row boundary. It must neither
	// reset the oldest IP nor inflate the distinct count.
	for i := range 501 {
		seed("192.0.2.2", anchor-500+int64(i)/2)
	}
	seed("::ffff:192.0.2.2", anchor-1)
	seed("192.0.2.3", anchor)
	scan, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "window_prefix_batch_01", From: from, To: anchor + 1, ScanKind: "user_ips"})
	if err != nil {
		t.Fatal(err)
	}
	if progress, err := f.repository.ProcessScanBatch(ctx); err != nil || !progress {
		t.Fatal(progress, err)
	}
	var count int
	if err = f.store.DB().QueryRow(`SELECT count(*) FROM risk_scan_window_sources WHERE scan_id=?`, scan.ID).Scan(&count); err != nil || count != 500 {
		t.Fatal(count, err)
	}
	// Construct a new repository to prove SQL checkpoint/window restoration.
	restarted, err := NewRepository(f.store.DB(), RepositoryOptions{Now: f.repository.now, FinalAuth: f.authority})
	if err != nil {
		t.Fatal(err)
	}
	finishAggregateScan(t, restarted)
	result, err := restarted.TaskResults(ctx, actor, scan.ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	items := result.Items.([]UserIPs)
	if len(items) != 1 || items[0].Peak != 3 || items[0].WindowFrom != anchor-86400 || items[0].WindowTo != anchor || len(items[0].IPs) != 3 {
		t.Fatalf("window %+v", items)
	}
	if err = f.store.DB().QueryRow(`SELECT count(*) FROM risk_scan_window_sources WHERE scan_id=?`, scan.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	// Moving the anchor one second excludes the oldest exact-boundary IP.
	f.exec(`UPDATE request_source_facts SET occurred_at=occurred_at+1 WHERE effective_ip='192.0.2.3'`)
	scan, err = f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "window_outside_boundary", From: from, To: anchor + 2, ScanKind: "user_ips"})
	if err != nil {
		t.Fatal(err)
	}
	finishAggregateScan(t, f.repository)
	result, err = f.repository.TaskResults(ctx, actor, scan.ID, 1, 20)
	if err != nil || result.TotalItems != "0" {
		t.Fatal(result, err)
	}
}

func TestSharedIPsDeduplicateDiscordAndRetainDeletedAccounts(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	first, second, other := f.user(1), f.user(1), f.user(1)
	var discord string
	if err := f.store.DB().QueryRow(`SELECT discord_id FROM users WHERE id=?`, first).Scan(&discord); err != nil {
		t.Fatal(err)
	}
	f.source(first, "self", "192.0.2.40", "trusted_forwarded", "Example/1", "model", "failed", 0)
	f.exec(`UPDATE logical_requests SET user_id=NULL WHERE user_id=?`, first)
	f.exec(`DELETE FROM users WHERE id=?`, first)
	f.exec(`UPDATE users SET discord_id=? WHERE id=?`, discord, second)
	f.source(second, "charity", "192.0.2.40", "direct_peer", "Example/1", "model", "success", 0)
	f.source(other, "self", "192.0.2.40", "direct_peer", "Example/1", "model", "success", 0)
	config := DefaultConfig()
	config.SharedIPUsers = 2
	if _, err := f.repository.UpdateConfig(ctx, actor, config); err != nil {
		t.Fatal(err)
	}
	scan, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "discord_shared_identity", ScanKind: "shared_ips", LookbackHours: 24})
	if err != nil {
		t.Fatal(err)
	}
	finishAggregateScan(t, f.repository)
	result, err := f.repository.TaskResults(ctx, actor, scan.ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	items := result.Items.([]SharedIP)
	if len(items) != 1 || items[0].Users != 2 || items[0].Requests != 3 || len(items[0].Associations) != 3 {
		t.Fatalf("shared %+v", items)
	}
	page, err := f.repository.SharedIPs(ctx, actor, Window{}, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].Users != 2 {
		t.Fatal(page, err)
	}
}

func TestUserIPsWatermarkExcludesLateOldRootAndUnknownSources(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	user := f.user(1)
	late := f.source(user, "self", "192.0.2.3", "direct_peer", "Example/1", "model", "success", 0)
	f.exec(`DELETE FROM request_source_facts WHERE request_log_id=?`, late)
	for i := range 2 {
		f.source(user, "self", fmt.Sprintf("192.0.2.%d", i+1), "direct_peer", "Example/1", "model", "success", 0)
	}
	f.source(user, "self", "192.0.2.99", "peer_fallback", "Example/1", "model", "failed", 0)
	scan, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "source_insert_watermark", ScanKind: "user_ips", LookbackHours: 24})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO request_source_facts(request_log_id,user_id,kind,effective_ip,ip_quality,source_json,occurred_at) VALUES(?,?,'self','192.0.2.3','direct_peer','{}',?)`, late, user, f.now-300)
	finishAggregateScan(t, f.repository)
	result, err := f.repository.TaskResults(ctx, actor, scan.ID, 1, 20)
	if err != nil || result.TotalItems != "0" || result.Coverage != "partial" {
		t.Fatal(result, err)
	}
}

func TestUserIPsCandidateLimitReportsBoundaryAndDiscardsPartialGroup(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	user := f.user(1)
	root := f.source(user, "self", "192.0.2.60", "direct_peer", "Example/1", "model", "success", 0)
	var at, source int64
	if err := f.store.DB().QueryRow(`SELECT occurred_at,source_id FROM request_source_facts WHERE request_log_id=?`, root).Scan(&at, &source); err != nil {
		t.Fatal(err)
	}
	scan, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "candidate_budget_boundary", ScanKind: "user_ips", LookbackHours: 24})
	if err != nil {
		t.Fatal(err)
	}
	// Resume a scan with one candidate left in its total budget.
	f.exec(`UPDATE risk_client_scans SET scanned=? WHERE id=?`, MaxScanCandidates-1, scan.ID)
	if progress, err := f.repository.ProcessScanBatch(ctx); err != nil || !progress {
		t.Fatal(progress, err)
	}
	result, err := f.repository.TaskResults(ctx, actor, scan.ID, 1, 20)
	if err != nil || result.Scan.State != "limited" || result.Scan.Coverage != "partial" || result.Scan.TruncatedReason != "candidate_limit" || result.Scan.LastSourceAt == nil || *result.Scan.LastSourceAt != at || result.Scan.LastSourceID != fmt.Sprint(source) || result.TotalItems != "0" {
		t.Fatal(result, err)
	}
	var windows int
	if err = f.store.DB().QueryRow(`SELECT count(*) FROM risk_scan_window_sources WHERE scan_id=?`, scan.ID).Scan(&windows); err != nil || windows != 0 {
		t.Fatal(windows, err)
	}
	assertNoScanIdentityCheckpoint(t, f, scan.ID)
}

func TestUserIPScanHTTPCreateAndResults(t *testing.T) {
	f := newAuditFixture(t)
	actor := Actor{Admin: true, UserID: f.admin}
	user := f.user(1)
	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		f.source(user, "self", ip, "direct_peer", "Example/1", "model", "success", 0)
	}
	create := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		out := httptest.NewRecorder()
		serveScans(f.repository, "scan_create_v2", actor, out, httptest.NewRequest("POST", "/admin/api/abuse-audit/scans", strings.NewReader(body)))
		return out
	}
	for _, kind := range []string{"", "unknown"} {
		out := create(fmt.Sprintf(`{"kind":%q,"request_token":"http_window_create_01","lookback_hours":24}`, kind))
		if out.Code != 400 {
			t.Fatalf("invalid kind %q: %d %s", kind, out.Code, out.Body.String())
		}
	}
	body := `{"kind":"user_ips","request_token":"http_window_create_01","lookback_hours":24}`
	out := create(body)
	if out.Code != 202 {
		t.Fatalf("create: %d %s", out.Code, out.Body.String())
	}
	var task struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &task); err != nil || task.ID == "" || task.Kind != "user_ips" {
		t.Fatalf("scan projection %+v: %v", task, err)
	}
	replay := create(body)
	var again struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &again); err != nil || replay.Code != 202 || again.ID != task.ID {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body.String())
	}
	finishAggregateScan(t, f.repository)
	req := httptest.NewRequest("GET", "/admin/api/abuse-audit/scans/"+task.ID+"/results?page=1&page_size=20", nil)
	req.SetPathValue("id", task.ID)
	result := httptest.NewRecorder()
	serveScans(f.repository, "scan_results_v2", actor, result, req)
	var response struct {
		TotalItems string    `json:"total_items"`
		Items      []UserIPs `json:"items"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil || result.Code != 200 || response.TotalItems != "1" || len(response.Items) != 1 || response.Items[0].Peak != 3 {
		t.Fatalf("HTTP results: %d %s, %v", result.Code, result.Body.String(), err)
	}
}
