package observability

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func newObservedDatabase(t *testing.T) (*Repository, int64) {
	t.Helper()
	key := bytes.Repeat([]byte{0x56}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "observability.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(); _ = vault.Close() })
	r, err := NewRepository(store.DB())
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	result, err := r.db.Exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES('70001','test user',zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),1700000000,1700000000)`)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec(`INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at) VALUES(?,3,zeroblob(32),'test','key',1700000000,1700000000)`, userID); err != nil {
		t.Fatal(err)
	}
	return r, userID
}

func observedLog(t *testing.T, r *Repository, userID, at int64) (string, int64) {
	t.Helper()
	id, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec(`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at) VALUES(?,?,'openai_chat_completions','safe-model','terminal',1,'failed',503,'none','user',zeroblob(16),?,?)`, id, userID, at, at); err != nil {
		t.Fatal(err)
	}
	result, err := r.db.Exec(`INSERT INTO request_logs(logical_request_id,user_id,route_kind,started_at,completed_at,caller_result_class,caller_status,status_code) VALUES(?,?,'openai_chat_completions',?,?,'failed',503,503)`, id, userID, at, at)
	if err != nil {
		t.Fatal(err)
	}
	row, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id, row
}

func scalar(t *testing.T, database *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var value int64
	if err := database.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRawBudgetConcurrentWholeBodiesAndCascadeRecovery(t *testing.T) {
	r, userID := newObservedDatabase(t)
	if _, err := r.db.Exec(`UPDATE site_config SET value='1' WHERE key='request_error_body_budget_mib'`); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 8)
	for i := range ids {
		ids[i], _ = observedLog(t, r, userID, r.now().Unix())
	}
	payload := bytes.Repeat([]byte{0xff}, upstreamerror.MaxRawBodyBytes)
	var wait sync.WaitGroup
	for _, id := range ids {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			ctx := r.ErrorScope(context.Background(), DiagnosticRef{RequestID: id, AttemptSeq: 1})
			upstreamerror.CaptureEvent(ctx, 503, "application/json", payload)
		}(id)
	}
	wait.Wait()
	if got := scalar(t, r.db, `SELECT count(*) FROM request_error_bodies`); got != 8 {
		t.Fatalf("metadata=%d", got)
	}
	if got := scalar(t, r.db, `SELECT count(*) FROM request_error_bodies WHERE save_state='saved'`); got != 1 {
		t.Fatalf("saved=%d", got)
	}
	if got := scalar(t, r.db, `SELECT raw_body_bytes FROM observability_state`); got != 1<<20 {
		t.Fatalf("budget=%d", got)
	}
	var logID int64
	if err := r.db.QueryRow(`SELECT request_log_id FROM request_error_bodies WHERE save_state='saved'`).Scan(&logID); err != nil {
		t.Fatal(err)
	}
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	body, err := ErrorBodyTx(context.Background(), tx, logID, 1, 1)
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(body.Body)
	if err != nil || body.Encoding != "base64" || !bytes.Equal(decoded, payload) {
		t.Fatal("binary diagnostic changed")
	}
	if _, err = r.db.Exec(`DELETE FROM request_logs WHERE id=?`, logID); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, r.db, `SELECT raw_body_bytes FROM observability_state`); got != 0 {
		t.Fatalf("cascade left bytes=%d", got)
	}
	if err = r.ReconcileCounters(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSourceAtomicClassificationAndOutcomeWindow(t *testing.T) {
	r, userID := newObservedDatabase(t)
	now := r.now().Unix()
	id, rowID := observedLog(t, r, userID, now-1)
	ctx := WithSource(context.Background(), Source{EffectiveIP: "192.0.2.1", IPQuality: "direct_peer", UserAgent: "test-agent"})
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = r.RecordSourceTx(ctx, tx, id, userID, "unclassified", now-1); err != nil {
		t.Fatal(err)
	}
	if err = r.RecordSourceTx(ctx, tx, id, userID, "charity", now-1); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err = r.db.QueryRow(`SELECT kind FROM request_source_facts WHERE request_log_id=?`, rowID).Scan(&kind); err != nil || kind != "charity" {
		t.Fatalf("kind=%s err=%v", kind, err)
	}
	model, err := r.db.Exec(`INSERT INTO charity_models(provider,model,full_name,pricing_mode,created_at,updated_at) VALUES('test','model','[公益]test/model','per_request',?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	modelID, _ := model.LastInsertId()
	for _, sample := range []struct {
		at         int64
		result     string
		dispatched bool
	}{{now - 86400, "success", true}, {now - 1, "failure", true}, {now - 1, "cancelled", true}, {now - 1, "failure", false}, {now, "success", true}, {now - 86401, "success", true}} {
		request, _ := observedLog(t, r, userID, sample.at)
		tx, err := r.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err = r.RecordOutcomeTx(ctx, tx, request, modelID, sample.result, sample.dispatched, sample.at); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	rate, err := r.RecentSuccess(ctx, modelID, now)
	if err != nil {
		t.Fatal(err)
	}
	if rate.Success != 1 || rate.Failure != 1 || rate.Cancelled != 1 || rate.SampleCount != 2 || rate.Rate == nil || *rate.Rate != 0.5 || !rate.InsufficientSample {
		t.Fatalf("wrong rate %+v", rate)
	}
	if _, err = r.db.Exec(`DELETE FROM request_logs WHERE id=?`, rowID); err != nil {
		t.Fatal(err)
	}
	if scalar(t, r.db, `SELECT count(*) FROM request_source_facts WHERE request_log_id=?`, rowID) != 0 {
		t.Fatal("source survived root deletion")
	}
}

func TestAccessPersistenceRevalidatesRotationAndAnonymousHasNoSource(t *testing.T) {
	r, userID := newObservedDatabase(t)
	ctx := context.Background()
	now := r.now().Unix()
	event := accessEvent{identity: AccessIdentity{UserID: userID, Generation: 3}, source: Source{EffectiveIP: "192.0.2.1", IPQuality: "direct_peer", UserAgent: "private-client"}, pathKind: "models", method: "GET", status: 200, responseKind: "api_json", at: now - 1}
	if err := r.recordAccess(ctx, event); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE caller_keys SET generation=4,key_hash=? WHERE user_id=?`, bytes.Repeat([]byte{1}, 32), userID); err != nil {
		t.Fatal(err)
	}
	if scalar(t, r.db, `SELECT count(*) FROM audit_access_events WHERE caller_key_user_id IS NOT NULL`) != 0 {
		t.Fatal("rotation retained key reference")
	}
	if err := r.recordAccess(ctx, event); err != nil {
		t.Fatal(err)
	}
	if scalar(t, r.db, `SELECT count(*) FROM audit_access_events`) != 1 || scalar(t, r.db, `SELECT SUM(count) FROM anonymous_access_minutes`) != 1 {
		t.Fatal("stale identity was persisted")
	}
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	summary, err := SummarizeAccessTx(ctx, tx, AccessFilter{From: now - 86400, To: now, Limit: 100})
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if summary.AuthenticatedEvents != 1 || summary.AnonymousEvents != 1 || summary.ModelGenerationRatio != nil {
		t.Fatalf("wrong summary %+v", summary)
	}
	if _, err = r.db.Exec(`DELETE FROM users WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if scalar(t, r.db, `SELECT access_rows FROM observability_state`) != 0 {
		t.Fatal("account deletion did not cascade counter")
	}
}

func TestAccessObserverPreservesResponseAndReusesIdentity(t *testing.T) {
	r, userID := newObservedDatabase(t)
	var lookups atomic.Int32
	o, err := NewAccessObserver(r, func(context.Context, string) (AccessIdentity, error) {
		lookups.Add(1)
		panic("read-only resolver unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpmw.New(httpmw.Config{UserHost: "example.invalid", AdminHost: "admin.example.invalid", SiteBaseURL: "https://example.invalid"}, o.Wrap(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/models" {
			MarkAccessIdentity(request.Context(), userID, 3)
			MarkResponseCategory(request.Context(), "api_json")
		} else {
			MarkResponseCategory(request.Context(), "spa_fallback")
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("unchanged response"))
	})))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/models?ignored=secret", "/dashboard/billing/usage", "/assets/not-observed"} {
		request := httptest.NewRequest("GET", "https://example.invalid"+path, nil)
		request.Header.Set("Authorization", "Bearer fictional")
		request.Header.Set("User-Agent", "private-client")
		writer := httptest.NewRecorder()
		handler.ServeHTTP(writer, request)
		if writer.Code != 200 || writer.Body.String() != "unchanged response" {
			t.Fatal("observer changed response")
		}
	}
	if err = o.Close(); err != nil {
		t.Fatal(err)
	}
	if lookups.Load() != 1 || scalar(t, r.db, `SELECT count(*) FROM audit_access_events`) != 1 || scalar(t, r.db, `SELECT SUM(count) FROM anonymous_access_minutes`) != 1 {
		t.Fatal("observer identity/allowlist mismatch")
	}
	var source string
	if err = r.db.QueryRow(`SELECT source_json FROM audit_access_events`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(source), &decoded) != nil || decoded["user_agent"] != "private-client" {
		t.Fatal("source lost")
	}
}

