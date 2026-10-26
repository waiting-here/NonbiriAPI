package v1

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"modernc.org/sqlite"
)

func bootstrapTestPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}
func bootstrapTestVault(t *testing.T) *secret.Vault { t.Helper(); return testVault(t, 0x29) }
func foreignKeyCheck(ctx context.Context, q queryer) error {
	return checkForeignKeys(ctx, q, "PRAGMA foreign_key_check")
}
func assertRetainedManifest(t *testing.T, database *sql.DB, want string) {
	t.Helper()
	hash, err := db.SchemaManifestHash(t.Context(), database)
	if err != nil || hash != want {
		t.Fatal(hash, err)
	}
}
func explicitUpgrade(t *testing.T, path string, vault *secret.Vault) {
	t.Helper()
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.TargetSchemaVersion != db.CurrentSchemaVersion {
		t.Fatal(result, err)
	}
}
func upgradeWithCodec(ctx context.Context, path string, codec secret.GenerationTwoContextCodec) error {
	sources := []OfflineSchema{}
	for _, step := range storageSchema.versions {
		if step.version < db.CurrentSchemaVersion {
			sources = append(sources, OfflineSchema{Version: step.version, ManifestHash: step.manifest})
		}
	}
	_, err := convertOffline(ctx, path, codec, sources, true, true, nil, func(context.Context, *sql.DB, *sql.Tx) error { return nil })
	return err
}
func migrationIndex(t *testing.T, from int) int {
	t.Helper()
	for i, step := range storageSchema.versions {
		if step.from == from {
			return i
		}
	}
	t.Fatal("missing migration", from)
	return -1
}
func oldStewardPolicyForTest(t *testing.T) string {
	t.Helper()
	grants := authz.DefaultStewardPermissions(5).Permissions
	for _, key := range []string{"ai.chat", "ai.review.read", "ai.memory.edit"} {
		delete(grants, key)
	}
	i := 0
	for key := range grants {
		grants[key] = i%2 == 0
		i++
	}
	body, err := json.Marshal(grants)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func countRows(t *testing.T, st *db.Store, query string, args ...any) int {
	t.Helper()
	var count int
	if err := st.DB().QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

func hostileOIDVariant(prefix string, marker byte, tail byte) string {
	body := []byte(strings.Repeat("A", 22))
	body[0] = marker
	body[len(body)-1] = tail
	return prefix + string(body)
}

func hostileInsertEndpoint(t *testing.T, db *sql.DB, userID int64, base string) int64 {
	t.Helper()
	result := hostileMustExec(t, db, `
INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at,native_service_mask,capability_origin)
VALUES(?,'openai-compatible',?,'',1,1,0,0,49,'legacy')`, userID, base)
	return hostileMustLastID(t, result)
}

func hostileInsertEndpointKey(t *testing.T, database *sql.DB, endpointID, secretID int64) int64 {
	t.Helper()
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	key, err := testseed.InsertEndpointKeyTx(t.Context(), tx, testseed.KeyInput{EndpointID: endpointID, SecretRefID: secretID, Fingerprint: hostileBlob32(1), DisplayHead: "head", DisplayTail: "tail", At: 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return key.KeyID
}

func hostileInsertLogicalRequest(t *testing.T, db *sql.DB, id string, userID any, route string, limit int64) {
	t.Helper()
	remaining := hostileBlob16(1)
	if route == "charity_chat_completions" {
		remaining = hostileBlob16(byte(limit + 1))
	} else if route == "model_discovery" {
		remaining = hostileBlob16(0)
	}
	hostileMustExec(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,model_snapshot,state,attempt_limit,
 accounting_state,account_reserved_milli,settlement_destination,
 ledger_rows_remaining,created_at
) VALUES(?,?,?,'model','accepted',?,'none',0,'user',?,0)`,
		id, userID, route, limit, remaining)
}

func hostileInsertReportCase(t *testing.T, db *sql.DB, id string, fingerprint []byte, status, progress string, at int64) {
	t.Helper()
	var terminalAt any
	if status == "approved" || status == "rejected" {
		terminalAt = at + 1
	}
	hostileMustExec(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_source,cursor_id,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 retry_attempt_count,created_at,terminal_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1',?,?,1,1,1000,NULL,NULL,0,0,0,0,0,0,0,?,?)`,
		id, fingerprint, status, progress, at, terminalAt)
}

func hostileInsertDonation(t *testing.T, db *sql.DB, userID any) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "donations")
	result := hostileMustExec(t, db, `
INSERT INTO donations(id,user_id,status,revision,description,review_note,created_at,updated_at)
VALUES(?,?,'pending',1,'','',0,0)`, id, userID)
	return hostileMustLastID(t, result)
}

func hostileInsertDonationKey(t *testing.T, db *sql.DB, donationID int64, endpointKeyID any) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "donation_keys")
	zero := hostileBlob16(0)
	one := hostileBlob16(1)
	sourceKeyID := int64(0)
	if raw, ok := endpointKeyID.(int64); ok && raw > 0 {
		sourceKeyID = raw
	} else {
		// Tombstone rows keep the original physical key identity without an FK;
		// any positive value satisfies the NOT NULL boundary in this fixture.
		sourceKeyID = hostileNextPK64(t, db, "endpoint_keys")
	}
	result := hostileMustExec(t, db, `
INSERT INTO donation_keys(
 id,donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,
 price_limit_mag,call_limit_mag,token_limit_mag,price_used_mag,price_reserved_mag,
 calls_used,calls_reserved,tokens_used,tokens_reserved,token_reserve,enabled,failure_disabled,
 failure_streak,streak_generation,next_claim_seq,next_fold_seq,safe_note,created_at,updated_at,
 authorized_expires_at,expires_at,source_endpoint_key_id,report_fingerprint
) VALUES(?,?,?,'head','tail','https://upstream.example/v1','openai-compatible',NULL,NULL,NULL,?,?,?,?,?, ?,0,1,0,?,?,?,?,'',0,0,NULL,NULL,?,?)`,
		id, donationID, endpointKeyID, zero, zero, zero, zero, zero, zero, zero, one, one, zero, sourceKeyID, hostileBlob32(7))
	return hostileMustLastID(t, result)
}

