package riskaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func assertNoScanIdentityCheckpoint(t *testing.T, f *auditFixture, id string) {
	t.Helper()
	var raw string
	if err := f.store.DB().QueryRow(`SELECT checkpoint_json FROM risk_client_scans WHERE id=?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pending_user", "after_user", "pending_ip", "after_ip", "source_at", "source_id", "ip_summary"} {
		if _, present := fields[name]; present {
			t.Fatalf("identity field %s retained in scan %s checkpoint", name, id)
		}
	}
	if _, present := fields["config"]; !present {
		t.Fatalf("scan %s lost its frozen configuration", id)
	}
}

func finishAggregateScan(t *testing.T, r *Repository) {
	t.Helper()
	for range 1000 {
		progressed, err := r.ProcessScanBatch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !progressed {
			return
		}
	}
	t.Fatal("aggregate scan did not finish")
}

func TestAggregateScansFilterAfterCandidatesAndResumeAcrossLargeIPGroup(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	users := make([]int64, 0, 121)
	for index := range 121 {
		userID := f.user(1)
		users = append(users, userID)
		f.source(userID, "self", "192.0.2.44", "direct_peer", "Example/1", "model", "success", 0)
		if index == 120 {
			for minute := range 5 {
				at := (f.now-600)/60*60 + int64(minute)*60
				f.exec(`INSERT INTO risk_audit_minutes(epoch,user_id,minute,call_kind,rpm_committed,occupancy_millis,rpm_limit,concurrency_limit,config_revision,updated_at) VALUES('epoch',?,?,'total',9,0,10,10,1,?)`, userID, at, at+60)
			}
		}
	}
	usersScan, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "users_complete_scan_01", LookbackHours: 24, ScanKind: "users", Signal: "rpm"})
	if err != nil || usersScan.ScanKind != "users" {
		t.Fatal(usersScan, err)
	}
	finishAggregateScan(t, f.repository)
	result, err := f.repository.TaskResults(ctx, actor, usersScan.ID, 9, 20)
	if err != nil || result.TotalItems != "1" || result.Page != "1" || result.Scan.State != "completed" {
		t.Fatal(result, err)
	}
	rows := result.Items.([]UserSummary)
	if len(rows) != 1 || rows[0].UserID != users[120] || !rows[0].RPMRisk {
		t.Fatal(rows)
	}
	assertNoScanIdentityCheckpoint(t, f, usersScan.ID)
	// A second task shares the worker and must not publish a partial IP group.
	ipScan, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "shared_ip_group_scan", LookbackHours: 24, ScanKind: "shared_ips"})
	if err != nil {
		t.Fatal(err)
	}
	for range 520 {
		f.source(users[0], "self", "192.0.2.44", "direct_peer", "Example/1", "model", "success", 0)
	}
	// The fixed source watermark excludes the later 520 calls.
	finishAggregateScan(t, f.repository)
	ipResult, err := f.repository.TaskResults(ctx, actor, ipScan.ID, 1, 20)
	if err != nil || ipResult.TotalItems != "1" {
		t.Fatal(ipResult, err)
	}
	ips := ipResult.Items.([]SharedIP)
	if len(ips) != 1 || ips[0].Users != 121 || ips[0].Requests != 121 {
		t.Fatal(ips)
	}
	assertNoScanIdentityCheckpoint(t, f, ipScan.ID)
	large, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "shared_ip_large_group", LookbackHours: 24, ScanKind: "shared_ips"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repository.ProcessScanBatch(ctx); err != nil {
		t.Fatal(err)
	}
	partial, err := f.repository.TaskResults(ctx, actor, large.ID, 1, 20)
	if err != nil || partial.TotalItems != "0" || partial.Scan.Scanned != ScanBatchSize {
		t.Fatal(partial, err)
	}
	var private, linked int
	if err = f.store.DB().QueryRow(`SELECT count(*) FROM risk_scan_results WHERE scan_id=? AND published=0`, large.ID).Scan(&private); err != nil || private != 1 {
		t.Fatal(private, err)
	}
	if err = f.store.DB().QueryRow(`SELECT count(*) FROM risk_scan_result_sources WHERE scan_id=?`, large.ID).Scan(&linked); err != nil || linked != ScanBatchSize {
		t.Fatal(linked, err)
	}
	finishAggregateScan(t, f.repository)
	complete, err := f.repository.TaskResults(ctx, actor, large.ID, 1, 20)
	if err != nil || complete.TotalItems != "1" || complete.Scan.State != "completed" || complete.Items.([]SharedIP)[0].Requests != 641 {
		t.Fatal(complete, err)
	}
	assertNoScanIdentityCheckpoint(t, f, large.ID)
}

func TestAggregatePendingContributorDeletionNeverPublishesStaleIP(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	users := []int64{f.user(1), f.user(1), f.user(1)}
	for index := range 501 {
		f.source(users[index%len(users)], "self", "192.0.2.88", "direct_peer", "Example/1", "model", "failed", 0)
	}
	scan, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "pending_source_delete", LookbackHours: 24, ScanKind: "shared_ips"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repository.ProcessScanBatch(ctx); err != nil {
		t.Fatal(err)
	}
	var sourceID int64
	if err = f.store.DB().QueryRow(`SELECT request_log_id FROM risk_scan_result_sources WHERE scan_id=? LIMIT 1`, scan.ID).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	f.exec(`DELETE FROM request_source_facts WHERE request_log_id=?`, sourceID)
	if _, err = f.repository.ProcessScanBatch(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := f.repository.TaskResults(ctx, actor, scan.ID, 1, 20)
	if err != nil || result.TotalItems != "0" || !result.Scan.Changed || result.Scan.State != "failed" {
		t.Fatal(result, err)
	}
	assertNoScanIdentityCheckpoint(t, f, scan.ID)
}

func TestUnpublishedAggregateCursorIsClearedAfterSourceOrUserRetirement(t *testing.T) {
	for _, kind := range []string{"users", "shared_ips", "removed_source", "minute_only_user"} {
		t.Run(kind, func(t *testing.T) {
			f := newAuditFixture(t)
			ctx := context.Background()
			actor := Actor{Admin: true, UserID: f.admin}
			user := f.user(1)
			var sourceID int64
			input := ScanInput{RequestToken: "unpublished_cursor_" + kind, LookbackHours: 24, ScanKind: kind}
			if kind == "shared_ips" {
				sourceID = f.source(user, "self", "192.0.2.92", "direct_peer", "Example/1", "model", "success", 0)
			} else if kind == "users" || kind == "removed_source" {
				sourceID = f.source(user, "self", "192.0.2.93", "direct_peer", "Example/1", "model", "success", 0)
				input.ScanKind = "users"
				input.Signal = "rpm" // No published result for a healthy user.
			} else {
				input.ScanKind, input.Signal = "users", "rpm"
				minute := (f.now - 120) / 60 * 60
				f.exec(`INSERT INTO risk_audit_minutes(epoch,user_id,minute,call_kind,rpm_committed,occupancy_millis,rpm_limit,concurrency_limit,config_revision,updated_at) VALUES('epoch',?,?,'total',1,0,10,10,1,?)`, user, minute, minute+60)
			}
			scan, err := f.repository.CreateScan(ctx, actor, input)
			if err != nil {
				t.Fatal(err)
			}
			if progress, err := f.repository.ProcessScanBatch(ctx); err != nil || !progress {
				t.Fatal(progress, err)
			}
			var before string
			if err := f.store.DB().QueryRow(`SELECT checkpoint_json FROM risk_client_scans WHERE id=?`, scan.ID).Scan(&before); err != nil || before == "" {
				t.Fatal(err)
			}
			var checkpoint map[string]any
			if err := json.Unmarshal([]byte(before), &checkpoint); err != nil {
				t.Fatal(err)
			}
			if kind == "shared_ips" && checkpoint["after_ip"] != "192.0.2.92" ||
				kind != "shared_ips" && checkpoint["after_user"] != float64(user) {
				t.Fatalf("expected no-result resume cursor for %s: %s", kind, before)
			}
			var published int
			if err := f.store.DB().QueryRow(`SELECT count(*) FROM risk_scan_results WHERE scan_id=?`, scan.ID).Scan(&published); err != nil || published != 0 {
				t.Fatalf("expected no linked result for %s: %d %v", kind, published, err)
			}
			if kind == "minute_only_user" {
				f.exec(`DELETE FROM users WHERE id=?`, user)
			} else if kind == "removed_source" {
				f.exec(`DELETE FROM request_source_facts WHERE request_log_id=?`, sourceID)
			} else {
				f.exec(`UPDATE request_source_facts SET user_id=NULL WHERE request_log_id=?`, sourceID)
			}
			var state, reason string
			var changed bool
			if kind == "users" || kind == "shared_ips" {
				if err := f.store.DB().QueryRow(`SELECT state,changed FROM risk_client_scans WHERE id=?`, scan.ID).Scan(&state, &changed); err != nil || state != "running" || changed {
					t.Fatalf("active projection clear retired facts: %s %t %v", state, changed, err)
				}
				return
			}
			if err := f.store.DB().QueryRow(`SELECT state,reason,changed FROM risk_client_scans WHERE id=?`, scan.ID).Scan(&state, &reason, &changed); err != nil || state != "failed" || reason != "source_changed" || !changed {
				t.Fatalf("retired unpublished group state=%s reason=%s changed=%t err=%v", state, reason, changed, err)
			}
			assertNoScanIdentityCheckpoint(t, f, scan.ID)
		})
	}
}

func TestCancelledAggregateScanClearsPrivateCheckpoint(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	user := f.user(1)
	for range ScanBatchSize + 1 {
		f.source(user, "self", "192.0.2.94", "direct_peer", "Example/1", "model", "success", 0)
	}
	scan, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "cancel_private_ip_group", LookbackHours: 24, ScanKind: "shared_ips"})
	if err != nil {
		t.Fatal(err)
	}
	if progress, err := f.repository.ProcessScanBatch(ctx); err != nil || !progress {
		t.Fatal(progress, err)
	}
	var before string
	if err = f.store.DB().QueryRow(`SELECT checkpoint_json FROM risk_client_scans WHERE id=?`, scan.ID).Scan(&before); err != nil || !json.Valid([]byte(before)) {
		t.Fatal(err)
	}
	var private map[string]any
	if err = json.Unmarshal([]byte(before), &private); err != nil || private["pending_ip"] != "192.0.2.94" {
		t.Fatalf("expected resumable pending group: %s %v", before, err)
	}
	cancelled, err := f.repository.CancelScan(ctx, actor, scan.ID)
	if err != nil || cancelled.State != "cancelled" {
		t.Fatal(cancelled, err)
	}
	assertNoScanIdentityCheckpoint(t, f, scan.ID)
}

func TestAggregatePendingRetentionAccountsForLinkedSources(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	user := f.user(1)
	for range 501 {
		f.source(user, "self", "192.0.2.89", "direct_peer", "Example/1", "model", "failed", 0)
	}
	scan, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: "pending_retention_01", LookbackHours: 24, ScanKind: "shared_ips"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.repository.ProcessScanBatch(ctx); err != nil {
		t.Fatal(err)
	}
	f.now += int64(ScanLifetime / time.Second)
	processed := 0
	for batch := range 10 {
		result, err := f.repository.CleanupScans(ctx)
		if err != nil || result.Processed > 100 || result.Processed < 1 {
			t.Fatalf("batch %d: %+v %v", batch, result, err)
		}
		processed += result.Processed
		if !result.More {
			break
		}
	}
	var scans, sources int
	if err = f.store.DB().QueryRow(`SELECT count(*) FROM risk_client_scans WHERE id=?`, scan.ID).Scan(&scans); err != nil {
		t.Fatal(err)
	}
	if err = f.store.DB().QueryRow(`SELECT count(*) FROM risk_scan_result_sources WHERE scan_id=?`, scan.ID).Scan(&sources); err != nil || scans != 0 || sources != 0 || processed != 503 {
		t.Fatalf("retention scans=%d sources=%d processed=%d err=%v", scans, sources, processed, err)
	}
}

func TestTaskListIncludesAllRetainedTasksWhileLegacyListStaysBounded(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	actor := Actor{Admin: true, UserID: f.admin}
	for index := range 12 {
		_, err := f.repository.CreateScan(ctx, actor, ScanInput{RequestToken: fmt.Sprintf("retained_task_%02d", index), LookbackHours: 24, ScanKind: "users"})
		if err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := f.repository.RecentTasks(ctx, actor)
	if err != nil || len(tasks) != 12 {
		t.Fatalf("task list length=%d err=%v", len(tasks), err)
	}
	legacy, err := f.repository.RecentScans(ctx, actor)
	if err != nil || len(legacy) != 10 {
		t.Fatalf("legacy list length=%d err=%v", len(legacy), err)
	}
}
