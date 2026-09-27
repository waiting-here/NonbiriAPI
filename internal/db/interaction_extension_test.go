package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func interactionSourceFixture(t *testing.T) *sql.DB {
	t.Helper()
	// Compare the retained published DDL independently of the target pin.
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(generationTwoWithoutInteractionsSchema))); got != "c5656895b47dcf9b9893de580d72ef2dd6c2bea530e8f9f28ef304377d01bfdd" {
		t.Fatalf("released source DDL drift: %s", got)
	}
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	if _, err := database.Exec("PRAGMA foreign_keys=ON;" + generationTwoWithoutInteractionsSchema); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, preInteractionManifestHash)
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := seedGenerationTwo(context.Background(), tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestWorkerSuccessTimestampUpgradePreservesUnknownHistory(t *testing.T) {
	database := interactionSourceFixture(t)
	hostileMustExec(t, database, `INSERT INTO worker_checkpoints(worker_key,generation,attempt_count,next_attempt_at,updated_at) VALUES('lifecycle_recovery_v1',1,0,0,100)`)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var recorded sql.NullInt64
	if err := database.QueryRow(`SELECT last_success_at FROM worker_checkpoints WHERE worker_key='lifecycle_recovery_v1'`).Scan(&recorded); err != nil || recorded.Valid {
		t.Fatal("upgrade invented a successful checkpoint", recorded, err)
	}
	for _, value := range []any{1.5, int64(-1), int64(101), "not-a-timestamp"} {
		if _, err := database.Exec(`UPDATE worker_checkpoints SET last_success_at=? WHERE worker_key='lifecycle_recovery_v1'`, value); err == nil {
			t.Fatalf("invalid last-success timestamp accepted: %v", value)
		}
	}
	hostileMustExec(t, database, `UPDATE worker_checkpoints SET last_success_at=100 WHERE worker_key='lifecycle_recovery_v1'`)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT last_success_at FROM worker_checkpoints WHERE worker_key='lifecycle_recovery_v1'`).Scan(&recorded); err != nil || !recorded.Valid || recorded.Int64 != 100 {
		t.Fatal("reentry changed the successful checkpoint", recorded, err)
	}
}

func TestInteractionUpgradePreservesExistingScanResults(t *testing.T) {
	database := interactionSourceFixture(t)
	user := hostileInsertUser(t, database, "retained scan", 1, 1)
	scan := hostileOID("scn_")
	var logs []int64
	for index := 0; index < 2; index++ {
		request, err := GenerateOpaqueID("req_")
		if err != nil {
			t.Fatal(err)
		}
		hostileInsertTerminalRequest(t, database, request, user, "openai_chat_completions", "success", 200, nil)
		log := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO request_logs(logical_request_id,user_id,route_kind,model,upstream_model_id,endpoint_base_url,caller_result_class,caller_status,status_code,attempt_count,started_at,completed_at) VALUES(?,?,'openai_chat_completions','model','upstream','https://upstream.example/v1','success',200,200,0,0,1)`, request, user))
		hostileMustExec(t, database, `INSERT INTO request_source_facts VALUES(?,?,'self','192.0.2.1','direct_peer','{}',1)`, log, user)
		logs = append(logs, log)
	}
	hostileMustExec(t, database, `INSERT INTO risk_client_scans(id,user_id,admin,request_token,query_json,rules_json,state,from_at,to_at,call_kind,model,upper_log_id,after_at,candidates,scanned,matched,created_at,updated_at,expires_at) VALUES(?,?,1,'abcdefghijklmnop','{}','[]','completed',0,100,'total','',?,0,3,3,3,0,0,86400)`, scan, user, logs[1])
	for index, log := range logs {
		hostileMustExec(t, database, `INSERT INTO risk_client_scan_matches VALUES(?,?,?)`, scan, log, 1+index*2)
	}
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	for index, log := range logs {
		var id, ordinal, source int64
		var published int
		var value string
		if err := database.QueryRow(`SELECT r.request_log_id,r.row_no,r.result_json,s.request_log_id,r.published FROM risk_scan_results r JOIN risk_scan_result_sources s USING(scan_id,row_no) WHERE r.scan_id=? AND r.row_no=?`, scan, 1+index*2).Scan(&id, &ordinal, &value, &source, &published); err != nil || id != log || source != log || ordinal != int64(1+index*2) || value != "{}" || published != 1 {
			t.Fatal("upgrade lost the stable result or source", id, ordinal, value, source, err)
		}
	}
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, database, `UPDATE request_source_facts SET user_id=NULL WHERE request_log_id=?`, logs[0])
	var count, changed int
	if err := database.QueryRow(`SELECT (SELECT count(*) FROM risk_scan_results WHERE scan_id=?),changed FROM risk_client_scans WHERE id=?`, scan, scan).Scan(&count, &changed); err != nil || count != 1 || changed != 1 {
		t.Fatal("retained result missed privacy invalidation", count, changed, err)
	}
}

