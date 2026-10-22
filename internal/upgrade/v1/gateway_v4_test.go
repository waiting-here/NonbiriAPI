package v1

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestGatewayV4UpgradePreservesHistoryAndCredentials(t *testing.T) {
	ctx := context.Background()
	path, vault := sourceFixtureFromDDL(t, schema31SQLForTest(t), 31, stableSchema31Manifest)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	mustExec(t, database, "PRAGMA foreign_keys=ON")
	plan, err := storageSchema.plan(ctx, database)
	if err != nil || len(plan.steps) != 1 || len(plan.steps[0].impact.rebuiltTables) != 7 || !plan.auditsCredentials() {
		t.Fatalf("Gateway migration impact = %+v, %v", plan, err)
	}
	owner := hostileInsertUser(t, database, "gateway-owner", 0, 1)
	seedGatewayRows(t, database, vault, owner, 1, "ai-sdk-gateway-v3")
	// The high-water mark must survive even when its row no longer exists.
	mustExec(t, database, `UPDATE sqlite_sequence SET seq=9000 WHERE name='endpoints'`)
	beforeRows := gatewayRows(t, database)
	beforeDDL, err := readGenerationManifest(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	probe := errors.New("rollback after Gateway table rebuilds")
	if err := runGenerationTwoExtension(ctx, database, func(ctx context.Context, tx *sql.Tx) error {
		if err := extendGatewayV4(ctx, tx); err != nil {
			return err
		}
		return probe
	}); !errors.Is(err, probe) {
		t.Fatalf("rollback error = %v", err)
	}
	afterRollback, err := readGenerationManifest(ctx, database)
	if err != nil || generationManifestDigest(afterRollback) != generationManifestDigest(beforeDDL) || !reflect.DeepEqual(gatewayRows(t, database), beforeRows) {
		t.Fatalf("rollback changed source schema or rows: %v", err)
	}
	assertForeignKeyEnforcement(t, database)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := Run(ctx, path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" || result.TargetSchemaVersion != 32 {
		t.Fatalf("upgrade = %+v, %v", result, err)
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	database = store.DB()
	if after := gatewayRows(t, database); !reflect.DeepEqual(after, beforeRows) {
		for table, rows := range beforeRows {
			if !reflect.DeepEqual(after[table], rows) {
				t.Errorf("migration changed %s rows", table)
			}
		}
		t.FailNow()
	}
	afterDDL, err := readGenerationManifest(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	wantChanged := map[string]bool{"mainstream_channels": true, "endpoints": true, "endpoint_key_secrets": true, "donation_keys": true, "request_attempts": true, "report_cases": true, "report_targets": true}
	prior := map[string]schemaObject{}
	for _, object := range beforeDDL.Objects {
		prior[object.Type+"/"+object.Name] = object
	}
	for _, object := range afterDDL.Objects {
		old, found := prior[object.Type+"/"+object.Name]
		if !found {
			t.Fatalf("unexpected schema object %s", object.Name)
		}
		if object.SQL != old.SQL {
			if object.Type != "table" || !wantChanged[object.Name] || object.SQL != strings.Replace(old.SQL, "'ai-sdk-gateway-v3'", "'ai-sdk-gateway-v3','ai-sdk-gateway-v4'", 1) {
				t.Fatalf("unexpected schema change %s", object.Name)
			}
			delete(wantChanged, object.Name)
		}
	}
	if len(wantChanged) != 0 || len(afterDDL.Objects) != len(beforeDDL.Objects) {
		t.Fatalf("wrong schema closure: missing=%v objects=%d/%d", wantChanged, len(afterDDL.Objects), len(beforeDDL.Objects))
	}
	// Exercise every widened table with real foreign keys and credential envelopes.
	seedGatewayRows(t, database, vault, owner, 2, "ai-sdk-gateway-v4")
	if _, err := database.Exec(`INSERT INTO endpoints(user_id,connector_type,base_url,created_at,updated_at) VALUES(?,'ai-sdk-gateway-v5','https://gateway.example/v1',1,1)`, owner); err == nil {
		t.Fatal("unknown Gateway version accepted")
	}
	if _, err := database.Exec(`UPDATE endpoints SET connector_type='ai-sdk-gateway-v4' WHERE id=1`); err == nil {
		t.Fatal("old endpoint identity became mutable")
	}
	if _, err := database.Exec(`UPDATE endpoint_key_secrets SET connector_type='ai-sdk-gateway-v4' WHERE id=1`); err == nil {
		t.Fatal("credential identity became mutable")
	}
	if _, err := database.Exec(`UPDATE endpoint_keys SET secret_ref_id=2 WHERE id=1`); err == nil {
		t.Fatal("cross-version credential attachment accepted")
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM endpoints WHERE user_id=? AND base_url='https://gateway.example/v1'`, owner).Scan(&count); err != nil || count != 2 {
		t.Fatalf("same-base version coexistence = %d, %v", count, err)
	}
	assertForeignKeyEnforcement(t, database)
	if err := db.ValidateCurrentStorage(ctx, database, vault); err != nil {
		t.Fatal(err)
	}
}

// Snapshot complete rows, including opaque IDs, blobs and high-water marks.
// schema_state is the only domain row intentionally changed by this migration.
func gatewayRows(t *testing.T, database *sql.DB) map[string][]string {
	t.Helper()
	rows, err := database.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND name<>'schema_state' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	result := make(map[string][]string, len(tables))
	for _, table := range tables {
		rows, err := database.Query("SELECT * FROM " + quoteSQLiteIdentifier(table))
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		result[table] = []string{}
		for rows.Next() {
			values, pointers := make([]any, len(columns)), make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			result[table] = append(result[table], string(encoded))
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		sort.Strings(result[table])
	}
	return result
}

func seedGatewayRows(t *testing.T, database *sql.DB, vault *secret.Vault, owner, id int64, kind string) {
	t.Helper()
	const base = "https://gateway.example/v1"
	opaque := func(prefix string) string {
		value, err := db.GenerateOpaqueID(prefix)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	channel, request, claim, report, target := opaque("mch_"), opaque("req_"), opaque("clm_"), opaque("rpc_"), opaque("rpt_")
	contextID := bytes.Repeat([]byte{byte(id)}, 16)
	credential, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := vault.SealForGenerationTwoContext([]byte("synthetic-gateway-credential"), credential)
	if err != nil {
		t.Fatal(err)
	}
	zero, one, fingerprint := make([]byte, 16), make([]byte, 16), bytes.Repeat([]byte{byte(id)}, 32)
	one[15] = 1
	mustExec(t, database, `INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at) VALUES(?,'gateway','api_platform',?,?,1,'active',1,1,1)`, channel, kind, base)
	mustExec(t, database, `INSERT INTO endpoints(id,user_id,connector_type,base_url,note,created_at,updated_at) VALUES(?,?,?,?,'preserve endpoint',1,1)`, id, owner, kind, base)
	mustExec(t, database, `INSERT INTO endpoint_key_secrets(id,context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,?,?,?,?,1)`, id, contextID, base, kind, envelope)
	mustExec(t, database, `INSERT INTO endpoint_keys(id,endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,force_store_false,revision,created_at,updated_at) VALUES(?,?,?,?, 'head','tail','preserve key',1,0,1,1,1)`, id, id, id, fingerprint)
	mustExec(t, database, `INSERT INTO donations(id,user_id,status,revision,description,review_note,created_at,updated_at) VALUES(?,?,'pending',1,'preserve donation','',1,1)`, id, owner)
	mustExec(t, database, `INSERT INTO donation_keys(id,donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,failure_streak,streak_generation,next_claim_seq,next_fold_seq,safe_note,created_at,updated_at,source_endpoint_key_id,report_fingerprint) VALUES(?,?,?,'head','tail',?,?,?,?,?,?,?,?,?,?,?,?,'',1,1,?,?)`, id, id, id, base, kind, zero, zero, zero, zero, zero, zero, zero, one, one, zero, id, fingerprint)
	mustExec(t, database, `INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,caller_error_code,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at) VALUES(?,?,'model_discovery','gateway/model','terminal',1,'failed',503,'upstream','none','user',?,1,2)`, request, owner, zero)
	mustExec(t, database, `INSERT INTO request_logs(id,logical_request_id,user_id,route_kind,started_at,completed_at,caller_result_class,caller_status,caller_error_code,attempt_count,status_code,error_code) VALUES(?,?,?,'model_discovery',1,2,'failed',503,'upstream',1,503,'upstream')`, id, request, owner)
	mustExec(t, database, `INSERT INTO request_attempts(claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,result_kind,upstream_status,input_tokens,cache_write_input_tokens,cache_read_input_tokens,output_tokens,usage_unknown,started_at,completed_at) VALUES(?,?,1,?,?,'model','response',503,0,0,0,0,0,1,2)`, claim, id, kind, base)
	mustExec(t, database, `INSERT INTO report_cases(id,fingerprint,connector_type,canonical_base_url,status,progress_state,material_version,target_version,deadline,material_count,target_count,distinct_owner_count,created_at) VALUES(?,?,?,?,'pending_review','complete',1,1,1000,0,0,0,1)`, report, fingerprint, kind, base)
	mustExec(t, database, `INSERT INTO report_targets(id,case_id,target_seq,endpoint_key_id,source_endpoint_key_id,key_ref,owner_user_id,connector_type,canonical_base_url,state,discovered_version,created_at,updated_at) VALUES(?,?,1,?,?,?,?,?,?,'protected',1,1,1)`, target, report, id, id, fingerprint, owner, kind, base)
}
