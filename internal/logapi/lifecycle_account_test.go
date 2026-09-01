package logapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type lifecycleLogDatabase struct {
	store *db.Store
	vault *secret.Vault
	repo  *Repository
}

func newLifecycleLogDatabase(t *testing.T) *lifecycleLogDatabase {
	t.Helper()
	key := bytes.Repeat([]byte{0x5c}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "log-lifecycle.sqlite")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	repository, err := NewRepository(store.DB(), vault)
	if err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = vault.Close()
	})
	return &lifecycleLogDatabase{store: store, vault: vault, repo: repository}
}

func seedLifecycleLogUser(t *testing.T, database *sql.DB, discordID string, admin bool) int64 {
	t.Helper()
	zero := make([]byte, 16)
	adminValue := 0
	var discord any = discordID
	if admin {
		adminValue = 1
		discord = nil
	}
	result, err := database.Exec(`
INSERT INTO users(
 discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
 total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,
 revision,lang,created_at,updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		discord, "lifecycle log user", adminValue, zero, zero, zero, zero, zero, zero, zero, zero,
		"en", int64(1_700_000_000), int64(1_700_000_000))
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertLifecycleRequestLog(
	t *testing.T,
	database *sql.DB,
	requestID string,
	userID int64,
	class any,
	status any,
	callerCode any,
	started int64,
	completed any,
	duration, unknown int64,
) int64 {
	t.Helper()
	requestState := "running"
	statusCode := int64(0)
	errorSource := "platform"
	errorCode := ""
	if class == string(ResultSuccess) {
		requestState = "terminal"
		statusCode = 200
	}
	if class == string(ResultFailed) {
		requestState = "terminal"
		statusCode = 502
		errorSource = "upstream"
		errorCode = "upstream"
	}
	if _, err := database.Exec(`
INSERT INTO logical_requests(
 id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,
 caller_error_code,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at
) VALUES(?,?,'openai_chat_completions','safe-model',?,1,?,?,?,'none','user',?,?,?)`,
		requestID, userID, requestState, class, status, callerCode, make([]byte, 16), started, completed); err != nil {
		t.Fatal(err)
	}
	result, err := database.Exec(`
INSERT INTO request_logs(
 logical_request_id,user_id,model,route_kind,caller_result_class,caller_status,caller_error_code,
 attempt_count,status_code,duration_ms,started_at,completed_at,uncached_input_tokens,
 cache_write_input_tokens,cache_read_input_tokens,output_tokens,usage_unknown,error_source,error_code,error_diag
) VALUES(?,?, 'safe-model','openai_chat_completions',?,?,?,?,?,?,?,?,1,2,3,4,?,?,?,'HOSTILE-RAW-LOG-DIAG')`,
		requestID, userID, class, status, callerCode, 1, statusCode, duration, started, completed,
		unknown, errorSource, errorCode)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertLifecycleAttempt(t *testing.T, database *sql.DB, requestLogID int64, started, completed int64) {
	t.Helper()
	claimID, err := db.GenerateOpaqueID("clm_")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
INSERT INTO request_attempts(
 claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,result_kind,
 upstream_status,upstream_code,diag,input_tokens,cache_write_input_tokens,cache_read_input_tokens,
 output_tokens,usage_unknown,started_at,completed_at
) VALUES(?,?,1,'openai-compatible','https://held.example/v1','upstream-safe','response',
         200,'HOSTILE-UPSTREAM-CODE','HOSTILE-RAW-UPSTREAM-DIAG',1,2,3,4,0,?,?)`,
		claimID, requestLogID, started, completed); err != nil {
		t.Fatal(err)
	}
}

type lifecycleQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type heldLogAggregateSnapshot struct {
	root     string
	attempts []string
}