// Preserve every original column and row, including opaque credentials and
// arbitrary instance text, while permitting only the new hidden activity row.
func interactionOriginalRows(t *testing.T, database *sql.DB, source generationManifest) map[string][]string {
	t.Helper()
	out := make(map[string][]string)
	for _, table := range source.Tables {
		if strings.HasPrefix(table.Name, "sqlite_") {
			continue
		}
		columns := make([]string, len(table.Columns))
		for i, col := range table.Columns {
			columns[i] = hostileQuoteIdent(col.Name)
		}
		query := "SELECT " + strings.Join(columns, ",") + " FROM " + hostileQuoteIdent(table.Name)
		if table.Name == "limited_activity_configs" || table.Name == "limited_activity_revisions" {
			query += " WHERE activity_key<>'fat-fish'"
		}
		rows, err := database.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		out[table.Name] = []string{}
		for rows.Next() {
			values, ptrs := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			out[table.Name] = append(out[table.Name], string(encoded))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		sort.Strings(out[table.Name])
	}
	return out
}

func TestInteractionPublishedSourcePreservesRows(t *testing.T) {
	ctx := context.Background()
	database := interactionSourceFixture(t)
	user := hostileInsertUser(t, database, "retained", 0, 1)
	endpoint := hostileInsertEndpoint(t, database, user, "https://example.invalid/api")
	credential := hostileInsertSecret(t, database, "https://example.invalid/api", 1)
	hostileInsertEndpointKey(t, database, endpoint, credential)
	hostileMustExec(t, database, `UPDATE users SET level=6,is_banned=1,banned_until=5000,charity_suspended_until=7000 WHERE id=?`, user)
	hostileMustExec(t, database, `UPDATE site_config SET value='Preserve literal multilingual text: 法律\n',updated_at=17 WHERE key IN ('site_name','legal_privacy_override_zh','legal_privacy_override_en','legal_terms_override_zh','legal_terms_override_en')`)
	hostileMustExec(t, database, `INSERT INTO discord_blacklist VALUES('123456789012345678','original first reason',2)`)
	hostileMustExec(t, database, `INSERT INTO admin_alerts(kind,message,created_at) VALUES('account_deleted','retained deletion',3)`)
	hostileMustExec(t, database, `INSERT INTO admin_account_deletions(alert_id,snapshot_json) VALUES(1,'{"user_id":"9223372036854775807","discord_id":"former-discord"}')`)
	control, model := hostileOID("iup_"), hostileOID("imdl_")
	hostileMustExec(t, database, `INSERT INTO image_upstream_control(id,identity_hash,rpm_limit,concurrency_limit,protection_paused,protection_reason,protection_revision,updated_at) VALUES(?,zeroblob(32),60,2,0,'',1,1)`, control)
	hostileMustExec(t, database, `INSERT INTO image_activity_models(id,control_id,upstream_model_id,metadata_json,discovered_at) VALUES(?,?,'legacy-model','{}',1)`, model, control)
	hostileMustExec(t, database, `INSERT INTO image_model_revisions(model_id,revision,display_name,description,enabled,parameters_json,combinations_json,mapping_json,paper_price_mag,brush_price_mag,created_at) VALUES(?,1,'Existing model','keep',1,'[]','[]','{}',X'00000000000000000000000000001B58',zeroblob(16),1)`, model)
	hostileMustExec(t, database, `UPDATE image_activity_models SET current_revision=1 WHERE id=?`, model)
	source, err := readGenerationManifest(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	before := interactionOriginalRows(t, database, source)
	if err := extendKnownGenerationTwoSchema(ctx, database); err != nil {
		t.Fatal(err)
	}
	if after := interactionOriginalRows(t, database, source); !reflect.DeepEqual(before, after) {
		t.Fatal("published source row changed")
	}
	assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
	if err := foreignKeyCheck(ctx, database); err != nil {
		t.Fatal(err)
	}
	// A first note has the same Unicode-scalar budget as later notes.
	if _, err := database.Exec(`INSERT INTO discord_blacklist VALUES('200000000000000001',?,3)`, strings.Repeat("界", 2000)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO discord_blacklist VALUES('200000000000000002',?,3)`, strings.Repeat("界", 2001)); err == nil {
		t.Fatal("accepted an oversized first note")
	}
	hostileMustExec(t, database, `DELETE FROM discord_blacklist WHERE discord_id='200000000000000001'`)
	var visible, rows int
	if err := database.QueryRow(`SELECT visible FROM limited_activity_configs WHERE activity_key='fat-fish'`).Scan(&visible); err != nil || visible != 0 {
		t.Fatal(visible, err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM client_rule_auto_bans`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal(rows, err)
	}
	var readiness, price string
	if err := database.QueryRow(`SELECT readiness FROM image_model_capability_revisions WHERE model_id=?`, model).Scan(&readiness); err != nil || readiness != "legacy" {
		t.Fatal(readiness, err)
	}
	if err := database.QueryRow(`SELECT hex(default_paper_mag) FROM image_model_pricing_revisions WHERE model_id=?`, model).Scan(&price); err != nil || price != fmt.Sprintf("%032X", 7000) {
		t.Fatal(price, err)
	}
	var former int64
	var unknown sql.NullInt64
	if err := database.QueryRow(`SELECT former_user_id,registered_at FROM admin_account_deletions`).Scan(&former, &unknown); err != nil || former != 9223372036854775807 || unknown.Valid {
		t.Fatal(former, unknown, err)
	}
	if err := extendKnownGenerationTwoSchema(ctx, database); err != nil {
		t.Fatal(err)
	}
	if after := interactionOriginalRows(t, database, source); !reflect.DeepEqual(before, after) {
		t.Fatal("restart changed published facts")
	}
	fresh := openGenerationTwoDDLForTest(t)
	defer fresh.Close()
	want, err := readGenerationManifest(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readGenerationManifest(ctx, database)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh and upgrade manifest differ: %v", err)
	}
}

func TestInteractionMigrationRollbackAndUnknownSource(t *testing.T) {
	database := interactionSourceFixture(t)
	ctx := context.Background()
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyInteractionExtension(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA writable_schema=RESET`); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, preInteractionManifestHash)
	hostileMustExec(t, database, `CREATE TABLE unrecognized_source(id INTEGER PRIMARY KEY) STRICT`)
	if err := extendKnownGenerationTwoSchema(ctx, database); err == nil {
		t.Fatal("unknown source accepted")
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='fatfish_capacity'`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	hostileMustExec(t, database, `DROP TABLE unrecognized_source`)
	if err := extendKnownGenerationTwoSchema(ctx, database); err != nil {
		t.Fatal(err)
	}
}