func hostileInsertRequestLog(t *testing.T, db *sql.DB, reqID string, userID any, route string) int64 {
	t.Helper()
	result := hostileMustExec(t, db, `
INSERT INTO request_logs(
 logical_request_id,user_id,model,upstream_model_id,route_kind,endpoint_base_url,
 started_at
) VALUES(?,?, 'model','upstream',?,'https://upstream.example/v1',0)`, reqID, userID, route)
	return hostileMustLastID(t, result)
}

func hostileInsertAttempt(t *testing.T, db *sql.DB, claimID string, logID int64, seq int64) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO request_attempts(
 claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,
 result_kind,upstream_status,input_tokens,cache_write_input_tokens,
 cache_read_input_tokens,output_tokens,usage_unknown,started_at,completed_at
) VALUES(?,?,?,'openai-compatible','https://upstream.example/v1','upstream',
 'response',200,0,0,0,0,0,0,1)`, claimID, logID, seq)
}

func restoreSchema36LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	dropCharityModelIndexesForTest(t, database)
	for _, table := range []string{"ai_agent_plans", "ai_agent_model_results", "ai_agent_review_events", "ai_agent_jobs", "ai_agent_messages", "ai_agent_conversations", "ai_agent_memory_history", "ai_agent_memories", "ai_agent_settings", "agent_request_response_chunks", "agent_request_settings", "agent_requests", "agent_request_usage", "agent_request_usage_totals", "agent_request_usage_hours"} {
		hostileMustExec(t, database, "DROP TABLE "+table)
	}
}

func restoreSchema35ForTest(t *testing.T, store *db.Store) {
	t.Helper()
	restoreSchema36LayoutForTest(t, store.DB())
	old := strings.ReplaceAll(strings.ReplaceAll(db.StewardAuditRolesSQL, "'admin','level5','level6'", "'admin','level6'"), "'admin','steward5','steward6'", "'admin','steward6'")
	hostileMustExec(t, store.DB(), strings.ReplaceAll(old, "level IN (5,6)", "level=6"))
	hostileMustExec(t, store.DB(), strings.ReplaceAll(db.EndpointTagsSQL, ",'user_gateway','steward_gateway','invalid_entry'", ""))
	hostileMustExec(t, store.DB(), `DROP TABLE steward_permission_audits; DROP TABLE steward_permissions; UPDATE schema_state SET version=35 WHERE id=1`)
}

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

func schema33Fixture(t *testing.T) (string, *secret.Vault, *db.Store) {
	t.Helper()
	path, vault := bootstrapTestPath(t, "schema33.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
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
	if hash, err := db.SchemaManifestHash(t.Context(), store.DB()); err != nil || hash != db.Schema33ManifestHash {
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

func endpointKeyHistoryDigests(t *testing.T, database *sql.DB) map[string][]string {
	t.Helper()
	rows := gatewayRows(t, database)
	selected := map[string][]string{}
	for _, name := range []string{"request_logs", "request_attempts", "dispatch_claims", "report_targets", "donation_keys", "endpoint_keys", "endpoint_key_grant_revisions", "endpoint_key_secrets", "credentials", "credential_versions"} {
		selected[name] = rows[name]
	}
	return selected
}

const previousDiscoveryRunGuardSQL = `CREATE TRIGGER discovery_run_version_guard BEFORE INSERT ON discovery_runs
WHEN NEW.authorization_kind<>'legacy' AND NOT EXISTS(SELECT 1 FROM discovery_source_authorizations a WHERE a.source_revision_id=NEW.source_revision_id AND a.source_revision=NEW.source_revision_snapshot AND a.credential_version_id=NEW.credential_version_id AND a.available=1 AND ((NEW.authorization_kind='self' AND EXISTS(SELECT 1 FROM discovery_sources ds WHERE ds.id=a.source_id AND ds.current_revision_id=a.source_revision_id)) OR (NEW.authorization_kind='donation_use' AND EXISTS(SELECT 1 FROM donation_uses u JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id AND ur.use_id=u.id JOIN charity_use_availability av ON av.use_id=u.id WHERE u.id=NEW.authorization_use_id AND ur.id=NEW.authorization_use_revision_id AND ur.discovery_source_revision_id=a.source_revision_id AND u.discovery_enabled=1 AND av.credential_version_id=a.credential_version_id))))
BEGIN SELECT RAISE(ABORT,'discovery run authorization mismatch'); END;`

const previousCharityCatalogSQL = `CREATE VIEW charity_model_catalog AS
 WITH automatic_entries AS (
  SELECT DISTINCT u.donation_key_id,c.id,c.normalized_model_id,r.result_generation
  FROM donation_uses u
  JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id AND ur.use_id=u.id AND ur.review_state='approved'
  JOIN discovery_source_states st ON st.source_revision_id=ur.discovery_source_revision_id
  JOIN discovery_runs r ON r.id=st.last_successful_run_id AND r.source_revision_id=ur.discovery_source_revision_id AND r.state='succeeded'
  JOIN model_catalog_entries c ON c.discovery_run_id=r.id AND c.source_type='automatic'
 ), supports AS (
  SELECT donation_key_id,normalized_model_id,count(*) AS automatic_supports,0 AS manual_supports,max(result_generation) AS automatic_revision
  FROM automatic_entries GROUP BY donation_key_id,normalized_model_id
  UNION ALL
  SELECT donation_key_id,normalized_model_id,0,1,0 FROM donation_key_manual_models
 )
 SELECT donation_key_id,normalized_model_id,sum(automatic_supports) AS automatic_supports,sum(manual_supports) AS manual_supports,max(automatic_revision) AS automatic_revision
 FROM supports GROUP BY donation_key_id,normalized_model_id;`

func schema34CatalogFixture(t *testing.T) (string, *secret.Vault, *db.Store) {
	t.Helper()
	path := bootstrapTestPath(t, "schema34.sqlite")
	vault := bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	restoreSchema35ForTest(t, store)
	hostileMustExec(t, store.DB(), "DROP TRIGGER discovery_run_version_guard")
	hostileMustExec(t, store.DB(), previousDiscoveryRunGuardSQL)
	hostileMustExec(t, store.DB(), "DROP VIEW charity_model_catalog")
	hostileMustExec(t, store.DB(), previousCharityCatalogSQL)
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=34")
	if hash, err := db.SchemaManifestHash(t.Context(), store.DB()); err != nil || hash != db.Schema34ManifestHash {
		t.Fatalf("source identity: %s %v", hash, err)
	}
	return path, vault, store
}

func TestSchema33OfflineUpgradePreservesHistoryAndDeletion(t *testing.T) {
	path, vault, source := schema33Fixture(t)
	key := seedEndpointKeyHistory(t, source.DB(), vault, 'A')
	other := seedEndpointKeyHistory(t, source.DB(), vault, 'C')
	before := endpointKeyHistoryDigests(t, source.DB())
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	explicitUpgrade(t, path, vault)
	{
		store, err := db.Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		assertEndpointKeyDeletionIndexes(t, store.DB())
		if after := endpointKeyHistoryDigests(t, store.DB()); !reflect.DeepEqual(before, after) {
			t.Fatal("index upgrade or reopen changed preserved rows")
		}
		if version, err := readSchemaVersion(t.Context(), store.DB()); err != nil || version != db.CurrentSchemaVersion {
			t.Fatalf("schema version=%d, err=%v", version, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	store, err := db.Open(path, vault)
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

func testSchema33OfflineUpgradeRollback(t *testing.T, failure string) {
	t.Helper()
	path, vault, source := schema33Fixture(t)
	seedEndpointKeyHistory(t, source.DB(), vault, 'A')
	before := endpointKeyHistoryDigests(t, source.DB())
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	index := migrationIndex(t, 33)
	original := storageSchema.versions[index].sql
	t.Cleanup(func() { storageSchema.versions[index].sql = original })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var codec secret.GenerationTwoContextCodec = vault
	var indexedRows atomic.Int64
	switch failure {
	case "partial-ddl":
		storageSchema.versions[index].sql = strings.Split(original, ";")[0] + "; SELECT missing_migration_function();"
	case "target-mismatch":
		storageSchema.versions[index].sql = strings.Split(original, ";")[0] + ";"
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
		storageSchema.versions[index].sql = strings.Split(original, ";")[0] + "; CREATE INDEX interrupted_history_index ON request_logs(" + function + "(endpoint_key_id));"
	case "cancel-before-commit":
		codec = &cancelIndexUpgradeCodec{Vault: vault, cancel: cancel}
	}
	err := upgradeWithCodec(ctx, path, codec)
	if err == nil {
		t.Fatal("partial migration succeeded")
	}
	var startup *db.StartupError
	cancelled := strings.HasPrefix(failure, "cancel-")
	if cancelled && !errors.Is(err, context.Canceled) || !cancelled && (!errors.As(err, &startup) || startup.Kind != db.StartupSchemaMismatch) {
		t.Fatalf("unexpected migration error: %v", err)
	}
	if failure == "cancel-during-index" && indexedRows.Load() == 0 {
		t.Fatal("cancellation did not occur during index construction")
	}
	if err := db.InspectContext(t.Context(), path, vault, func(ctx context.Context, read *sql.DB) error {
		version, err := readSchemaVersion(ctx, read)
		if err != nil || version != 33 {
			t.Fatalf("failed migration version=%d, err=%v", version, err)
		}
		hash, err := db.SchemaManifestHash(ctx, read)
		if err != nil || hash != db.Schema33ManifestHash {
			t.Fatalf("partial DDL survived rollback: %s, %v", hash, err)
		}
		if !reflect.DeepEqual(before, endpointKeyHistoryDigests(t, read)) {
			t.Fatal("failed upgrade changed preserved rows")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	storageSchema.versions[index].sql = original
	explicitUpgrade(t, path, vault)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	store.Close()
}

func TestSchema33OfflineUpgradeRollsBackAndRetries(t *testing.T) {
	for _, failure := range []string{"partial-ddl", "target-mismatch", "cancel-during-index", "cancel-before-commit"} {
		t.Run(failure, func(t *testing.T) {
			testSchema33OfflineUpgradeRollback(t, failure)
		})
	}
}

func TestSchema34OfflineCatalogUpgradePreservesRowsAndRetries(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[fail], func(t *testing.T) {
			path, vault, source := schema34CatalogFixture(t)
			seedEndpointKeyHistory(t, source.DB(), vault, 'A')
			before := endpointKeyHistoryDigests(t, source.DB())
			index := migrationIndex(t, 34)
			original := storageSchema.versions[index].sql
			defer func() { storageSchema.versions[index].sql = original }()
			if fail {
				storageSchema.versions[index].sql = "DROP TRIGGER discovery_run_version_guard; SELECT missing_catalog_migration();"
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			_, err := Run(t.Context(), path, vault, Options{Apply: true})
			if fail {
				if err == nil {
					t.Fatal("partial trigger replacement succeeded")
				}
				raw, openErr := sql.Open("sqlite", path)
				if openErr != nil {
					t.Fatal(openErr)
				}
				if hash, hashErr := db.SchemaManifestHash(t.Context(), raw); hashErr != nil || hash != db.Schema34ManifestHash {
					t.Fatalf("partial DDL survived: %s %v", hash, hashErr)
				}
				raw.Close()
				storageSchema.versions[index].sql = original
				_, err = Run(t.Context(), path, vault, Options{Apply: true})
			}
			if err != nil {
				t.Fatal(err)
			}
			upgraded, err := db.Open(path, vault)
			if err != nil {
				t.Fatal(err)
			}
			defer upgraded.Close()
			if version, err := readSchemaVersion(t.Context(), upgraded.DB()); err != nil || version != db.CurrentSchemaVersion {
				t.Fatalf("target: %d %v", version, err)
			}
			if after := endpointKeyHistoryDigests(t, upgraded.DB()); !reflect.DeepEqual(before, after) {
				t.Fatal("catalog upgrade changed data")
			}
			if err := upgraded.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := db.Open(path, vault)
			if err != nil {
				t.Fatal(err)
			}
			reopened.Close()
		})
	}
}

func TestStewardPermissionOfflineUpgradePreservesExistingAccounts(t *testing.T) {
	path := bootstrapTestPath(t, "steward-permissions.sqlite")
	vault := bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO risk_client_rules(id,name,status,enabled,revision,conditions_json,created_by_role,updated_by_role,created_at,updated_at) VALUES('historical-rule','Historical rule','suspected',1,3,'[]','admin','level6',1,2);
INSERT INTO client_rule_auto_bans(rule_id,enabled,duration_seconds,revision,updated_at) VALUES('historical-rule',1,60,4,2);
INSERT INTO discord_blacklist(discord_id,reason,created_at) VALUES('123456789012345678','Historical restriction',1);
INSERT INTO discord_blacklist_origins(discord_id,first_actor_kind) VALUES('123456789012345678','steward6');
INSERT INTO discord_blacklist_events(id,discord_id,operation_key,actor_kind,reason_codes_json,safe_note,created_at) VALUES(17,'123456789012345678','historical-operation','steward6','[]','Historical note',1);
INSERT INTO admin_endpoint_tags(base_url,tag) VALUES('https://historical.example/v1','community_charity');`); err != nil {
		t.Fatal(err)
	}
	restoreSchema36LayoutForTest(t, store.DB())
	if _, err := store.DB().Exec(strings.ReplaceAll(db.EndpointTagsSQL, ",'user_gateway','steward_gateway','invalid_entry'", "")); err != nil {
		t.Fatal(err)
	}
	oldRoles := strings.ReplaceAll(strings.ReplaceAll(db.StewardAuditRolesSQL, "'admin','level5','level6'", "'admin','level6'"), "'admin','steward5','steward6'", "'admin','steward6'")
	if _, err := store.DB().Exec(strings.ReplaceAll(oldRoles, "level IN (5,6)", "level=6")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`DROP TABLE steward_permission_audits; DROP TABLE steward_permissions; UPDATE schema_state SET version=35 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	explicitUpgrade(t, path, vault)
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version, policies, audits int
	if err := store.DB().QueryRow(`SELECT version,(SELECT COUNT(*) FROM steward_permissions),(SELECT COUNT(*) FROM steward_permission_audits) FROM schema_state WHERE id=1`).Scan(&version, &policies, &audits); err != nil {
		t.Fatal(err)
	}
	if version != db.CurrentSchemaVersion || policies != 0 || audits != 0 {
		t.Fatal("unexpected migration state", version, policies, audits)
	}
	if err := validateGenerationTwoManifest(context.Background(), store.DB()); err != nil {
		t.Fatal(err)
	}
	var preserved bool
	if err := store.DB().QueryRow(`SELECT EXISTS(SELECT 1 FROM risk_client_rules r JOIN client_rule_auto_bans a ON a.rule_id=r.id WHERE r.id='historical-rule' AND r.revision=3 AND r.created_by_role='admin' AND r.updated_by_role='level6' AND r.conditions_json='[]' AND a.revision=4 AND a.duration_seconds=60)
AND EXISTS(SELECT 1 FROM admin_endpoint_tags WHERE base_url='https://historical.example/v1' AND tag='community_charity')
AND EXISTS(SELECT 1 FROM discord_blacklist_origins o JOIN discord_blacklist_events e USING(discord_id) WHERE o.first_actor_kind='steward6' AND e.id=17 AND e.actor_kind='steward6' AND e.operation_key='historical-operation' AND e.reason_codes_json='[]' AND e.safe_note='Historical note')`).Scan(&preserved); err != nil || !preserved {
		t.Fatal("historical audit rows changed", preserved, err)
	}
	rows, err := store.DB().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("migration foreign key violation", rows.Err())
	}
}

func TestAgentPolicyOfflineMigrationPreservesAuthorityAndAudit(t *testing.T) {
	path := bootstrapTestPath(t, "agent-policy.sqlite")
	vault := bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	user := hostileInsertUser(t, store.DB(), "policy-target", 0, 0)
	original := oldStewardPolicyForTest(t)
	hostileMustExec(t, store.DB(), `INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,9,?,'{"saved":"17"}','enhanced',10,20)`, user, original)
	hostileMustExec(t, store.DB(), `INSERT INTO steward_permission_audits(user_id,from_revision,to_revision,previous_policy,next_policy,created_at) VALUES(?,8,9,?,?,20)`, user, original, original)
	restoreSchema36LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=36")
	if hash, err := db.SchemaManifestHash(t.Context(), store.DB()); err != nil || hash != db.Schema36ManifestHash {
		t.Fatal(hash, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	explicitUpgrade(t, path, vault)
	for pass := 0; pass < 2; pass++ {
		store, err = db.Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		var body, limits, scope, previous, next string
		var revision, created, updated int64
		err = store.DB().QueryRow(`SELECT permissions,limits,donation_scope,revision,created_at,updated_at FROM steward_permissions WHERE user_id=?`, user).Scan(&body, &limits, &scope, &revision, &created, &updated)
		if err != nil || revision != 9 || created != 10 || updated != 20 || limits != `{"saved":"17"}` || scope != "enhanced" {
			t.Fatal("policy facts changed", err)
		}
		var before, after map[string]bool
		json.Unmarshal([]byte(original), &before)
		json.Unmarshal([]byte(body), &after)
		if len(after) != 51 {
			t.Fatal("not current", after)
		}
		for key, value := range before {
			if after[key] != value {
				t.Fatal("authority changed", key)
			}
		}
		for _, key := range []string{"ai.chat", "ai.review.read", "ai.memory.edit"} {
			if after[key] {
				t.Fatal("new grant enabled", key)
			}
		}
		if err = store.DB().QueryRow("SELECT previous_policy,next_policy FROM steward_permission_audits WHERE user_id=?", user).Scan(&previous, &next); err != nil || previous != original || next != original {
			t.Fatal("audit rewritten", err)
		}
		if err = store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestModelInterfaceOfflineMigrationPreservesLegacyMeaningOnce(t *testing.T) {
	path := bootstrapTestPath(t, "model-interfaces.sqlite")
	vault := bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	user := hostileInsertUser(t, store.DB(), "model-interface-owner", 0, 0)
	type modelFact struct {
		table, name, fullName string
		id, source, upgraded  int64
	}
	var facts []modelFact
	for i, mask := range []int64{1, 16, 17, 2, 3, 18, 32768, 32769} {
		name := fmt.Sprintf("model%d", i)
		want := mask
		if mask&17 != 0 {
			want |= 17
		}
		personal := hostileMustLastID(t, hostileMustExec(t, store.DB(), `INSERT INTO models(user_id,provider,model,full_name,model_types,revision,binding_revision,created_at,updated_at) VALUES(?,'p',?,?,?,7,11,13,17)`, user, name, "p/"+name, mask))
		charity := hostileMustLastID(t, hostileMustExec(t, store.DB(), `INSERT INTO charity_models(provider,model,full_name,pricing_mode,model_types,revision,binding_revision,created_at,updated_at) VALUES('p',?,?,'per_request',?,7,11,13,17)`, name, "[公益]p/"+name, mask))
		facts = append(facts, modelFact{"models", name, "p/" + name, personal, mask, want}, modelFact{"charity_models", name, "[公益]p/" + name, charity, mask, want})
	}
	restoreSchema36LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=36")
	if hash, err := db.SchemaManifestHash(t.Context(), store.DB()); err != nil || hash != db.Schema36ManifestHash {
		t.Fatal("source identity changed", hash, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	explicitUpgrade(t, path, vault)
	for pass := 0; pass < 2; pass++ {
		store, err = db.Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		for _, fact := range facts {
			want := fact.upgraded
			if pass == 1 {
				want = fact.source
			}
			var mask, revision, binding, created, updated int64
			var name, fullName string
			if err := store.DB().QueryRow("SELECT model_types,revision,binding_revision,created_at,updated_at,model,full_name FROM "+fact.table+" WHERE id=?", fact.id).Scan(&mask, &revision, &binding, &created, &updated, &name, &fullName); err != nil || mask != want || revision != 7 || binding != 11 || created != 13 || updated != 17 || name != fact.name || fullName != fact.fullName {
				t.Fatalf("pass=%d fact=%+v got=%d/%d/%d/%d/%d/%s/%s err=%v", pass, fact, mask, revision, binding, created, updated, name, fullName, err)
			}
			if pass == 0 {
				// Current-schema writes are independent selections, including
				// the same single-bit values that had a shared legacy meaning.
				hostileMustExec(t, store.DB(), "UPDATE "+fact.table+" SET model_types=? WHERE id=?", fact.source, fact.id)
			}
		}
		if version, err := readSchemaVersion(t.Context(), store.DB()); err != nil || version != db.CurrentSchemaVersion {
			t.Fatal(version, err)
		}
		assertRetainedManifest(t, store.DB(), db.PinnedGenerationTwoManifestHash)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAgentPolicyUpgradeFailureRollsBackAndNewerSchemaRejects(t *testing.T) {
	path := bootstrapTestPath(t, "agent-policy-failure.sqlite")
	vault := bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	valid := hostileInsertUser(t, store.DB(), "valid-policy", 0, 0)
	invalid := hostileInsertUser(t, store.DB(), "invalid-policy", 0, 0)
	original := oldStewardPolicyForTest(t)
	for _, p := range []struct {
		id   int64
		body string
	}{{valid, original}, {invalid, "{}"}} {
		hostileMustExec(t, store.DB(), `INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,'{}',1,1)`, p.id, p.body)
	}
	personal := hostileMustLastID(t, hostileMustExec(t, store.DB(), `INSERT INTO models(user_id,provider,model,full_name,model_types,revision,created_at,updated_at) VALUES(?,'p','rollback','p/rollback',1,7,2,3)`, valid))
	charity := hostileMustLastID(t, hostileMustExec(t, store.DB(), `INSERT INTO charity_models(provider,model,full_name,pricing_mode,model_types,revision,created_at,updated_at) VALUES('p','rollback','[公益]p/rollback','per_request',16,7,2,3)`))
	restoreSchema36LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=36")
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err == nil {
		t.Fatal("invalid policy migrated")
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var body string
	if err = raw.QueryRow("SELECT permissions FROM steward_permissions WHERE user_id=?", valid).Scan(&body); err != nil || body != original {
		t.Fatal("partial policy update", body, err)
	}
	for _, model := range []struct {
		table    string
		id, mask int64
	}{{"models", personal, 1}, {"charity_models", charity, 16}} {
		var mask, revision, created, updated int64
		if err := raw.QueryRow("SELECT model_types,revision,created_at,updated_at FROM "+model.table+" WHERE id=?", model.id).Scan(&mask, &revision, &created, &updated); err != nil || mask != model.mask || revision != 7 || created != 2 || updated != 3 {
			t.Fatal("partial model interface update survived rollback", model.table, mask, revision, created, updated, err)
		}
	}
	if hash, err := db.SchemaManifestHash(t.Context(), raw); err != nil || hash != db.Schema36ManifestHash {
		t.Fatal("DDL did not roll back", hash, err)
	}
	hostileMustExec(t, raw, "UPDATE steward_permissions SET permissions=? WHERE user_id=?", original, invalid)
	raw.Close()
	explicitUpgrade(t, path, vault)
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal("retry", err)
	}
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=?", db.CurrentSchemaVersion+1)
	store.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("newer database accepted")
	}
	if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err == nil {
		t.Fatal("newer source converted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("newer source changed", err)
	}
}

func TestAgentOfflineUpgradePreservesExactSchema36(t *testing.T) {
	path := bootstrapTestPath(t, "agent-upgrade.sqlite")
	vault := bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	user := hostileInsertUser(t, store.DB(), "preserved-agent-owner", 0, 0)
	hostileMustExec(t, store.DB(), "UPDATE site_config SET value='preserved instance' WHERE key='site_name'")
	var originalName string
	if err = store.DB().QueryRow("SELECT username FROM users WHERE id=?", user).Scan(&originalName); err != nil {
		t.Fatal(err)
	}
	restoreSchema36LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=36")
	manifest, err := db.SchemaManifestHash(context.Background(), store.DB())
	if err != nil || manifest != db.Schema36ManifestHash {
		t.Fatal("source36 fixture drift", manifest, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	explicitUpgrade(t, path, vault)
	upgraded, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var username, site string
	if err = upgraded.DB().QueryRow("SELECT username,(SELECT value FROM site_config WHERE key='site_name') FROM users WHERE id=?", user).Scan(&username, &site); err != nil || username != originalName || site != "preserved instance" {
		t.Fatal("original facts changed", username, site, err)
	}
	for _, table := range []string{"ai_agent_plans", "ai_agent_model_results", "ai_agent_review_events", "ai_agent_jobs", "ai_agent_messages", "ai_agent_conversations", "ai_agent_memory_history", "ai_agent_memories", "ai_agent_settings", "agent_request_response_chunks", "agent_request_settings", "agent_requests", "agent_request_usage", "agent_request_usage_totals", "agent_request_usage_hours"} {
		var count int
		if err = upgraded.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("new table not empty", table, count, err)
		}
	}
	if err = validateGenerationTwoManifest(context.Background(), upgraded.DB()); err != nil {
		t.Fatal(err)
	}
}
