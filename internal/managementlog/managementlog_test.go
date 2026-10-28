package managementlog_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	. "github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func fixture(t *testing.T) *Service {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	for _, ddl := range []string{
		"PRAGMA foreign_keys=ON",
		"CREATE TABLE users(id INTEGER PRIMARY KEY,username TEXT,is_admin INTEGER,is_banned INTEGER,level INTEGER,auto_level INTEGER)",
		"INSERT INTO users VALUES(1,'Administrator',1,0,6,6),(2,'Steward',0,0,5,5)",
		"CREATE TABLE business(value INTEGER)",
		db.ManagementOperationsSQL,
	} {
		if _, err = database.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	return New(database, database)
}
func recordValue(id string, at int64, n int) Record {
	v := Record{Metadata: Metadata{EventID: id, CreatedAt: at, Source: SourceAIManual, ExecutorKind: "ai", Actor: Subject{ID: "1", Name: "Administrator", Role: "admin"}, Authorizer: Subject{ID: "1", Name: "Administrator", Role: "admin"}, Domain: "donations", Action: "approve", AuthorizationRef: "plan/example"}, Result: Result{Outcome: Succeeded}}
	for i := 0; i < n; i++ {
		v.Items = append(v.Items, Item{Action: "approve", TargetKind: "donation", TargetID: fmt.Sprint(i + 1), Outcome: Succeeded, Changes: []Change{{Field: "status", Operation: "updated"}}})
	}
	return v
}
func insert(t *testing.T, s *Service, v Record) int64 {
	t.Helper()
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	id, e := RecordTx(context.Background(), tx, v)
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TestAtomicRetentionReplayAndPermission(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	v := recordValue("one", 7200000, 3)
	tx, _ := s.DB.Begin()
	if _, e := tx.Exec("INSERT INTO business VALUES(1)"); e != nil {
		t.Fatal(e)
	}
	if _, e := RecordTx(ctx, tx, v); e != nil {
		t.Fatal(e)
	}
	tx.Rollback()
	var count int
	s.DB.QueryRow("SELECT COUNT(*) FROM business").Scan(&count)
	if count != 0 {
		t.Fatal(count)
	}
	id := insert(t, s, v)
	if again := insert(t, s, v); id != again {
		t.Fatal(id, again)
	}
	page, e := s.Query(ctx, 1, Query{})
	if e != nil || page.Total != 1 || page.Groups[0].Targets != 3 {
		t.Fatalf("%+v %v", page, e)
	}
	if _, e = s.Query(ctx, 2, Query{}); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("DELETE FROM users WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow("SELECT actor_name FROM management_operations WHERE id=?", id).Scan(&v.Actor.Name); e != nil || v.Actor.Name != "Administrator" {
		t.Fatal(v, e)
	}
}
func TestSnapshotGroupsAndItemSeek(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	first := insert(t, s, recordValue("a", 7200001, 9))
	insert(t, s, recordValue("b", 7200002, 2))
	page, e := s.Query(ctx, 1, Query{SnapshotMaxID: first})
	if e != nil || page.Groups[0].Operations != 1 || page.Groups[0].Targets != 9 {
		t.Fatal(page, e)
	}
	page, e = s.Query(ctx, 1, Query{Filter: Filter{TimeFrom: 7200002, TimeTo: 7200003}})
	if e != nil || len(page.Groups) != 1 || page.Groups[0].Targets != 2 {
		t.Fatal(page, e)
	}
	items, e := s.Items(ctx, 1, first, 3, 3)
	if e != nil || len(items.Items) != 3 || items.Items[0].Ordinal != 7 {
		t.Fatal(items, e)
	}
}
func TestLargeBatchCleanupResumesAndProtectsCutoff(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	first := insert(t, s, recordValue("large", 7200000, 1300))
	insert(t, s, recordValue("small", 7200001, 2))
	prepared, e := s.PrepareCleanup(ctx, 1, CleanupRequest{All: true})
	if e != nil {
		t.Fatal(e)
	}
	tx, _ := s.DB.Begin()
	job, e := s.ApplyCleanupTx(ctx, tx, 1, "cleanup_request", prepared)
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	insert(t, s, recordValue("after", 7200002, 1))
	active, e := s.Step(ctx)
	if e != nil || !active {
		t.Fatal(active, e)
	}
	status, e := s.CleanupStatus(ctx, 1, job.ID)
	if e != nil || status.DeletedItems != 512 || status.DeletedOperations != 0 {
		t.Fatal(status, e)
	}
	if _, e = s.Items(ctx, 1, first, 1, 25); !errors.Is(e, ErrCleaning) {
		t.Fatal(e)
	}
	s = New(s.DB, s.ReadDB)
	for i := 0; i < 10; i++ {
		active, e = s.Step(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if !active {
			break
		}
	}
	status, e = s.CleanupStatus(ctx, 1, job.ID)
	if e != nil || status.State != "completed" || status.DeletedOperations != 2 || status.DeletedItems != 1302 {
		t.Fatal(status, e)
	}
	page, e := s.Query(ctx, 1, Query{View: "operations"})
	if e != nil || page.Total != 3 {
		t.Fatal(page, e)
	}
	var ops, rollups int
	s.DB.QueryRow("SELECT COUNT(*) FROM management_operations").Scan(&ops)
	s.DB.QueryRow("SELECT SUM(operations) FROM management_operation_rollups").Scan(&rollups)
	if ops != rollups {
		t.Fatal(ops, rollups)
	}
}
func TestBoundaryNeverInfersSuccessAndExcludesReads(t *testing.T) {
	s := fixture(t)
	for _, path := range []string{"/admin/api/ai/driver/models", "/admin/api/rewards/preview", "/admin/api/fatfish/levels/validate", "/admin/api/games/duel/export", "/admin/api/donation-keys/failure-streak-reset/selection", "/api/steward/donation-keys/models/refresh/selection"} {
		if _, yes := DescribeRoute("POST", path); yes {
			t.Fatal(path)
		}
	}
	h := s.Wrap("PATCH", "/admin/api/users/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PATCH", "/admin/api/users/2", nil))
	page, e := s.Query(context.Background(), 1, Query{View: "operations"})
	if e != nil || page.Operations[0].Outcome != Unknown || page.Operations[0].Actor.ID != "" {
		t.Fatal(page, e)
	}
}
func TestDurableBoundsAndIndexedTargetReads(t *testing.T) {
	s := fixture(t)
	v := recordValue("bounded", 7200000, 1)
	v.Summary = strings.Repeat("x", 1025)
	tx, _ := s.DB.Begin()
	if _, e := RecordTx(context.Background(), tx, v); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	tx.Rollback()
	insert(t, s, recordValue("valid", 7200000, 1))
	if _, e := s.DB.Exec("UPDATE management_operations SET summary=?", strings.Repeat("x", 1025)); e == nil {
		t.Fatal("durable summary bound missing")
	}
	rows, e := s.DB.Query("EXPLAIN QUERY PLAN SELECT operation_id FROM management_operation_items WHERE target_kind=? AND target_id=?", "donation", "1")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var a, b, c int
		var text string
		rows.Scan(&a, &b, &c, &text)
		plan += text
	}
	if !strings.Contains(plan, "management_operation_items_target") {
		t.Fatal(plan)
	}
}

