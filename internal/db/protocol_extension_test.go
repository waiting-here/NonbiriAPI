package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestProtocolExtensionPreservesSourceAndAllowsExplicitTypes(t *testing.T) {
	database := openSchema17DDLForTest(t)
	_, err := database.Exec(`INSERT INTO users(id,discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
VALUES(1,'test-user','tester',zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),1,1);
INSERT INTO endpoints(id,user_id,connector_type,base_url,note,created_at,updated_at) VALUES(1,1,'openai-compatible','https://example.com/v1','old endpoint',1,1);
INSERT INTO models(id,user_id,provider,model,full_name,created_at,updated_at) VALUES(1,1,'p','m','p/m',1,1);`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	before, err := readGenerationManifest(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := runGenerationTwoExtension(ctx, database, func(ctx context.Context, tx *sql.Tx) error {
		if err := extendContentProtocols(ctx, tx); err != nil {
			return err
		}
		return errors.New("rollback probe")
	}); err == nil {
		t.Fatal("expected rollback")
	}
	after, err := readGenerationManifest(ctx, database)
	if err != nil || generationManifestDigest(after) != generationManifestDigest(before) {
		t.Fatal("migration rollback changed source", err)
	}
	if err := runGenerationTwoExtension(ctx, database, extendContentProtocols); err != nil {
		t.Fatal(err)
	}
	after, err = readGenerationManifest(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	prior := map[string]schemaObject{}
	for _, object := range before.Objects {
		prior[object.Type+"/"+object.Name] = object
	}
	changed := 0
	for _, object := range after.Objects {
		old, ok := prior[object.Type+"/"+object.Name]
		if !ok {
			t.Fatalf("unexpected new object %s", object.Name)
		}
		if object.SQL != old.SQL {
			changed++
			if object.Type != "table" && !strings.Contains(object.Name, "_rejection_") {
				t.Fatalf("unrelated DDL changed: %s", object.Name)
			}
		}
	}
	if changed != 15 || len(after.Objects) != len(before.Objects) {
		t.Fatalf("changed=%d objects %d/%d", changed, len(before.Objects), len(after.Objects))
	}
	for _, kind := range []string{"google-gemini", "openai-responses", "openai-decisions"} {
		if _, err := database.Exec(`INSERT INTO endpoints(user_id,connector_type,base_url,created_at,updated_at) VALUES(1,?,'https://example.com/v1',1,1)`, kind); err != nil {
			t.Fatal(kind, err)
		}
	}
	if _, err := database.Exec(`UPDATE models SET model_types=15 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE models SET model_types=16 WHERE id=1`); err == nil {
		t.Fatal("unknown model type accepted")
	}
	if _, err := database.Exec(`INSERT INTO endpoints(user_id,connector_type,base_url,created_at,updated_at) VALUES(1,'unknown','https://example.com/v1',1,1)`); err == nil {
		t.Fatal("unknown connector accepted")
	}
	if _, err := database.Exec(`UPDATE endpoints SET connector_type='openai-decisions' WHERE id=1`); err == nil {
		t.Fatal("identity trigger lost")
	}
	var count int
	var note string
	if err := database.QueryRow(`SELECT count(*) FROM endpoints WHERE base_url='https://example.com/v1'`).Scan(&count); err != nil || count != 4 {
		t.Fatal("same URL protocol coexistence failed", err, count)
	}
	if err := database.QueryRow(`SELECT note FROM endpoints WHERE id=1`).Scan(&note); err != nil || note != "old endpoint" {
		t.Fatal("old resource changed", err, note)
	}
	for _, kind := range []string{"openai_decisions", "charity_decisions"} {
		id, err := GenerateOpaqueID("req_")
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.Exec(`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,caller_error_code,accounting_state,account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at,terminal_at,rejection_stage,rejection_reason,request_method,request_path)
VALUES(?,1,?,'p/m','terminal',1,'failed',400,'invalid_request','none',0,'user',zeroblob(16),1,1,'preflight','invalid_request','POST','/v1/decisions')`, id, kind)
		if err != nil {
			t.Fatal(kind, err)
		}
	}
}
