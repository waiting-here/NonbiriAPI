package riskaudit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTargetUserIPsExactAccountWindowAndSingleIP(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	target, other := f.user(1), f.user(1)
	var discord string
	if err := f.store.DB().QueryRow(`SELECT discord_id FROM users WHERE id=?`, other).Scan(&discord); err != nil {
		t.Fatal(err)
	}
	f.source(other, "self", "192.0.2.2", "direct_peer", "Example/1", "model", "success", 0)
	f.source(other, "self", "192.0.2.99", "peer_fallback", "Example/1", "model", "failed", 0)
	f.exec(`UPDATE logical_requests SET user_id=NULL WHERE user_id=?`, other)
	f.exec(`DELETE FROM users WHERE id=?`, other)
	f.exec(`UPDATE users SET discord_id=? WHERE id=?`, discord, target)
	f.source(target, "self", "192.0.2.1", "direct_peer", "Example/1", "model", "success", 1)
	f.source(target, "charity", "192.0.2.1", "trusted_forwarded", "Example/1", "model", "failed", 0)
	prefix := f.source(target, "self", "192.0.2.3", "direct_peer", "Example/1", "model", "success", 0)
	f.exec(`UPDATE request_source_facts SET occurred_at=? WHERE request_log_id=?`, f.now-86401, prefix)
	input := ScanInput{RequestToken: "target_account_window_01", ScanKind: "user_ips", UserID: ScanUserID(target), LookbackHours: 24}
	scan, err := f.repository.CreateScan(ctx, actor, input)
	if err != nil || scan.UserID != target || scan.Candidates != 2 {
		t.Fatal(scan, err)
	}
	replay, err := f.repository.CreateScan(ctx, actor, input)
	if err != nil || replay.ID != scan.ID {
		t.Fatal(replay, err)
	}
	input.UserID = ScanUserID(other)
	if _, err = f.repository.CreateScan(ctx, actor, input); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	finishAggregateScan(t, f.repository)
	result, err := f.repository.TaskResults(ctx, actor, scan.ID, 1, 20)
	if err != nil || result.Coverage != "complete" || result.TotalItems != "1" {
		t.Fatal(result, err)
	}
	items := result.Items.([]SharedIP)
	if len(items) != 1 || items[0].IP != "192.0.2.1" || items[0].Requests != 2 || items[0].Users != 1 || len(items[0].Associations) != 2 {
		t.Fatal(items)
	}
	for _, a := range items[0].Associations {
		if a.UserID != target || a.FirstSeen != f.now-300 || a.LastSeen != f.now-300 {
			t.Fatal(a)
		}
	}
	assertNoScanIdentityCheckpoint(t, f, scan.ID)
}

func TestTargetUserIPsNoDiscordAndMissingSources(t *testing.T) {
	for _, quality := range []string{"missing", "peer_fallback"} {
		t.Run(quality, func(t *testing.T) {
			f := newAuditFixture(t)
			ctx := context.Background()
			actor := Actor{Admin: true, UserID: f.admin}
			target := f.user(1)
			f.exec(`UPDATE users SET discord_id=NULL WHERE id=?`, target)
			f.source(target, "self", "2001:db8::1", "trusted_forwarded", "Example/1", "model", "success", 0)
			storedQuality := quality
			if quality == "missing" {
				storedQuality = "direct_peer"
			}
			root := f.source(target, "self", "192.0.2.3", storedQuality, "Example/1", "model", "success", 0)
			if quality == "missing" {
				f.exec(`DELETE FROM request_source_facts WHERE request_log_id=?`, root)
			}
			scan, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "target_missing_source_01", ScanKind: "user_ips", UserID: ScanUserID(target)})
			if err != nil {
				t.Fatal(err)
			}
			finishAggregateScan(t, f.repository)
			result, err := f.repository.TaskResults(ctx, actor, scan.ID, 1, 20)
			if err != nil || result.Coverage != "partial" || result.Scan.TruncatedReason != "identity_or_ip_unavailable" || result.TotalItems != "1" {
				t.Fatal(result, err)
			}
			items := result.Items.([]SharedIP)
			if items[0].IP != "2001:db8::1" || items[0].Requests != 1 {
				t.Fatal(items)
			}
		})
	}
}

func TestTargetUserIPsPaginationResumeWatermarkAndSourceRetirement(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	target := f.user(1)
	for range 501 {
		f.source(target, "self", "192.0.2.1", "direct_peer", "Example/1", "model", "success", 0)
	}
	for i := 2; i <= 21; i++ {
		f.source(target, "self", fmt.Sprintf("192.0.2.%d", i), "direct_peer", "Example/1", "model", "success", 0)
	}
	scan, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "target_resume_pages_01", ScanKind: "user_ips", UserID: ScanUserID(target)})
	if err != nil {
		t.Fatal(err)
	}
	if progress, err := f.repository.ProcessScanBatch(ctx); err != nil || !progress {
		t.Fatal(progress, err)
	}
	f.source(target, "self", "192.0.2.22", "direct_peer", "Example/1", "model", "success", 0)
	restarted, err := NewRepository(f.store.DB(), RepositoryOptions{Now: f.repository.now, FinalAuth: f.authority})
	if err != nil {
		t.Fatal(err)
	}
	finishAggregateScan(t, restarted)
	first, err := restarted.TaskResults(ctx, actor, scan.ID, 1, 20)
	if err != nil || first.TotalItems != "21" || first.TotalPages != "2" || len(first.Items.([]SharedIP)) != 20 || first.Scan.Scanned != 521 {
		t.Fatal(first, err)
	}
	second, err := restarted.TaskResults(ctx, actor, scan.ID, 99, 20)
	if err != nil || second.Page != "2" || len(second.Items.([]SharedIP)) != 1 {
		t.Fatal(second, err)
	}
	var requests int64
	for _, item := range first.Items.([]SharedIP) {
		requests += item.Requests
	}
	for _, item := range second.Items.([]SharedIP) {
		requests += item.Requests
	}
	if requests != 521 {
		t.Fatal(requests)
	}
	f.exec(`DELETE FROM request_source_facts WHERE effective_ip='192.0.2.1'`)
	changed, err := restarted.TaskResults(ctx, actor, scan.ID, 1, 20)
	if err != nil || changed.Coverage != "changed" || changed.TotalItems != "20" {
		t.Fatal(changed, err)
	}
}