func snapshotHeldLogAggregate(t *testing.T, queryer lifecycleQueryer, logID int64) heldLogAggregateSnapshot {
	t.Helper()
	var snapshot heldLogAggregateSnapshot
	err := queryer.QueryRowContext(context.Background(), `
SELECT quote(id)||'|'||quote(logical_request_id)||'|'||quote(model)||'|'||quote(endpoint_key_id)||'|'||
       quote(upstream_model_id)||'|'||quote(route_kind)||'|'||quote(endpoint_base_url)||'|'||
       quote(caller_result_class)||'|'||quote(caller_status)||'|'||quote(caller_error_code)||'|'||
       quote(attempt_count)||'|'||quote(status_code)||'|'||quote(duration_ms)||'|'||quote(started_at)||'|'||
       quote(completed_at)||'|'||quote(uncached_input_tokens)||'|'||quote(cache_write_input_tokens)||'|'||
       quote(cache_read_input_tokens)||'|'||quote(output_tokens)||'|'||quote(usage_unknown)||'|'||
       quote(error_source)||'|'||quote(error_code)||'|'||quote(error_diag)||'|'||quote(legal_hold_consumed)
FROM request_logs WHERE id=?`, logID).Scan(&snapshot.root)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := queryer.QueryContext(context.Background(), `
SELECT quote(claim_id)||'|'||quote(attempt_seq)||'|'||quote(endpoint_id_snapshot)||'|'||
       quote(endpoint_key_id_snapshot)||'|'||quote(connector_type)||'|'||quote(canonical_base_url)||'|'||
       quote(upstream_model_id)||'|'||quote(result_kind)||'|'||quote(upstream_status)||'|'||
       quote(upstream_code)||'|'||quote(diag)||'|'||quote(input_tokens)||'|'||
       quote(cache_write_input_tokens)||'|'||quote(cache_read_input_tokens)||'|'||quote(output_tokens)||'|'||
       quote(usage_unknown)||'|'||quote(started_at)||'|'||quote(completed_at)
FROM request_attempts WHERE request_log_id=? ORDER BY attempt_seq`, logID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		snapshot.attempts = append(snapshot.attempts, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestLifecycleLogSummaryRetentionAndHeldDeletion(t *testing.T) {
	fixture := newLifecycleLogDatabase(t)
	userID := seedLifecycleLogUser(t, fixture.store.DB(), "lifecycle-log-owner", false)
	adminID := seedLifecycleLogUser(t, fixture.store.DB(), "", true)
	decisionNow := int64(1_800_000_000)
	cutoff := decisionNow - requestLogRetentionSeconds
	heldRequestID, _ := db.GenerateOpaqueID("req_")
	recentRequestID, _ := db.GenerateOpaqueID("req_")
	pendingRequestID, _ := db.GenerateOpaqueID("req_")
	heldLogID := insertLifecycleRequestLog(t, fixture.store.DB(), heldRequestID, userID,
		string(ResultSuccess), 200, nil, cutoff-10, cutoff, 900, 0)
	recentLogID := insertLifecycleRequestLog(t, fixture.store.DB(), recentRequestID, userID,
		string(ResultFailed), 502, "upstream", decisionNow-20, decisionNow-10, 100, 1)
	pendingLogID := insertLifecycleRequestLog(t, fixture.store.DB(), pendingRequestID, userID,
		nil, nil, nil, decisionNow-5, nil, 0, 0)
	insertLifecycleAttempt(t, fixture.store.DB(), heldLogID, cutoff-9, cutoff-8)
	insertLifecycleAttempt(t, fixture.store.DB(), recentLogID, decisionNow-19, decisionNow-18)
	insertLifecycleAttempt(t, fixture.store.DB(), pendingLogID, decisionNow-4, decisionNow-3)
	holdID, _ := db.GenerateOpaqueID("lgh_")
	if _, err := fixture.store.DB().Exec(`
INSERT INTO legal_holds(
 id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at
) VALUES(?,'request_log',?,'active',1,'test basis',?,?,?)`,
		holdID, fmt.Sprintf("%d", heldLogID), adminID, decisionNow-100, decisionNow+100); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE request_logs SET legal_hold_consumed=1 WHERE id=?`, heldLogID); err != nil {
		t.Fatal(err)
	}

	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := fixture.repo.ExportLifecycleSummary(context.Background(), tx, userID, decisionNow, 1)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if summary.TotalLogs != "2" || summary.LogsLast30Days != "2" || summary.ErrorLogs != "1" ||
		summary.UsageUnknownLogs != "1" || summary.AverageDuration != "50" {
		t.Fatalf("lifecycle log summary=%+v", summary)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"HOSTILE-RAW-LOG-DIAG", "HOSTILE-RAW-UPSTREAM-DIAG", "safe-model", "upstream-safe"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("forbidden request-log material %q in %s", forbidden, encoded)
		}
	}

	tx, err = fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.repo.PrepareLifecycleAccountDeletion(context.Background(), tx, userID, decisionNow); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	var preparedRoots int
	if err := tx.QueryRow(`SELECT count(*) FROM request_logs WHERE user_id=?`, userID).Scan(&preparedRoots); err != nil || preparedRoots != 1 {
		t.Fatalf("prepared roots=%d err=%v", preparedRoots, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var rolledBackRoots int
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM request_logs WHERE user_id=?`, userID).Scan(&rolledBackRoots); err != nil || rolledBackRoots != 3 {
		t.Fatalf("rolled-back roots=%d err=%v", rolledBackRoots, err)
	}

	tx, err = fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotHeldLogAggregate(t, tx, heldLogID)
	if err := fixture.repo.PrepareLifecycleAccountDeletion(context.Background(), tx, userID, decisionNow); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	// The forward owner clears its live logical-request projection separately;
	// its sync trigger clears request_logs.user_id without changing the held
	// evidence aggregate owned by logapi.
	if _, err := tx.Exec(`UPDATE logical_requests SET user_id=NULL WHERE user_id=?`, userID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, userID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	after := snapshotHeldLogAggregate(t, tx, heldLogID)
	if !reflect.DeepEqual(after, before) {
		_ = tx.Rollback()
		t.Fatalf("held request-log aggregate changed\nbefore=%+v\nafter=%+v", before, after)
	}
	var projectedUser sql.NullInt64
	if err := tx.QueryRow(`SELECT user_id FROM request_logs WHERE id=?`, heldLogID).Scan(&projectedUser); err != nil || projectedUser.Valid {
		_ = tx.Rollback()
		t.Fatalf("held user projection=%+v err=%v", projectedUser, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, deletedID := range []int64{recentLogID, pendingLogID} {
		var count int
		if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM request_logs WHERE id=?`, deletedID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("non-held log %d count=%d err=%v", deletedID, count, err)
		}
	}
	var heldRoots, heldAttempts int
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM request_logs WHERE id=?`, heldLogID).Scan(&heldRoots); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM request_attempts WHERE request_log_id=?`, heldLogID).Scan(&heldAttempts); err != nil {
		t.Fatal(err)
	}
	if heldRoots != 1 || heldAttempts != 1 {
		t.Fatalf("held aggregate roots=%d attempts=%d", heldRoots, heldAttempts)
	}
}
