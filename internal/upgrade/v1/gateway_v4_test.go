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

func TestConnectionsUpgradePreservesPublishedHistoryAndCredentials(t *testing.T) {
	ctx := context.Background()
	path, vault := sourceFixtureFromDDL(t, schema26SQLForTest(t), 26, stableSchema26Manifest)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	mustExec(t, database, "PRAGMA foreign_keys=ON")
	plan, err := storageSchema.plan(ctx, database)
	if err != nil || len(plan.steps) != 5 || plan.steps[0].version != 33 || plan.steps[1].version != 34 || plan.steps[2].version != 35 || plan.steps[3].version != 36 || plan.steps[4].version != db.CurrentSchemaVersion || !plan.auditsCredentials() {
		t.Fatalf("connection migration plan: %+v, %v", plan, err)
	}
	owner := hostileInsertUser(t, database, "connection-owner", 0, 1)
	for i, kind := range []string{"openai-compatible", "openai-responses", "openai-decisions", "anthropic-compatible", "google-gemini", "ai-sdk-gateway-v3"} {
		seedGatewayRows(t, database, vault, owner, int64(i+1), kind)
		mustExec(t, database, "INSERT INTO model_discovery_evidence(endpoint_key_id,state,revision) VALUES(?,'unknown',1)", i+1)
	}
	mustExec(t, database, "UPDATE sqlite_sequence SET seq=9000 WHERE name='endpoints'")
	beforeRows := gatewayRows(t, database)
	columns := legacyConnectionColumns(t, database)
	beforeDDL, err := readGenerationManifest(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	probe := errors.New("rollback after connection relationship conversion")
	if err := runGenerationTwoExtension(ctx, database, func(ctx context.Context, tx *sql.Tx) error {
		if err := extendConnections(ctx, tx); err != nil {
			return err
		}
		return probe
	}); !errors.Is(err, probe) {
		t.Fatalf("rollback error = %v", err)
	}
	afterDDL, err := readGenerationManifest(ctx, database)
	if err != nil || generationManifestDigest(beforeDDL) != generationManifestDigest(afterDDL) || !reflect.DeepEqual(gatewayRows(t, database), beforeRows) {
		t.Fatalf("rollback changed published source: %v", err)
	}
	assertForeignKeyEnforcement(t, database)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := Run(ctx, path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" || result.SourceSchemaVersion != 26 || result.TargetSchemaVersion != db.CurrentSchemaVersion {
		t.Fatalf("upgrade = %+v, %v", result, err)
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	database = store.DB()
	assertPublishedConnectionRows(t, database, columns, beforeRows)
	for id, want := range []int64{49, 2, 8192, 65536, 131072, 262144} {
		var endpointMask, grantMask, version, grantRevision int64
		if err := database.QueryRow(`SELECT e.native_service_mask,g.native_service_mask,v.version,g.grant_revision
   FROM endpoints e JOIN endpoint_keys k ON k.endpoint_id=e.id JOIN endpoint_key_grant_revisions g ON g.id=k.current_grant_revision_id
   JOIN credential_versions v ON v.id=g.credential_version_id WHERE e.id=?`, id+1).Scan(&endpointMask, &grantMask, &version, &grantRevision); err != nil || endpointMask != want || grantMask != want || version != 1 || grantRevision != 1 {
			t.Fatalf("legacy permission %d: %d/%d v%d g%d: %v", id+1, endpointMask, grantMask, version, grantRevision, err)
		}
	}
	if _, err := database.Exec(`UPDATE endpoints SET connector_type='ai-sdk-gateway-v4' WHERE id=6`); err == nil {
		t.Fatal("existing endpoint protocol became mutable")
	}
	if _, err := database.Exec(`UPDATE endpoint_key_secrets SET connector_type='ai-sdk-gateway-v4' WHERE id=6`); err == nil {
		t.Fatal("existing encrypted origin became mutable")
	}
	assertForeignKeyEnforcement(t, database)
	if err := db.ValidateCurrentStorage(ctx, database, vault); err != nil {
		t.Fatal(err)
	}
}

func legacyConnectionColumns(t *testing.T, database *sql.DB) map[string][]string {
	t.Helper()
	rows, err := database.Query("SELECT name FROM sqlite_schema WHERE type='table' AND name<>'schema_state' ORDER BY name")
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
	result := map[string][]string{}
	for _, table := range tables {
		rows, err := database.Query("SELECT * FROM " + quoteSQLiteIdentifier(table) + " LIMIT 0")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if closeErr := rows.Close(); err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
		result[table] = columns
	}
	return result
}

func assertPublishedConnectionRows(t *testing.T, database *sql.DB, columns map[string][]string, want map[string][]string) {
	t.Helper()
	for table, names := range columns {
		from := quoteSQLiteIdentifier(table) + " b"
		switch table {
		case "endpoint_keys":
			from = "endpoint_key_current_credentials b"
		case "model_catalog_entries":
			from = "endpoint_key_catalog_entries b"
		case "model_bindings":
			from = "model_bindings b JOIN model_binding_primary_targets t ON t.binding_id=b.id"
		case "charity_model_bindings":
			from = "charity_model_bindings b JOIN charity_binding_primary_targets t ON t.binding_id=b.id JOIN donation_keys dk ON dk.id=t.donation_key_id"
		case "mainstream_channels":
			from = "mainstream_channels b JOIN mainstream_channel_primary_entries t ON t.channel_id=b.id"
		}
		selected := make([]string, len(names))
		for i, name := range names {
			alias := "b"
			if table == "model_bindings" && (name == "endpoint_key_id" || name == "upstream_model_id") || table == "charity_model_bindings" && (name == "donation_key_id" || name == "upstream_model_id") || table == "mainstream_channels" && (name == "connector_type" || name == "canonical_base_url") {
				alias = "t"
			}
			if table == "charity_model_bindings" && name == "endpoint_key_id" {
				alias = "dk"
			}
			selected[i] = alias + "." + quoteSQLiteIdentifier(name)
		}
		query := "SELECT " + strings.Join(selected, ",") + " FROM " + from
		rows, err := database.Query(query)
		if err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		got := []string{}
		knownSequence := map[string]bool{}
		if table == "sqlite_sequence" {
			for _, raw := range want[table] {
				var values []any
				if err := json.Unmarshal([]byte(raw), &values); err != nil {
					t.Fatal(err)
				}
				knownSequence[values[0].(string)] = true
			}
		}
		for rows.Next() {
			values, pointers := make([]any, len(names)), make([]any, len(names))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			if table == "sqlite_sequence" && !knownSequence[values[0].(string)] {
				continue
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			if table == "site_config" && (string(encoded) == `["default_light_rpm_per_user","30",0]` || string(encoded) == `["artwork_r18_disabled","1",0]`) {
				found := false
				for _, old := range want[table] {
					if old == string(encoded) {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}
			got = append(got, string(encoded))
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want[table]) {
			t.Errorf("published columns changed for %s:\n got %v\nwant %v", table, got, want[table])
		}
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