func TestBusinessOutcomesSurviveCompletionAndRollback(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	h := s.Wrap("POST", "/admin/api/donations/review", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := SetIdentity(r.Context(), s.DB, 1, SourceWeb); err != nil {
			t.Fatal(err)
		}
		ChangedFields(r.Context(), []byte(`{"password":"DO-NOT-RETAIN","token":"ALSO-SECRET","expected_revision":"1"}`))
		SetItemsResult(r.Context(), []Item{{Action: "approve", TargetKind: "donation_use", TargetID: "1", Outcome: Succeeded}, {Action: "approve", TargetKind: "donation_use", TargetID: "2", Outcome: Rejected, Code: "conflict"}})
		tx, e := s.DB.Begin()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec("INSERT INTO business VALUES(7)"); e != nil {
			t.Fatal(e)
		}
		if e = RecordCompletedTx(r.Context(), tx, 200); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		w.WriteHeader(200)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil))
	page, e := s.Query(ctx, 1, Query{View: "operations"})
	if e != nil || page.Total != 1 || page.Operations[0].Outcome != Partial || page.Operations[0].Succeeded != 1 || page.Operations[0].Failed != 1 {
		t.Fatal(page, e)
	}
	h = s.Wrap("PUT", "/admin/api/limited-activities/picture-book/models", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tx, _ := s.DB.Begin()
		tx.Exec("INSERT INTO business VALUES(8)")
		tx.Rollback()
		ChangedFields(r.Context(), []byte(`{"password":"DO-NOT-RETAIN","token":"ALSO-SECRET"}`))
		SetResult(r.Context(), Result{Outcome: Rejected, Code: "validation_failed"})
		w.WriteHeader(200)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/", nil))
	page, e = s.Query(ctx, 1, Query{View: "operations"})
	if e != nil || page.Total != 2 || page.Operations[0].Outcome != Rejected {
		t.Fatal(page, e)
	}
	items, e := s.Items(ctx, 1, page.Operations[0].ID, 1, 25)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(items)
	if strings.Contains(string(encoded), "DO-NOT-RETAIN") || strings.Contains(string(encoded), "ALSO-SECRET") || !strings.Contains(string(encoded), "password") {
		t.Fatal(string(encoded))
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM business").Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	replay := s.Wrap("PUT", "/admin/api/config", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { MarkReplay(r.Context()); w.WriteHeader(200) }))
	replay.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/", nil))
	page, e = s.Query(ctx, 1, Query{View: "operations"})
	if e != nil || page.Total != 2 {
		t.Fatal(page, e)
	}
}

func TestConditionalSubmissionRecordsOnlyInternalRule(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	h := s.Wrap("POST", "/api/donations", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil))
	page, e := s.Query(ctx, 1, Query{View: "operations"})
	if e != nil || page.Total != 0 {
		t.Fatal(page, e)
	}
	h = s.Wrap("POST", "/api/donations", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := recordValue("rule", 7200000, 1)
		v.Source = SourceMainstreamRule
		v.ExecutorKind = "rule"
		TrackInternalAttempt(r.Context(), v)
		w.WriteHeader(409)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil))
	page, e = s.Query(ctx, 1, Query{View: "operations"})
	if e != nil || page.Total != 1 || page.Operations[0].Source != SourceMainstreamRule || page.Operations[0].Outcome != Rejected {
		t.Fatal(page, e)
	}
}

