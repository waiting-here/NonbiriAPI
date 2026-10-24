package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"modernc.org/sqlite"
)

var endpointKeyHistoryIndexes = []string{
	"idx_request_logs_endpoint_key", "idx_dispatch_claims_endpoint_key",
	"idx_report_targets_endpoint_key", "idx_donation_keys_endpoint_key",
}

func endpointKeyDeletePlan(t *testing.T, database *sql.DB, query string) string {
	t.Helper()
	rows, err := database.Query("EXPLAIN QUERY PLAN "+query, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(details, "\n")
}

func assertEndpointKeyDeletionIndexes(t *testing.T, database *sql.DB) {
	t.Helper()
	plan := endpointKeyDeletePlan(t, database, "DELETE FROM endpoint_keys WHERE id=?")
	for _, index := range endpointKeyHistoryIndexes {
		if !strings.Contains(plan, index) {
			t.Fatalf("endpoint-key deletion does not use %s:\n%s", index, plan)
		}
	}
	if strings.Contains(plan, "SCAN ") {
		t.Fatalf("endpoint-key deletion scans unrelated rows:\n%s", plan)
	}
	// These tables are deleted by the key's cascades or deletion triggers.
	for _, query := range []string{
		"DELETE FROM endpoint_key_grant_revisions WHERE endpoint_key_id=?",
		"DELETE FROM model_binding_targets WHERE endpoint_key_id=?",
		"DELETE FROM charity_binding_targets WHERE donation_key_id=?",
		"DELETE FROM donation_service_health WHERE endpoint_key_id=?",
	} {
		plan := endpointKeyDeletePlan(t, database, query)
		if strings.Contains(plan, "SCAN ") {
			t.Fatalf("key deletion cascade scans unrelated rows (%s):\n%s", query, plan)
		}
	}
}

func TestEndpointKeyDeletionUsesHistoryIndexes(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	assertEndpointKeyDeletionIndexes(t, database)
}

func schema33Fixture(t *testing.T) (string, *secret.Vault, *Store) {
	t.Helper()
	path, vault := bootstrapTestPath(t, "schema33.sqlite"), bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	restoreSchema35ForTest(t, store)
	for _, index := range endpointKeyHistoryIndexes {
		hostileMustExec(t, store.DB(), "DROP INDEX "+index)
	}
	hostileMustExec(t, store.DB(), "DROP TRIGGER discovery_run_version_guard")
	hostileMustExec(t, store.DB(), previousDiscoveryRunGuardSQL)
	hostileMustExec(t, store.DB(), "DROP VIEW charity_model_catalog")
	hostileMustExec(t, store.DB(), previousCharityCatalogSQL)
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=33 WHERE id=1")
	if hash, err := SchemaManifestHash(t.Context(), store.DB()); err != nil || hash != Schema33ManifestHash {
		t.Fatalf("fixture does not match the published schema33: %s, %v", hash, err)
	}
	return path, vault, store
}

func seedEndpointKeyHistory(t *testing.T, database *sql.DB, vault *secret.Vault, marker byte) int64 {
	t.Helper()
	user := hostileInsertUser(t, database, string(marker), 0, 0)
	endpoint := hostileInsertEndpoint(t, database, user, "https://upstream.example/v1")
	contextID := hostileBlob16(marker)
	credentialContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := vault.SealForGenerationTwoContext([]byte("fixture-credential"), credentialContext)
	if err != nil {
		t.Fatal(err)
	}
	secretID := hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,'https://upstream.example/v1','openai-compatible',?,0)`, contextID, envelope))
	key := hostileInsertEndpointKey(t, database, endpoint, secretID)
	request := hostileOIDVariant("req_", marker, 'Q')
	hostileInsertLogicalRequest(t, database, request, user, "openai_chat_completions", 2)
	log := hostileInsertRequestLog(t, database, request, user, "openai_chat_completions")
	hostileMustExec(t, database, "UPDATE request_logs SET endpoint_key_id=? WHERE id=?", key, log)
	for i, state := range []string{"committed", "released"} {
		claim := hostileOIDVariant("clm_", marker+byte(i), 'Q')
		var dispatchedAt any
		if state == "committed" {
			dispatchedAt = 1
		}
		hostileMustExec(t, database, `INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,endpoint_key_id,claim_now,state,dispatched_at,terminal_at,donor_reward_state,streak_disposition,failure_origin) VALUES(?,?,?,'self',?,0,?,?,2,'not_applicable','neutral','platform')`, claim, request, i+1, key, state, dispatchedAt)
		hostileInsertAttempt(t, database, claim, log, int64(i+1))
		hostileMustExec(t, database, "UPDATE request_attempts SET endpoint_key_id_snapshot=? WHERE claim_id=?", key, claim)
	}
	hostileMustExec(t, database, "UPDATE logical_requests SET state='terminal',caller_result_class='success',caller_status=200,terminal_at=2,ledger_rows_remaining=zeroblob(16) WHERE id=?", request)
	donation := hostileInsertDonation(t, database, user)
	donationKey := hostileInsertDonationKey(t, database, donation, key)
	hostileMustExec(t, database, "UPDATE donation_keys SET enabled=0,ended_at=2,ended_reason='member_removed',report_match_until=7776002,updated_at=2 WHERE id=?", donationKey)
	caseID := hostileOIDVariant("rpc_", marker, 'Q')
	hostileInsertReportCase(t, database, caseID, hostileBlob32(marker), "pending_review", "complete", 0)
	hostileMustExec(t, database, `INSERT INTO report_targets(id,case_id,target_seq,endpoint_key_id,source_endpoint_key_id,key_ref,connector_type,canonical_base_url,key_display_head,key_display_tail,state,discovered_version,created_at,updated_at) VALUES(?,?,0,?,?,?,'openai-compatible','https://upstream.example/v1','head','tail','protected',1,0,0)`, hostileOIDVariant("rpt_", marker, 'Q'), caseID, key, key, hostileBlob32(marker))
	return key
}

func endpointKeyHistoryDigests(t *testing.T, database *sql.DB) map[string]interactionTableDigest {
	t.Helper()
	manifest, err := readGenerationManifest(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"request_logs": true, "request_attempts": true, "dispatch_claims": true, "report_targets": true, "donation_keys": true, "endpoint_keys": true, "endpoint_key_grant_revisions": true, "endpoint_key_secrets": true, "credentials": true, "credential_versions": true}
	var selected generationManifest
	for _, table := range manifest.Tables {
		if wanted[table.Name] {
			selected.Tables = append(selected.Tables, table)
		}
	}
	return interactionTableDigests(t, database, selected)
}

func TestSchema33StartupUpgradePreservesHistoryAndDeletion(t *testing.T) {
	path, vault, source := schema33Fixture(t)
	key := seedEndpointKeyHistory(t, source.DB(), vault, 'A')
	other := seedEndpointKeyHistory(t, source.DB(), vault, 'C')
	before := endpointKeyHistoryDigests(t, source.DB())
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	{
		store, err := Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		assertEndpointKeyDeletionIndexes(t, store.DB())
		if after := endpointKeyHistoryDigests(t, store.DB()); !reflect.DeepEqual(before, after) {
			t.Fatal("index upgrade or reopen changed preserved rows")
		}
		if version, err := readSchemaVersion(t.Context(), store.DB()); err != nil || version != CurrentSchemaVersion {
			t.Fatalf("schema version=%d, err=%v", version, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	assertEndpointKeyDeletionIndexes(t, store.DB())
	if after := endpointKeyHistoryDigests(t, store.DB()); !reflect.DeepEqual(before, after) {
		t.Fatal("current-schema reopen changed preserved rows")
	}
	hostileMustExec(t, store.DB(), "DELETE FROM endpoint_keys WHERE id=?", key)
	for table, total := range map[string]int{"request_logs": 2, "dispatch_claims": 4, "report_targets": 2, "donation_keys": 2} {
		if got := countRows(t, store, "SELECT count(*) FROM "+table); got != total {
			t.Fatalf("%s lost history: %d", table, got)
		}
		if got := countRows(t, store, "SELECT count(*) FROM "+table+" WHERE endpoint_key_id IS NULL"); got != total/2 {
			t.Fatalf("%s did not detach matching history: %d", table, got)
		}
		if got := countRows(t, store, "SELECT count(*) FROM "+table+" WHERE endpoint_key_id=?", other); got != total/2 {
			t.Fatalf("%s changed unrelated history: %d", table, got)
		}
	}
	if countRows(t, store, "SELECT count(*) FROM endpoint_key_grant_revisions WHERE endpoint_key_id=?", key) != 0 || countRows(t, store, "SELECT count(*) FROM endpoint_key_grant_revisions WHERE endpoint_key_id=?", other) != 1 {
		t.Fatal("grant cascade changed unrelated grants or retained the deleted key")
	}
	if countRows(t, store, "SELECT count(*) FROM request_attempts WHERE endpoint_key_id_snapshot=?", key) != 2 || countRows(t, store, "SELECT count(*) FROM donation_keys WHERE source_endpoint_key_id=? AND ended_at=2 AND report_match_until=7776002 AND report_fingerprint IS NOT NULL AND display_head='head'", key) != 1 {
		t.Fatal("deletion changed attempt snapshots or donation tombstones")
	}
	if err := foreignKeyCheck(t.Context(), store.DB()); err != nil {
		t.Fatal(err)
	}
}

type cancelIndexUpgradeCodec struct {
	*secret.Vault
	cancel context.CancelFunc
	checks int
}

func (codec *cancelIndexUpgradeCodec) ValidateDatabaseMaterial(value []byte) error {
	if err := codec.Vault.ValidateDatabaseMaterial(value); err != nil {
		return err
	}
	codec.checks++
	// Read-only preflight, transaction source, then post-DDL target validation.
	if codec.checks == 3 {
		codec.cancel()
	}
	return nil
}

var indexCancellationFunctionID atomic.Uint64

func TestSchema33StartupUpgradeRollsBackAndRetries(t *testing.T) {
	for _, failure := range []string{"partial-ddl", "target-mismatch", "cancel-during-index", "cancel-before-commit"} {
		t.Run(failure, func(t *testing.T) {
			testSchema33StartupUpgradeRollback(t, failure)
		})
	}
}

func testSchema33StartupUpgradeRollback(t *testing.T, failure string) {
	t.Helper()
	path, vault, source := schema33Fixture(t)
	seedEndpointKeyHistory(t, source.DB(), vault, 'A')
	before := endpointKeyHistoryDigests(t, source.DB())
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	original := startupSchemaUpgradeSQL
	t.Cleanup(func() { startupSchemaUpgradeSQL = original })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var codec secret.GenerationTwoContextCodec = vault
	var indexedRows atomic.Int64
	switch failure {
	case "partial-ddl":
		startupSchemaUpgradeSQL = strings.Split(original, ";")[0] + "; SELECT missing_migration_function();"
	case "target-mismatch":
		startupSchemaUpgradeSQL = strings.Split(original, ";")[0] + ";"
	case "cancel-during-index":
		function := fmt.Sprintf("cancel_endpoint_key_index_%d", indexCancellationFunctionID.Add(1))
		if err := sqlite.RegisterDeterministicScalarFunction(function, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			indexedRows.Add(1)
			cancel()
			runtime.Gosched()
			return args[0], nil
		}); err != nil {
			t.Fatal(err)
		}
		// The first real index has already been created when SQLite evaluates
		// this expression while constructing the next index from stored rows.
		startupSchemaUpgradeSQL = strings.Split(original, ";")[0] + "; CREATE INDEX interrupted_history_index ON request_logs(" + function + "(endpoint_key_id));"
	case "cancel-before-commit":
		codec = &cancelIndexUpgradeCodec{Vault: vault, cancel: cancel}
	}
	store, err := OpenContext(ctx, path, codec)
	if store != nil {
		store.Close()
		t.Fatal("partial migration succeeded")
	}
	var startup *StartupError
	cancelled := strings.HasPrefix(failure, "cancel-")
	if cancelled && !errors.Is(err, context.Canceled) || !cancelled && (!errors.As(err, &startup) || startup.Kind != StartupSchemaMismatch) {
		t.Fatalf("unexpected migration error: %v", err)
	}
	if failure == "cancel-during-index" && indexedRows.Load() == 0 {
		t.Fatal("cancellation did not occur during index construction")
	}
	if err := InspectContext(t.Context(), path, vault, func(ctx context.Context, read *sql.DB) error {
		version, err := readSchemaVersion(ctx, read)
		if err != nil || version != 33 {
			t.Fatalf("failed migration version=%d, err=%v", version, err)
		}
		hash, err := SchemaManifestHash(ctx, read)
		if err != nil || hash != Schema33ManifestHash {
			t.Fatalf("partial DDL survived rollback: %s, %v", hash, err)
		}
		if !reflect.DeepEqual(before, endpointKeyHistoryDigests(t, read)) {
			t.Fatal("failed upgrade changed preserved rows")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	startupSchemaUpgradeSQL = original
	store, err = Open(path, vault)
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	store.Close()
}

func TestSchema33StartupUpgradeRejectsUnknownOrModifiedSources(t *testing.T) {
	for _, change := range []string{
		"UPDATE schema_state SET version=36", "UPDATE schema_state SET version=32",
		"DROP INDEX idx_users_created", "CREATE INDEX unexpected_index ON users(username)",
	} {
		t.Run(change, func(t *testing.T) {
			path, vault, source := schema33Fixture(t)
			hostileMustExec(t, source.DB(), change)
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			before := snapshotBootstrapSources(t, path)
			store, err := Open(path, vault)
			if store != nil {
				store.Close()
				t.Fatal("unrecognized source accepted")
			}
			var startup *StartupError
			if !errors.As(err, &startup) || startup.Kind != StartupSchemaMismatch {
				t.Fatalf("unexpected source rejection: %v", err)
			}
			assertBootstrapSourcesUnchanged(t, path, before)
		})
	}
}
