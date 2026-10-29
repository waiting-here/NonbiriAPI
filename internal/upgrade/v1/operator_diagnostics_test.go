package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

var operatorDiagnosticsTables = []string{"agent_codex_session", "request_attempt_diagnostics", "request_delivery_facts", "agent_request_diagnostics", "request_error_diagnostics", "management_operation_terms", "management_capture_rows", "management_capture_totals", "management_capture_context"}

func restoreSchema41LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	rows, err := database.Query(`SELECT name FROM sqlite_schema WHERE type='trigger' AND name GLOB 'management_capture_*' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var ddl strings.Builder
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		fmt.Fprintf(&ddl, "DROP TRIGGER %q;\n", name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, table := range operatorDiagnosticsTables {
		fmt.Fprintf(&ddl, "DROP TABLE %q;\n", table)
	}
	ddl.WriteString(`DROP INDEX management_operation_items_name;
ALTER TABLE management_operations DROP COLUMN target_name;
ALTER TABLE management_operation_items DROP COLUMN details;
DROP TABLE agent_request_drivers;
`)
	start, end := strings.Index(db.AgentDriversSQL, "CREATE TABLE agent_request_drivers ("), strings.Index(db.AgentDriversSQL, "CREATE TABLE agent_chatgpt_session (")
	if start < 0 || end <= start {
		t.Fatal("published driver schema is missing")
	}
	ddl.WriteString(db.AgentDriversSQL[start:end])
	hostileMustExec(t, database, ddl.String())
	assertRetainedManifest(t, database, db.Schema41ManifestHash)
}

func TestOperatorDiagnosticsSchema41UpgradePreservesDataAndRollsBack(t *testing.T) {
	path, vault := bootstrapTestPath(t, "operator-diagnostics.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	freshManifest, err := db.SchemaManifestHash(t.Context(), store.DB())
	if err != nil || freshManifest != db.PinnedGenerationTwoManifestHash {
		t.Fatal("fresh manifest", freshManifest, err)
	}
	restoreSchema41LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=41")
	owner := hostileInsertUser(t, store.DB(), "retained-operator", 0, 0)
	hostileMustExec(t, store.DB(), `INSERT INTO agent_driver_profiles(id,revision,name,settings_json,updated_at) VALUES(7,4,'Original profile','{"model":"original"}',12)`)
	hostileMustExec(t, store.DB(), `INSERT INTO agent_driver_bindings(scope,actor_id,driver_id,revision) VALUES('steward_chat',?,7,3)`, owner)
	for i, provider := range []string{"chat_completions", "responses", "chatgpt"} {
		id := i + 1
		hostileMustExec(t, store.DB(), `INSERT INTO agent_requests(id,client_request_id,source,kind,actor_id,actor_role,actor_label,input_json,state,created_at,completed_at,consumed_at) VALUES(?,?,'steward_chat','model',?,'level5','Original actor','{}','failed',1,2,3)`, id, fmt.Sprintf("retained-request-%d", id), owner)
		hostileMustExec(t, store.DB(), `INSERT INTO agent_request_drivers(request_id,driver_id,provider,settings_json) VALUES(?,7,?,?)`, id, provider, fmt.Sprintf(`{"provider":%q,"model":"original"}`, provider))
	}
	for i, outcome := range []string{"succeeded", "rejected", "partial"} {
		id := i + 1
		hostileMustExec(t, store.DB(), `INSERT INTO management_operations(id,event_id,created_at,bucket,group_key,source,executor_kind,actor_id,actor_name,authorizer_name,domain,action,outcome,code,summary,targets,succeeded,failed) VALUES(?,?,10,0,'retained-group','web','human',?,'Original actor','Original authorizer','models','update',?,'original_result','Original result',1,?,?)`, id, fmt.Sprintf("retained-event-%d", id), fmt.Sprint(owner), outcome, i%2, 1-i%2)
		hostileMustExec(t, store.DB(), `INSERT INTO management_operation_items(operation_id,ordinal,action,target_kind,target_id,target_name,outcome,code,changes) VALUES(?,1,'update','model','77','Original 名称',?,'original_result','[{"field":"enabled","operation":"updated","before":"0","after":"1"}]')`, id, outcome)
	}
	hostileMustExec(t, store.DB(), `INSERT INTO management_operation_rollups(group_key,bucket,source,domain,action,actor_id,actor_name,actor_role,authorizer_id,authorizer_name,authorizer_role,outcome,operations,targets,succeeded,skipped,failed,min_id,max_id) VALUES('retained-group',0,'web','models','update',?,'Original actor','level5','','Original authorizer','admin','partial',3,3,1,0,2,1,3)`, fmt.Sprint(owner))
	// A deleted last row must not lower the next audit identity after upgrade.
	hostileMustExec(t, store.DB(), `UPDATE sqlite_sequence SET seq=90 WHERE name='management_operations'`)
	var logIDs, errorIDs []int64
	var claimIDs []string
	for i := range 2 {
		requestID, err := db.GenerateOpaqueID("req_")
		if err != nil {
			t.Fatal(err)
		}
		claimID, err := db.GenerateOpaqueID("clm_")
		if err != nil {
			t.Fatal(err)
		}
		hostileInsertLogicalRequest(t, store.DB(), requestID, owner, "openai_chat_completions", 100)
		logID := hostileInsertRequestLog(t, store.DB(), requestID, owner, "openai_chat_completions")
		hostileMustExec(t, store.DB(), `UPDATE logical_requests SET state='terminal',caller_result_class='success',caller_status=200,terminal_at=1,ledger_rows_remaining=zeroblob(16) WHERE id=?`, requestID)
		hostileInsertAttempt(t, store.DB(), claimID, logID, int64(1+i*99))
		errorID := hostileMustLastID(t, hostileMustExec(t, store.DB(), `INSERT INTO request_error_bodies(request_log_id,attempt_seq,event_seq,http_status,content_type,body,bytes_saved,truncated,save_state,created_at,expires_at) VALUES(?,1,1,502,'text/plain',X'',0,0,'saved',1,10)`, logID))
		logIDs, errorIDs, claimIDs = append(logIDs, logID), append(errorIDs, errorID), append(claimIDs, claimID)
	}
	columns, before := legacyConnectionColumns(t, store.DB()), gatewayRows(t, store.DB())
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("schema41 started without explicit upgrade")
	} else {
		var mismatch *db.StartupError
		if !errors.As(err, &mismatch) || mismatch.Kind != db.StartupSchemaMismatch || mismatch.ActualSchemaVersion == nil || *mismatch.ActualSchemaVersion != 41 {
			t.Fatal("wrong historical source rejection", err)
		}
	}
	checkSource := func(t *testing.T) {
		t.Helper()
		if err := db.InspectContext(t.Context(), path, vault, func(_ context.Context, database *sql.DB) error {
			assertExactPublishedSource(t, database, 41, db.Schema41ManifestHash, before)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Run(t.Context(), path, vault, Options{})
	if err != nil || result.Status != "ready" || result.SourceSchemaVersion != 41 || result.TargetSchemaVersion != 42 {
		t.Fatal("read-only inspection", result, err)
	}
	checkSource(t)
	index := migrationIndex(t, 41)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	for _, mode := range []string{"failure", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reached := false
			storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
				if err := original(ctx, tx); err != nil {
					return err
				}
				reached = true
				if mode == "cancellation" {
					cancel()
					return ctx.Err()
				}
				return errors.New("injected after complete diagnostic migration")
			}
			_, err := Run(ctx, path, vault, Options{Apply: true})
			storageSchema.versions[index].apply = original
			if !reached || err == nil {
				t.Fatal("rollback boundary was not exercised", reached, err)
			}
			checkSource(t)
		})
	}
	result, err = Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" || result.SourceSchemaVersion != 41 || result.TargetSchemaVersion != 42 {
		t.Fatal(result, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, store.DB(), freshManifest)
	assertOperatorSourceRows(t, store.DB(), columns, before)
	for _, table := range operatorDiagnosticsTables {
		assertOperatorCount(t, store.DB(), table, 0)
	}
	var oldNames, oldDetails int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM management_operations WHERE target_name=''`).Scan(&oldNames); err != nil || oldNames != 3 {
		t.Fatal("historical names were invented", oldNames, err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM management_operation_items WHERE details='{}' AND target_name='Original 名称'`).Scan(&oldDetails); err != nil || oldDetails != 3 {
		t.Fatal("historical snapshots were invented or names lost", oldDetails, err)
	}
	if err := checkForeignKeys(t.Context(), store.DB(), "PRAGMA foreign_key_check"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	result, err = Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "current" {
		t.Fatal("repeated upgrade", result, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	assertOperatorSourceRows(t, store.DB(), columns, before)
	t.Run("lifecycle", func(t *testing.T) {
		assertOperatorDiagnosticsLifecycle(t, store.DB(), owner, logIDs, errorIDs, claimIDs)
	})
}

func assertOperatorSourceRows(t *testing.T, database *sql.DB, columns map[string][]string, want map[string][]string) {
	t.Helper()
	for table, names := range columns {
		selected := make([]string, len(names))
		for i, name := range names {
			selected[i] = quoteSQLiteIdentifier(name)
		}
		rows, err := database.Query("SELECT " + strings.Join(selected, ",") + " FROM " + quoteSQLiteIdentifier(table))
		if err != nil {
			t.Fatal(table, err)
		}
		got := []string{}
		for rows.Next() {
			values, refs := make([]any, len(names)), make([]any, len(names))
			for i := range values {
				refs[i] = &values[i]
			}
			if err := rows.Scan(refs...); err != nil {
				rows.Close()
				t.Fatal(table, err)
			}
			body, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			got = append(got, string(body))
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(table, err)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want[table]) {
			t.Errorf("published rows or identity high-water marks changed in %s", table)
		}
	}
}

func assertOperatorCount(t *testing.T, database *sql.DB, table string, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM " + quoteSQLiteIdentifier(table)).Scan(&count); err != nil || count != want {
		t.Fatalf("%s: count=%d want=%d error=%v", table, count, want, err)
	}
}

func assertOperatorDiagnosticsLifecycle(t *testing.T, database *sql.DB, owner int64, logIDs, errorIDs []int64, claimIDs []string) {
	t.Helper()
	for i := range logIDs {
		hostileMustExec(t, database, `INSERT INTO request_attempt_diagnostics(claim_id,request_log_id,attempt_seq,category,phase,details,completed_at) VALUES(?,?,?,'network','dial','{}',1)`, claimIDs[i], logIDs[i], 1+i*99)
		hostileMustExec(t, database, `INSERT INTO request_delivery_facts(request_log_id,details) VALUES(?,'{}')`, logIDs[i])
		hostileMustExec(t, database, `INSERT INTO request_error_diagnostics(error_id,details) VALUES(?,'{}')`, errorIDs[i])
	}
	hostileMustExec(t, database, `INSERT INTO agent_request_diagnostics(request_id,details) VALUES(1,'{}')`)
	hostileMustExec(t, database, `INSERT INTO agent_codex_session(id,revision,account_label,updated_at) VALUES(1,1,'Independent connection',1)`)
	hostileMustExec(t, database, `INSERT INTO management_operation_terms(term,operation_id,ordinal) VALUES('Original 名称',1,1)`)
	for _, query := range []string{
		`INSERT INTO request_attempt_diagnostics(claim_id,request_log_id,attempt_seq,category,phase,details,completed_at) VALUES('missing',999,1,'network','dial','{}',1)`,
		`INSERT INTO request_delivery_facts(request_log_id,details) VALUES(999,'{}')`,
		`INSERT INTO agent_request_diagnostics(request_id,details) VALUES(999,'{}')`,
		`INSERT INTO request_error_diagnostics(error_id,details) VALUES(999,'{}')`,
		`INSERT INTO management_operation_terms(term,operation_id,ordinal) VALUES('missing',999,1)`,
		`UPDATE request_attempt_diagnostics SET attempt_seq=101`,
		`UPDATE request_attempt_diagnostics SET attempt_seq=0`,
		`UPDATE request_delivery_facts SET details='[]'`,
		`UPDATE agent_request_diagnostics SET details='invalid'`,
	} {
		if _, err := database.Exec(query); err == nil {
			t.Fatal("invalid diagnostic relationship or boundary accepted", query)
		}
	}
	boundedJSON := `{"text":"` + strings.Repeat("x", 16373) + `"}`
	for _, table := range []string{"request_attempt_diagnostics", "request_delivery_facts", "agent_request_diagnostics", "request_error_diagnostics"} {
		hostileMustExec(t, database, "UPDATE "+table+" SET details=?", boundedJSON)
		if _, err := database.Exec("UPDATE "+table+" SET details=?", boundedJSON+" "); err == nil {
			t.Fatal("oversized diagnostic accepted", table)
		}
	}
	// Terminal requests are detached before the account FK deletes its owner.
	hostileMustExec(t, database, `UPDATE logical_requests SET user_id=NULL WHERE user_id=?`, owner)
	hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, owner)
	for table, want := range map[string]int{"agent_codex_session": 1, "agent_request_diagnostics": 1, "agent_requests": 3, "agent_request_drivers": 3, "agent_driver_profiles": 1, "agent_driver_bindings": 1, "management_operations": 3, "management_operation_items": 3, "management_operation_terms": 1, "request_attempt_diagnostics": 2, "request_delivery_facts": 2, "request_error_diagnostics": 2} {
		assertOperatorCount(t, database, table, want)
	}
	hostileMustExec(t, database, `DELETE FROM request_attempts WHERE claim_id=?`, claimIDs[0])
	assertOperatorCount(t, database, "request_attempt_diagnostics", 1)
	assertOperatorCount(t, database, "request_delivery_facts", 2)
	hostileMustExec(t, database, `DELETE FROM request_error_bodies WHERE id=?`, errorIDs[0])
	assertOperatorCount(t, database, "request_error_diagnostics", 1)
	hostileMustExec(t, database, `DELETE FROM request_logs WHERE id=?`, logIDs[1])
	for _, table := range []string{"request_attempt_diagnostics", "request_error_diagnostics"} {
		assertOperatorCount(t, database, table, 0)
	}
	assertOperatorCount(t, database, "request_delivery_facts", 1)
	hostileMustExec(t, database, `DELETE FROM agent_requests WHERE id=1`)
	assertOperatorCount(t, database, "agent_request_diagnostics", 0)
	assertOperatorCount(t, database, "agent_request_drivers", 2)
	assertOperatorCount(t, database, "agent_codex_session", 1)
	// Management history has a separate explicit cleanup path, not a user FK.
	hostileMustExec(t, database, `DELETE FROM management_operation_items WHERE operation_id=1`)
	if _, err := database.Exec(`DELETE FROM management_operations WHERE id=1`); err == nil {
		t.Fatal("management term allowed its operation to disappear")
	}
	hostileMustExec(t, database, `DELETE FROM management_operation_terms WHERE operation_id=1; DELETE FROM management_operations WHERE id=1`)
	assertOperatorCount(t, database, "management_operation_terms", 0)
	assertOperatorCount(t, database, "management_operations", 2)
	hostileMustExec(t, database, `UPDATE agent_request_drivers SET provider='codex' WHERE request_id=2`)
	for _, table := range []string{"management_capture_rows", "management_capture_totals", "management_capture_context"} {
		assertOperatorCount(t, database, table, 0)
	}
	if err := checkForeignKeys(t.Context(), database, "PRAGMA foreign_key_check"); err != nil {
		t.Fatal(err)
	}
}