func TestTargetUserIPsPermissionsCancellationAndLimit(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	target, steward, trainee, ordinary := f.user(1), f.user(6), f.user(5), f.user(1)
	input := ScanInput{RequestToken: "target_permissions_01", ScanKind: "user_ips", UserID: ScanUserID(target)}
	for _, id := range []int64{trainee, ordinary} {
		if _, err := f.repository.CreateScan(ctx, Actor{UserID: id}, input); !errors.Is(err, ErrForbidden) {
			t.Fatal(id, err)
		}
	}
	f.source(target, "self", "192.0.2.1", "direct_peer", "Example/1", "model", "success", 0)
	actor := Actor{UserID: steward}
	scan, err := f.repository.CreateScan(ctx, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repository.TaskResults(ctx, Actor{UserID: trainee}, scan.ID, 1, 20); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	stopped, err := f.repository.CancelScan(ctx, actor, scan.ID)
	if err != nil || stopped.State != "cancelled" || stopped.UserID != target {
		t.Fatal(stopped, err)
	}
	finishAggregateScan(t, f.repository)
	input.RequestToken = "target_budget_boundary_01"
	scan, err = f.repository.CreateScan(ctx, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE risk_client_scans SET scanned=? WHERE id=?`, MaxScanCandidates-1, scan.ID)
	if progress, err := f.repository.ProcessScanBatch(ctx); err != nil || !progress {
		t.Fatal(progress, err)
	}
	result, err := f.repository.TaskResults(ctx, actor, scan.ID, 1, 20)
	if err != nil || result.Scan.State != "limited" || result.TotalItems != "0" || result.Scan.TruncatedReason != "candidate_limit" || result.Scan.LastSourceAt == nil || result.Scan.LastSourceID == "" {
		t.Fatal(result, err)
	}
	assertNoScanIdentityCheckpoint(t, f, scan.ID)
	f.exec(`UPDATE users SET level=5 WHERE id=?`, steward)
	if _, err = f.repository.GetScan(ctx, actor, scan.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
}

func TestTargetUserIPHTTPRejectsInvalidTargetWithoutWidening(t *testing.T) {
	f := newAuditFixture(t)
	actor := Actor{Admin: true, UserID: f.admin}
	target := f.user(1)
	f.source(target, "self", "192.0.2.1", "direct_peer", "Example/1", "model", "success", 0)
	create := func(body string) *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		serveScans(f.repository, "scan_create_v2", actor, out, httptest.NewRequest("POST", "/admin/api/abuse-audit/scans", strings.NewReader(body)))
		return out
	}
	for _, value := range []string{`"0"`, `""`, `"01"`, `"-1"`, `"9223372036854775808"`, `null`, `1`} {
		out := create(fmt.Sprintf(`{"kind":"user_ips","request_token":"target_invalid_http_01","user_id":%s}`, value))
		if out.Code != 400 {
			t.Fatal(value, out.Code, out.Body.String())
		}
	}
	out := create(fmt.Sprintf(`{"kind":"users","request_token":"target_wrong_kind_http","user_id":"%d"}`, target))
	if out.Code != 400 {
		t.Fatal(out.Code, out.Body.String())
	}
	out = create(fmt.Sprintf(`{"kind":"user_ips","request_token":"target_success_http_01","user_id":"%d"}`, target))
	var scan struct {
		ID     string `json:"id"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &scan); err != nil || out.Code != 202 || scan.UserID != fmt.Sprint(target) {
		t.Fatal(out.Code, out.Body.String(), err)
	}
	finishAggregateScan(t, f.repository)
	req := httptest.NewRequest("GET", "/admin/api/abuse-audit/scans/"+scan.ID+"/results?page=1&page_size=20", nil)
	req.SetPathValue("id", scan.ID)
	result := httptest.NewRecorder()
	serveScans(f.repository, "scan_results_v2", actor, result, req)
	var page struct {
		Items []SharedIP `json:"items"`
		Total string     `json:"total_items"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &page); err != nil || result.Code != 200 || page.Total != "1" || len(page.Items) != 1 || page.Items[0].Requests != 1 {
		t.Fatal(result.Code, result.Body.String(), err)
	}
}

func TestTargetUserIPsSourceRetiredBeforeWorker(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	target := f.user(1)
	root := f.source(target, "self", "192.0.2.1", "direct_peer", "Example/1", "model", "success", 0)
	scan, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "target_source_retired_01", ScanKind: "user_ips", UserID: ScanUserID(target)})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`DELETE FROM request_source_facts WHERE request_log_id=?`, root)
	finishAggregateScan(t, f.repository)
	result, err := f.repository.TaskResults(ctx, actor, scan.ID, 1, 20)
	if err != nil || result.Coverage != "changed" || !result.Scan.Changed || result.TotalItems != "0" || result.Scan.Scanned != 0 {
		t.Fatal(result, err)
	}
}