func TestRetentionUsesCanonicalKeysAndLeavesRequestRootsToTheirAuthority(t *testing.T) {
	fixture := newDiagnosticFixture(t)
	r, userID := fixture.repository, fixture.owner
	now := r.now().Unix()
	old := now - RetentionSeconds - 60
	r.now = func() time.Time { return time.Unix(old, 0) }
	requestID, rootID := observedLog(t, r, userID, old)
	upstreamerror.CaptureEvent(r.ErrorScope(context.Background(), DiagnosticRef{RequestID: requestID, AttemptSeq: 1}), 503, "text/plain", []byte("root"))
	heldTask, _ := fixture.imageRoots(t)
	for i := range 2 {
		id := heldTask
		if i != 0 {
			id = diagnosticID(t, "img_")
			if _, err := r.db.Exec("INSERT INTO image_activity_tasks(id,user_id,model_id,model_revision,n,paper_charge_mag,brush_charge_mag,state,finance_state,slot_state,ledger_rows_remaining,created_at,updated_at,queue_deadline,execution_timeout_seconds,completed_at) SELECT ?,user_id,model_id,model_revision,n,paper_charge_mag,brush_charge_mag,state,finance_state,slot_state,ledger_rows_remaining,created_at,updated_at,queue_deadline,execution_timeout_seconds,completed_at FROM image_activity_tasks WHERE id=?", id, heldTask); err != nil {
				t.Fatal(err)
			}
		}
		upstreamerror.CaptureEvent(r.ErrorScope(context.Background(), DiagnosticRef{TaskID: id, AttemptSeq: 1}), 503, "text/plain", []byte("task"))
	}
	for _, identity := range []AccessIdentity{{UserID: userID, Generation: 3}, {}} {
		if err := r.recordAccess(context.Background(), accessEvent{identity: identity, source: Source{IPQuality: "direct_peer"}, pathKind: "models", method: "GET", status: 200, responseKind: "api_json", at: old}); err != nil {
			t.Fatal(err)
		}
	}
	r.now = func() time.Time { return time.Unix(now, 0) }
	held := func(_ context.Context, _ *sql.Tx, kind, id string, _ int64) (bool, error) {
		return kind == "image_task" && id == heldTask, nil
	}
	result, err := r.Retain(context.Background(), now, 10, time.Now().Add(2*time.Second), held)
	if err != nil {
		t.Fatal(err)
	}
	if result.Processed != 4 || result.Deleted != 3 || result.More {
		t.Fatalf("retention %+v", result)
	}
	if scalar(t, r.db, `SELECT count(*) FROM request_error_bodies`) != 2 || scalar(t, r.db, `SELECT raw_body_bytes FROM observability_state`) != 8 || scalar(t, r.db, `SELECT access_rows FROM observability_state`) != 0 || scalar(t, r.db, `SELECT count(*) FROM anonymous_access_minutes`) != 0 {
		t.Fatal("retention changed held/root evidence or failed to clear counters")
	}
	result, err = r.Retain(context.Background(), now, 10, time.Now().Add(2*time.Second), nil)
	if err != nil || result.Deleted != 1 {
		t.Fatalf("released retention %+v %v", result, err)
	}
	if _, err = r.db.Exec(`DELETE FROM request_logs WHERE id=?`, rootID); err != nil {
		t.Fatal(err)
	}
	if scalar(t, r.db, `SELECT raw_body_bytes FROM observability_state`) != 0 {
		t.Fatal("root cascade counter drift")
	}
}