func TestAsyncCompletionAndInterruptedBatchKeepRollupsConsistent(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	v := recordValue("async", 7200000, 0)
	v.Outcome = Accepted
	v.CorrelationKind = "model_discovery"
	v.CorrelationID = "job"
	id := insert(t, s, v)
	tx, _ := s.DB.Begin()
	e := AppendRelatedTx(ctx, tx, "model_discovery", "job", Item{Action: "refresh", TargetKind: "job", TargetID: "job", Outcome: Failed, Code: "interrupted"}, true)
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	detail, e := s.Detail(ctx, 1, id)
	if e != nil || detail.Outcome != Failed || detail.Failed != 1 {
		t.Fatal(detail, e)
	}
	tx, _ = s.DB.Begin()
	e = AppendRelatedTx(ctx, tx, "model_discovery", "job", Item{Outcome: Succeeded}, true)
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	meta := recordValue("batch", 7200001, 0).Metadata
	meta.CorrelationKind = "automation_batch"
	meta.CorrelationID = "batch"
	batch := WithOperation(ctx, meta)
	tx, _ = s.DB.Begin()
	e = AppendContextTx(batch, tx, Item{Action: "bind", TargetKind: "key", TargetID: "2", Outcome: Succeeded}, false)
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = s.RecoverInterrupted(ctx); e != nil {
		t.Fatal(e)
	}
	page, e := s.Query(ctx, 1, Query{View: "operations", Filter: Filter{EventID: "batch"}})
	if e != nil || page.Operations[0].Outcome != Partial || page.Operations[0].Succeeded != 1 {
		t.Fatal(page, e)
	}
	var heads, rollups int
	s.DB.QueryRow("SELECT count(*) FROM management_operations").Scan(&heads)
	s.DB.QueryRow("SELECT sum(operations) FROM management_operation_rollups").Scan(&rollups)
	if heads != rollups {
		t.Fatal(heads, rollups)
	}
}

