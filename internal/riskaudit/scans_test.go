package riskaudit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func scanRuleFixture(t *testing.T, f *auditFixture) Rule {
	t.Helper()
	rule, err := f.repository.PutRule(context.Background(), Actor{Admin: true, UserID: f.admin}, Rule{Name: "Example client", Status: "suspected", Enabled: true, Conditions: []Condition{{Field: "user_agent", Operator: "prefix", Value: "Example/"}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	return rule
}
func scanInput(token string) ScanInput {
	return ScanInput{RequestToken: token, LookbackHours: 24, Kind: "total"}
}
func finishScan(t *testing.T, r *Repository) {
	t.Helper()
	for range 20 {
		progressed, err := r.ProcessScanBatch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !progressed {
			return
		}
	}
	t.Fatal("scan failed to finish")
}

func TestClientScanFrozenWindowRulesResumeAndResultPages(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	subject := f.user(1)
	rule := scanRuleFixture(t, f)
	wanted := make([]int64, 0)
	for i := range 237 {
		ua := "Other/1"
		if i%2 == 0 {
			ua = "Example/1"
		}
		kind := "self"
		if i%3 == 0 {
			kind = "charity"
		}
		id := f.source(subject, kind, "192.0.2.1", "direct_peer", ua, "model", "success", 0)
		if i%2 == 0 {
			wanted = append(wanted, id)
		}
	}
	input := scanInput("create_scan_example_1")
	scan, err := f.repository.CreateScan(ctx, actor, input)
	if err != nil || scan.Candidates != 237 || scan.State != "queued" || scan.RuleCount != 1 {
		t.Fatalf("create %+v %v", scan, err)
	}
	again, err := f.repository.CreateScan(ctx, actor, input)
	if err != nil || again.ID != scan.ID {
		t.Fatalf("retry %+v %v", again, err)
	}
	changed := input
	changed.Kind = "self"
	if _, err = f.repository.CreateScan(ctx, actor, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("token changed intent: %v", err)
	}
	if _, err = f.repository.CreateScan(ctx, actor, scanInput("create_scan_example_2")); !errors.Is(err, ErrConflict) {
		t.Fatal("second unfinished scan admitted", err)
	}
	if _, err = f.repository.ProcessScanBatch(ctx); err != nil {
		t.Fatal(err)
	}
	partial, err := f.repository.ScanResults(ctx, actor, scan.ID, 1, 20)
	if err != nil || partial.Scan.Scanned != 100 || partial.TotalItems != "50" || len(partial.Items) != 20 || partial.TotalPages != "3" {
		t.Fatalf("partial %+v %v", partial, err)
	}
	// A later log, a changed rule and a newly enabled rule cannot alter this task.
	f.source(subject, "self", "192.0.2.1", "direct_peer", "Example/2", "model", "success", 0)
	rule.Enabled = false
	if _, err = f.repository.PutRule(ctx, actor, rule, false); err != nil {
		t.Fatal(err)
	}
	if _, err = f.repository.PutRule(ctx, actor, Rule{Name: "Other", Status: "confirmed", Enabled: true, Conditions: []Condition{{Field: "user_agent", Operator: "prefix", Value: "Other/"}}}, true); err != nil {
		t.Fatal(err)
	}
	// Reconstructing the repository simulates a fresh worker with no RAM cursor.
	restarted, err := NewRepository(f.store.DB(), RepositoryOptions{Now: func() time.Time { return time.Unix(f.now, 0) }, FinalAuth: f.authority})
	if err != nil {
		t.Fatal(err)
	}
	finishScan(t, restarted)
	seen := make(map[int64]bool)
	for page := int64(1); page <= 6; page++ {
		result, err := restarted.ScanResults(ctx, actor, scan.ID, page, 20)
		if err != nil || result.TotalItems != "119" || result.TotalPages != "6" || result.Scan.State != "completed" || result.Scan.Scanned != 237 {
			t.Fatalf("page %+v %v", result, err)
		}
		for _, row := range result.Items {
			if seen[row.LogID] || len(row.Matches) != 1 || row.Matches[0].Revision != 1 || row.Matches[0].Name != "Example client" {
				t.Fatal("unstable frozen result", row)
			}
			seen[row.LogID] = true
		}
	}
	for _, id := range wanted {
		if !seen[id] {
			t.Fatal("missing matching logical request", id)
		}
	}
	last, err := restarted.ScanResults(ctx, actor, scan.ID, 999, 100)
	if err != nil || last.Page != "2" || len(last.Items) != 19 {
		t.Fatalf("clamp %+v %v", last, err)
	}
	for _, size := range []int{10, 20, 50, 100} {
		p, e := restarted.ScanResults(ctx, actor, scan.ID, 1, size)
		if e != nil || len(p.Items) != size {
			t.Fatal("size", size, e)
		}
	}
}

func TestClientScanCancellationPermissionsDeletionAndRetention(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	admin := Actor{Admin: true, UserID: f.admin}
	steward := Actor{UserID: f.user(6)}
	other := Actor{UserID: f.user(6)}
	subject := f.user(1)
	scanRuleFixture(t, f)
	for range 105 {
		f.source(subject, "charity", "192.0.2.3", "direct_peer", "Example/1", "model", "failed", 0)
	}
	for _, actor := range []Actor{{UserID: subject}, {UserID: f.user(5)}, {Admin: true, UserID: subject}} {
		if _, err := f.repository.CreateScan(ctx, actor, scanInput("permission_example")); !errors.Is(err, ErrForbidden) {
			t.Fatal("unauthorized create", err)
		}
	}
	scan, err := f.repository.CreateScan(ctx, steward, scanInput("steward_scan_example"))
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []Actor{admin, other} {
		if _, err = f.repository.GetScan(ctx, actor, scan.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-owner status", err)
		}
		if _, err = f.repository.ScanResults(ctx, actor, scan.ID, 1, 20); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-owner results", err)
		}
		if _, err = f.repository.CancelScan(ctx, actor, scan.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-owner cancel", err)
		}
	}
	if _, err = f.repository.ProcessScanBatch(ctx); err != nil {
		t.Fatal(err)
	}
	cancelled, err := f.repository.CancelScan(ctx, steward, scan.ID)
	if err != nil || cancelled.State != "cancelled" || cancelled.Scanned != 100 {
		t.Fatal(cancelled, err)
	}
	if progress, err := f.repository.ProcessScanBatch(ctx); err != nil || progress {
		t.Fatal("cancelled scan advanced", err)
	}
	if again, err := f.repository.CancelScan(ctx, steward, scan.ID); err != nil || again.Scanned != 100 {
		t.Fatal("cancel not idempotent", again, err)
	}
	page, err := f.repository.ScanResults(ctx, steward, scan.ID, 1, 100)
	if err != nil || len(page.Items) != 100 {
		t.Fatal("cancel discarded results", err)
	}
	f.exec(`UPDATE request_source_facts SET user_id=NULL WHERE request_log_id=?`, page.Items[0].LogID)
	page, err = f.repository.ScanResults(ctx, steward, scan.ID, 1, 100)
	if err != nil || page.TotalItems != "99" {
		t.Fatal("retired association remains", err, page.TotalItems)
	}
	second, err := f.repository.CreateScan(ctx, steward, scanInput("steward_second_scan"))
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE users SET level=5 WHERE id=?`, steward.UserID)
	if _, err = f.repository.GetScan(ctx, steward, scan.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked scan readable", err)
	}
	var state, reason string
	if err = f.store.DB().QueryRow(`SELECT state,reason FROM risk_client_scans WHERE id=?`, second.ID).Scan(&state, &reason); err != nil || state != "cancelled" || reason != "permission_changed" {
		t.Fatal("revoked queued task", state, reason, err)
	}
	// Cancellation preserves ordinary results until the one-day deadline.
	f.now += int64(ScanLifetime / time.Second)
	for range 3 {
		result, err := f.repository.CleanupScans(ctx)
		if err != nil || result.Processed > 100 {
			t.Fatal("unbounded cleanup", result, err)
		}
	}
	var remaining int
	if err = f.store.DB().QueryRow(`SELECT count(*) FROM risk_client_scans`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("expired scans retained", remaining, err)
	}
}

func TestClientScanCancelledContextQueueBoundsAndConcurrentCreation(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	scanRuleFixture(t, f)
	f.source(f.user(1), "self", "192.0.2.4", "direct_peer", "Example/1", "model", "success", 0)
	admin := Actor{Admin: true, UserID: f.admin}
	var scans [2]ClientScan
	var errs [2]error
	var group sync.WaitGroup
	for i := range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			scans[i], errs[i] = f.repository.CreateScan(ctx, admin, scanInput("concurrent_creation"))
		}()
	}
	group.Wait()
	if errs[0] != nil || errs[1] != nil || scans[0].ID != scans[1].ID {
		t.Fatal("creation replay race", errs, scans)
	}
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.repository.ProcessScanBatch(stopped); err == nil {
		t.Fatal("cancelled batch committed")
	}
	scan, err := f.repository.GetScan(ctx, admin, scans[0].ID)
	if err != nil || scan.Scanned != 0 {
		t.Fatal("checkpoint moved", scan, err)
	}
	for i := 1; i < MaxQueuedScans; i++ {
		_, err = f.repository.CreateScan(ctx, Actor{UserID: f.user(6)}, scanInput(fmt.Sprintf("queue_example_%08d", i)))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.repository.CreateScan(ctx, Actor{UserID: f.user(6)}, scanInput("queue_example_overflow")); !errors.Is(err, ErrConflict) {
		t.Fatal("queue unbounded", err)
	}
}

func TestClientScanHTTPValidationAndStoredResultLimit(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	scanRuleFixture(t, f)
	f.source(f.user(1), "self", "192.0.2.5", "direct_peer", "Example/1", "model", "success", 0)
	scan, err := f.repository.CreateScan(ctx, actor, scanInput("limit_example_scan"))
	if err != nil {
		t.Fatal(err)
	}
	// Set the committed ordinal at the capacity boundary without allocating a
	// hundred thousand unrelated fixture logs. The next match must be the last.
	f.exec(`UPDATE risk_client_scans SET matched=? WHERE id=?`, MaxScanResults-1, scan.ID)
	if _, err = f.repository.ProcessScanBatch(ctx); err != nil {
		t.Fatal(err)
	}
	scan, err = f.repository.GetScan(ctx, actor, scan.ID)
	if err != nil || scan.State != "limited" || scan.Reason != "result_limit" || scan.Matched != MaxScanResults {
		t.Fatal(scan, err)
	}
	for _, query := range []string{"page=0", "page=01", "page=9223372036854775808", "page_size=30", "page=1&page=2", "unexpected=1"} {
		req := httptest.NewRequest("GET", "/client-scans/"+scan.ID+"/results?"+query, nil)
		req.SetPathValue("id", scan.ID)
		recorder := httptest.NewRecorder()
		serveScans(f.repository, "scan_results", actor, recorder, req)
		if recorder.Code != 400 {
			t.Fatal(query, recorder.Code, recorder.Body.String())
		}
	}
	for _, body := range []string{`{"request_token":"short"}`, `{"request_token":"valid_token_example","lookback_hours":721}`, `{"request_token":"valid_token_example","unknown":1}`} {
		recorder := httptest.NewRecorder()
		serveScans(f.repository, "scan_create", actor, recorder, httptest.NewRequest("POST", "/client-scans", strings.NewReader(body)))
		if recorder.Code != 400 {
			t.Fatal(body, recorder.Code)
		}
	}
	raw, err := json.Marshal(scan)
	if err != nil || strings.Contains(string(raw), "rules_json") || strings.Contains(string(raw), "request_token") || strings.Contains(string(raw), "owner") {
		t.Fatal("private task metadata escaped", string(raw), err)
	}
}

func TestClientScanSparseFiltersAdvanceAcrossSameAndLaterSeconds(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	subject := f.user(1)
	scanRuleFixture(t, f)
	wanted := int64(0)
	for i := range 251 {
		kind, model := "self", "different"
		if i == 239 {
			kind, model = "charity", "selected"
		}
		id := f.source(subject, kind, "192.0.2.9", "direct_peer", "Example/1", model, "success", 0)
		if i >= 150 {
			f.exec(`UPDATE request_source_facts SET occurred_at=occurred_at+1 WHERE request_log_id=?`, id)
		}
		if i == 239 {
			wanted = id
		}
	}
	input := scanInput("sparse_filter_example")
	input.Kind, input.Model = "charity", "selected"
	scan, err := f.repository.CreateScan(ctx, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	for batch, scanned := range []int64{100, 200, 251} {
		if _, err = f.repository.ProcessScanBatch(ctx); err != nil {
			t.Fatal(err)
		}
		result, e := f.repository.GetScan(ctx, actor, scan.ID)
		if e != nil || result.Scanned != scanned {
			t.Fatalf("batch %d: %+v %v", batch, result, e)
		}
	}
	page, err := f.repository.ScanResults(ctx, actor, scan.ID, 1, 20)
	if err != nil || page.Scan.State != "completed" || page.TotalItems != "1" || len(page.Items) != 1 || page.Items[0].LogID != wanted {
		t.Fatalf("sparse result %+v %v", page, err)
	}
}

func TestClientScanCompletedPermissionsAndExpiredReplay(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{UserID: f.user(6)}
	input := scanInput("completed_permission_test")
	scan, err := f.repository.CreateScan(ctx, actor, input)
	if err != nil || scan.State != "completed" {
		t.Fatal(scan, err)
	}
	// An expired temporary ban flag is not an active ban, even on later updates.
	f.exec(`UPDATE users SET is_banned=1,banned_until=? WHERE id=?`, time.Now().Unix()-60, actor.UserID)
	var reason string
	if err = f.store.DB().QueryRow(`SELECT reason FROM risk_client_scans WHERE id=?`, scan.ID).Scan(&reason); err != nil || reason != "" {
		t.Fatal(reason, err)
	}
	f.exec(`UPDATE users SET is_banned=0,level=5 WHERE id=?`, actor.UserID)
	f.exec(`UPDATE users SET level=6 WHERE id=?`, actor.UserID)
	if _, err = f.repository.CreateScan(ctx, actor, input); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked completed scan replayed", err)
	}
	if _, err = f.repository.GetScan(ctx, actor, scan.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked completed scan readable", err)
	}
	other := Actor{Admin: true, UserID: f.admin}
	expiredInput := scanInput("expired_creation_test")
	if _, err = f.repository.CreateScan(ctx, other, expiredInput); err != nil {
		t.Fatal(err)
	}
	f.now += int64(ScanLifetime / time.Second)
	// Refresh the synthetic session while the expired task awaits bounded cleanup.
	f.exec(`UPDATE sessions SET expires_at=?,absolute_expires_at=?,last_seen_at=?`, f.now+600, f.now+1200, f.now)
	if _, err = f.repository.CreateScan(ctx, other, expiredInput); !errors.Is(err, ErrConflict) {
		t.Fatal("expired replay must not hit a storage constraint", err)
	}
}