func TestCleanupSkipsActiveButDeletesUnknownAndContinues(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	active := recordValue("active", 7200000, 0)
	active.Outcome = Accepted
	active.CorrelationKind = "risk_scan"
	active.CorrelationID = "scan"
	activeID := insert(t, s, active)
	unknown := recordValue("unknown", 7200001, 0)
	unknown.Outcome = Unknown
	unknownID := insert(t, s, unknown)
	insert(t, s, recordValue("terminal", 7200002, 1))
	prepared, e := s.PrepareCleanup(ctx, 1, CleanupRequest{All: true})
	if e != nil {
		t.Fatal(e)
	}
	tx, _ := s.DB.Begin()
	job, e := s.ApplyCleanupTx(ctx, tx, 1, "skip-active", prepared)
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	for i := 0; i < 5; i++ {
		more, e := s.Step(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if !more {
			break
		}
	}
	status, e := s.CleanupStatus(ctx, 1, job.ID)
	if e != nil || status.State != "completed" || status.SkippedActive != 1 || status.DeletedOperations != 2 {
		t.Fatal(status, e)
	}
	if _, e = s.Detail(ctx, 1, activeID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Detail(ctx, 1, unknownID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	tx, _ = s.DB.Begin()
	e = AppendRelatedTx(ctx, tx, "risk_scan", "scan", Item{Action: "scan", TargetKind: "risk_scan", TargetID: "scan", Outcome: Succeeded}, true)
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	detail, e := s.Detail(ctx, 1, activeID)
	if e != nil || detail.Outcome != Succeeded {
		t.Fatal(detail, e)
	}
}

func TestOperationTotalsUseSameFilteredSnapshot(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	first := insert(t, s, recordValue("range-one", 7200001, 1))
	insert(t, s, recordValue("range-two", 7200002, 1))
	page, e := s.Query(ctx, 1, Query{View: "operations", SnapshotMaxID: first})
	if e != nil || page.Total != 1 {
		t.Fatal(page, e)
	}
	page, e = s.Query(ctx, 1, Query{View: "operations", Filter: Filter{TimeFrom: 7200002, TimeTo: 7200003}})
	if e != nil || page.Total != 1 {
		t.Fatal(page, e)
	}
	page, e = s.Query(ctx, 1, Query{View: "operations", Filter: Filter{TargetKind: "donation", TargetID: "1"}})
	if e != nil || page.Total != 2 {
		t.Fatal(page, e)
	}
	rows, e := s.DB.Query("EXPLAIN QUERY PLAN SELECT id FROM management_operations WHERE id IN (SELECT operation_id FROM management_operation_items WHERE target_kind=? AND target_id=?) ORDER BY created_at DESC,id DESC LIMIT 25", "donation", "1")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var a, b, c int
		var text string
		if e = rows.Scan(&a, &b, &c, &text); e != nil {
			t.Fatal(e)
		}
		plan += text
	}
	if !strings.Contains(plan, "management_operation_items_target") || !strings.Contains(plan, "INTEGER PRIMARY KEY") {
		t.Fatal(plan)
	}
}

func TestAIRouteClassificationKeepsConversationHistorySeparate(t *testing.T) {
	cases := []struct {
		method, path string
		record       bool
	}{
		{"GET", "/chat", false}, {"POST", "/chat/messages", false}, {"GET", "/chat/messages", false},
		{"PUT", "/chat/messages/{id}", false}, {"POST", "/chat/clear", false}, {"POST", "/chat/jobs/{id}/cancel", false},
		{"POST", "/chat/compress", false}, {"GET", "/chat/plans", false}, {"POST", "/chat/plans/{id}/decision", true},
		{"GET", "/reviews", false}, {"GET", "/reviews/{id}", false},
		{"GET", "/memories/{kind}", false}, {"PUT", "/memories/{kind}", true},
		{"GET", "/personal-memory", false}, {"PUT", "/personal-memory", true},
		{"GET", "/personal-memories", false}, {"GET", "/personal-memories/{owner}", false}, {"PUT", "/personal-memories/{owner}", true},
		{"GET", "/config", false}, {"PUT", "/config", true}, {"GET", "/driver", false}, {"PUT", "/driver", true}, {"POST", "/driver/models", false},
		{"GET", "/drivers", false}, {"POST", "/drivers", true}, {"PUT", "/drivers/{id}", true}, {"DELETE", "/drivers/{id}", true},
		{"PUT", "/driver-bindings", true}, {"POST", "/drivers/{id}/models", false}, {"POST", "/drivers/{driver}/models", false},
		{"GET", "/chatgpt", false}, {"POST", "/chatgpt/target", false}, {"POST", "/chatgpt/import", true}, {"POST", "/chatgpt/logout", true},
		{"GET", "/requests", false}, {"GET", "/requests/{id}", false}, {"POST", "/requests/cleanup", true}, {"GET", "/usage", false},
		{"GET", "/archives", false}, {"GET", "/archives/{owner}/messages", false}, {"GET", "/archives/{owner}/results", false}, {"GET", "/archives/{owner}/plans", false},
	}
	for _, prefix := range []string{"/admin/api/ai", "/api/steward/ai"} {
		for _, test := range cases {
			if _, record := DescribeRoute(test.method, prefix+test.path); record != test.record {
				t.Errorf("%s %s: record=%v want=%v", test.method, prefix+test.path, record, test.record)
			}
		}
	}
	s := fixture(t)
	for _, path := range []string{"/admin/api/ai/chat/messages", "/api/steward/ai/chat/clear", "/admin/api/ai/drivers/{id}/models"} {
		h := s.Wrap("POST", path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := MetadataFromContext(r.Context()); ok {
				t.Error("ordinary AI interaction received management context")
			}
			w.WriteHeader(http.StatusOK)
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil))
	}
	page, e := s.Query(context.Background(), 1, Query{View: "operations"})
	if e != nil || page.Total != 0 {
		t.Fatal(page, e)
	}
}

func TestCleanupUsesHTTPDomainInOperationAndGroupQueries(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	prepared, e := s.PrepareCleanup(ctx, 1, CleanupRequest{All: true})
	if e != nil {
		t.Fatal(e)
	}
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	job, e := s.ApplyCleanupTx(ctx, tx, 1, "domain-cleanup", prepared)
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("UPDATE management_operation_cleanup_jobs SET state='failed' WHERE id=?", job.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.ResumeCleanup(ctx, 1, job.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Step(ctx); e != nil {
		t.Fatal(e)
	}
	for _, view := range []string{"operations", "groups"} {
		page, e := s.Query(ctx, 1, Query{View: view, Filter: Filter{Domain: "management-operations"}})
		if e != nil || page.Total != 3 {
			t.Fatalf("%s: %+v %v", view, page, e)
		}
	}
}
