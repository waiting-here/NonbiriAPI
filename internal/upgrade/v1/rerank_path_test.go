package v1

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func restoreSchema50LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	oldAudit := strings.Replace(db.CharityAdaptationAuditSQL, "actor_role IN ('owner','admin','trainee5','level6')", "actor_role IN ('owner','admin')", 1)
	hostileMustExec(t, database, oldAudit)
	for _, body := range strings.Split(db.ManagementDetailsSQL, "\nCREATE TRIGGER ")[1:] {
		if strings.HasPrefix(body, "management_capture_steward_permissions_") {
			hostileMustExec(t, database, "DROP TRIGGER "+strings.Fields(body)[0]+"; CREATE TRIGGER "+body)
		}
	}
	table, guards, ok := strings.Cut(callerParameterErrorSchema, "CREATE TRIGGER")
	if !ok {
		t.Fatal("missing retained proof schema")
	}
	hostileMustExec(t, database, "CREATE TABLE caller_parameter_errors_rebuild AS SELECT claim_id,http_status,error_code FROM dispatch_caller_parameter_errors; DROP TABLE dispatch_caller_parameter_errors;"+table+"INSERT INTO dispatch_caller_parameter_errors SELECT claim_id,http_status,error_code FROM caller_parameter_errors_rebuild; DROP TABLE caller_parameter_errors_rebuild; CREATE TRIGGER"+guards)
	hostileMustExec(t, database, `DROP VIEW endpoint_grant_authorizations;
CREATE VIEW endpoint_grant_authorizations AS
 SELECT g.owner_user_id,g.endpoint_id,g.endpoint_key_id,g.credential_id,g.credential_version_id,
 v.version AS credential_version,g.id AS grant_revision_id,g.grant_revision,v.secret_ref_id,
 e.connector_type AS configured_type,e.base_url AS canonical_base_url,
 e.native_service_mask AS endpoint_native_service_mask,g.native_service_mask AS granted_native_service_mask,
 (e.native_service_mask&g.native_service_mask&CASE WHEN e.connector_type IN ('openai-compatible','openai-chat-completions','openai-responses','openai-decisions') AND e.rerank_format<>g.rerank_format THEN 507903 ELSE 524287 END) AS effective_native_service_mask,g.rerank_format,e.rerank_format AS endpoint_rerank_format,
 CASE WHEN k.id IS NOT NULL AND k.endpoint_id=g.endpoint_id AND e.id=g.endpoint_id AND e.user_id=g.owner_user_id AND c.id=g.credential_id AND c.user_id=g.owner_user_id AND v.id=g.credential_version_id AND v.credential_id=g.credential_id THEN 1 ELSE 0 END AS relationship_valid,
 CASE WHEN k.id IS NOT NULL AND k.endpoint_id=g.endpoint_id AND e.id=g.endpoint_id AND e.user_id=g.owner_user_id AND c.id=g.credential_id AND c.user_id=g.owner_user_id AND v.id=g.credential_version_id AND v.credential_id=g.credential_id AND e.enabled=1 AND k.enabled=1 AND c.enabled=1 AND c.deleted_at IS NULL
   AND v.enabled=1 AND s.id=v.secret_ref_id AND s.orphaned_at IS NULL AND (e.native_service_mask&g.native_service_mask&CASE WHEN e.connector_type IN ('openai-compatible','openai-chat-completions','openai-responses','openai-decisions') AND e.rerank_format<>g.rerank_format THEN 507903 ELSE 524287 END)<>0 THEN 1 ELSE 0 END AS available
 FROM endpoint_key_grant_revisions g
 LEFT JOIN endpoint_keys k ON k.id=g.endpoint_key_id
 LEFT JOIN endpoints e ON e.id=g.endpoint_id
 LEFT JOIN credentials c ON c.id=g.credential_id
 LEFT JOIN credential_versions v ON v.id=g.credential_version_id
 LEFT JOIN endpoint_key_secrets s ON s.id=v.secret_ref_id;
DROP TRIGGER donation_use_pointer_revision_guard;
CREATE TRIGGER donation_use_pointer_revision_guard BEFORE UPDATE ON donation_uses
WHEN (NEW.current_approved_revision_id IS NOT OLD.current_approved_revision_id OR NEW.current_pending_revision_id IS NOT OLD.current_pending_revision_id OR NEW.enabled<>OLD.enabled OR NEW.discovery_enabled<>OLD.discovery_enabled) AND (OLD.revision=9223372036854775807 OR NEW.revision<>OLD.revision+1)
BEGIN SELECT RAISE(ABORT,'donation use changes require a revision'); END;
DROP TRIGGER donation_use_revision_identity_guard;
CREATE TRIGGER donation_use_revision_identity_guard BEFORE UPDATE ON donation_use_revisions
WHEN NEW.id IS NOT OLD.id OR NEW.use_id IS NOT OLD.use_id OR NEW.use_revision IS NOT OLD.use_revision OR NEW.source_grant_revision_id IS NOT OLD.source_grant_revision_id OR NEW.source_discovery_revision_id IS NOT OLD.source_discovery_revision_id OR NEW.credential_id_snapshot IS NOT OLD.credential_id_snapshot OR NEW.credential_version IS NOT OLD.credential_version OR NEW.grant_revision IS NOT OLD.grant_revision OR NEW.display_head IS NOT OLD.display_head OR NEW.display_tail IS NOT OLD.display_tail OR NEW.key_body_review_hmac IS NOT OLD.key_body_review_hmac OR NEW.origin IS NOT OLD.origin OR NEW.created_at IS NOT OLD.created_at OR (NEW.grant_revision_id IS NOT OLD.grant_revision_id AND NOT (NEW.grant_revision_id IS NULL AND NOT EXISTS(SELECT 1 FROM endpoint_key_grant_revisions WHERE id=OLD.grant_revision_id))) OR (NEW.discovery_source_revision_id IS NOT OLD.discovery_source_revision_id AND NOT (NEW.discovery_source_revision_id IS NULL AND NOT EXISTS(SELECT 1 FROM discovery_source_revisions WHERE id=OLD.discovery_source_revision_id)))
BEGIN SELECT RAISE(ABORT,'use revision identity is immutable'); END;
DROP TRIGGER management_capture_donation_uses_update;
CREATE TRIGGER management_capture_donation_uses_update BEFORE UPDATE ON donation_uses
WHEN EXISTS(SELECT 1 FROM management_capture_context)
BEGIN
 INSERT INTO management_capture_totals(event_id,kind,updated) SELECT event_id,'donation_uses',1 FROM management_capture_context WHERE id=1 AND (OLD.current_approved_revision_id IS NOT NEW.current_approved_revision_id OR OLD.current_pending_revision_id IS NOT NEW.current_pending_revision_id OR OLD.discovery_enabled IS NOT NEW.discovery_enabled OR OLD.donation_key_id IS NOT NEW.donation_key_id OR OLD.enabled IS NOT NEW.enabled OR OLD.native_service_bit IS NOT NEW.native_service_bit)
 ON CONFLICT(event_id,kind) DO UPDATE SET updated=updated+1;
 UPDATE management_capture_totals SET omitted=omitted+1 WHERE event_id=(SELECT event_id FROM management_capture_context) AND kind='donation_uses' AND (OLD.current_approved_revision_id IS NOT NEW.current_approved_revision_id OR OLD.current_pending_revision_id IS NOT NEW.current_pending_revision_id OR OLD.discovery_enabled IS NOT NEW.discovery_enabled OR OLD.donation_key_id IS NOT NEW.donation_key_id OR OLD.enabled IS NOT NEW.enabled OR OLD.native_service_bit IS NOT NEW.native_service_bit) AND (SELECT COUNT(*) FROM management_capture_rows WHERE event_id=management_capture_totals.event_id)>=256 AND NOT EXISTS(SELECT 1 FROM management_capture_rows WHERE event_id=management_capture_totals.event_id AND kind='donation_uses' AND target_id=CAST(NEW.id AS TEXT));
 INSERT INTO management_capture_rows(event_id,kind,target_id,before_json,after_json,facts_json)
 SELECT event_id,'donation_uses',CAST(NEW.id AS TEXT),json_object('$name',substr(COALESCE(CAST('Donation use '||OLD.id AS TEXT),''),1,512),'donation_key_id',substr(CAST(OLD.donation_key_id AS TEXT),1,512),'native_service_bit',substr(CAST(OLD.native_service_bit AS TEXT),1,512),'enabled',substr(CAST(OLD.enabled AS TEXT),1,512),'discovery_enabled',substr(CAST(OLD.discovery_enabled AS TEXT),1,512),'current_approved_revision_id',substr(CAST(OLD.current_approved_revision_id AS TEXT),1,512),'current_pending_revision_id',substr(CAST(OLD.current_pending_revision_id AS TEXT),1,512)),json_object('$name',substr(COALESCE(CAST('Donation use '||NEW.id AS TEXT),''),1,512),'donation_key_id',substr(CAST(NEW.donation_key_id AS TEXT),1,512),'native_service_bit',substr(CAST(NEW.native_service_bit AS TEXT),1,512),'enabled',substr(CAST(NEW.enabled AS TEXT),1,512),'discovery_enabled',substr(CAST(NEW.discovery_enabled AS TEXT),1,512),'current_approved_revision_id',substr(CAST(NEW.current_approved_revision_id AS TEXT),1,512),'current_pending_revision_id',substr(CAST(NEW.current_pending_revision_id AS TEXT),1,512)),json_patch('{}',json_object()) FROM management_capture_context WHERE id=1
 AND ((SELECT COUNT(*) FROM management_capture_rows WHERE event_id=management_capture_context.event_id)<256 OR EXISTS(SELECT 1 FROM management_capture_rows WHERE event_id=management_capture_context.event_id AND kind='donation_uses' AND target_id=CAST(NEW.id AS TEXT)))
 ON CONFLICT(event_id,kind,target_id) DO UPDATE SET after_json=excluded.after_json,facts_json=json_patch(facts_json,excluded.facts_json);
END;
ALTER TABLE dispatch_claim_targets DROP COLUMN rerank_path;
ALTER TABLE donation_uses DROP COLUMN rerank_path_override;
ALTER TABLE donation_use_revisions DROP COLUMN rerank_path;
ALTER TABLE endpoints DROP COLUMN rerank_path;
`)
	assertRetainedManifest(t, database, db.Schema50ManifestHash)
}

// The registered schema50 source retains credentials, authorization and history.
func TestRerankPathUpgradeIsExplicitAtomicAndPreservesFacts(t *testing.T) {
	path, vault := bootstrapTestPath(t, "rerank-upgrade.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := seedEndpointKeyHistory(t, store.DB(), vault, 'R')
	var owner, endpoint int64
	if err = store.DB().QueryRow("SELECT user_id,id FROM endpoints WHERE id=(SELECT endpoint_id FROM endpoint_keys WHERE id=?)", key).Scan(&owner, &endpoint); err != nil {
		t.Fatal(err)
	}
	donation := hostileInsertDonation(t, store.DB(), owner)
	member := hostileInsertDonationKey(t, store.DB(), donation, key)
	hostileMustExec(t, store.DB(), "INSERT INTO donation_key_memberships(donation_key_id,endpoint_key_id,donation_id,created_at) VALUES(?,?,?,0)", member, key, donation)
	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = testseed.SeedDonationUsesTx(t.Context(), tx, member, true, 0); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// A retained dispatch snapshot must receive the compatible default too.
	requestID, claimID := hostileOIDVariant("req_", 'P', 'Q'), hostileOIDVariant("clm_", 'P', 'Q')
	hostileInsertLogicalRequest(t, store.DB(), requestID, owner, "openai_chat_completions", 1)
	hostileMustExec(t, store.DB(), "INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,endpoint_key_id,secret_ref_id,claim_now,state) SELECT ?,?,1,'debug_live',endpoint_key_id,secret_ref_id,0,'claimed' FROM endpoint_grant_authorizations WHERE endpoint_key_id=?", claimID, requestID, key)
	hostileMustExec(t, store.DB(), `INSERT INTO dispatch_claim_targets
 (claim_id,authorization_kind,owner_user_id,endpoint_key_id_snapshot,endpoint_id_snapshot,
 credential_id_snapshot,credential_version_id,credential_version,grant_revision_id_snapshot,
 grant_revision,native_service_bit,configured_type,wire_type,canonical_base_url,upstream_model_id,rerank_format)
 SELECT c.id,'self',g.owner_user_id,g.endpoint_key_id,g.endpoint_id,g.credential_id,g.credential_version_id,g.credential_version,g.grant_revision_id,g.grant_revision,1,g.configured_type,g.configured_type,g.canonical_base_url,'existing-model',g.rerank_format
 FROM dispatch_claims c JOIN endpoint_grant_authorizations g ON g.endpoint_key_id=c.endpoint_key_id
 WHERE c.id=?`, claimID)
	proofRequest, proofClaim := hostileOIDVariant("req_", 'L', 'Q'), hostileOIDVariant("clm_", 'L', 'Q')
	hostileInsertLogicalRequest(t, store.DB(), proofRequest, owner, "charity_chat_completions", 10)
	hostileMustExec(t, store.DB(), "INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,endpoint_key_id,secret_ref_id,donation_key_id,claim_now,state,dispatched_at,donor_reward_state) SELECT ?,?,1,'charity',endpoint_key_id,secret_ref_id,?,0,'dispatched',1,'pending' FROM endpoint_grant_authorizations WHERE endpoint_key_id=?", proofClaim, proofRequest, member, key)
	hostileMustExec(t, store.DB(), "INSERT INTO dispatch_caller_parameter_errors(claim_id,http_status,error_code) VALUES(?,400,'invalid_value')", proofClaim)
	before := endpointKeyHistoryDigests(t, store.DB())
	restoreSchema50LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=50")
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := db.Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("schema50 started without explicit upgrade")
	}
	checked, err := Run(t.Context(), path, vault, Options{})
	if err != nil || checked.Status != "ready" || checked.SourceSchemaVersion != 50 {
		t.Fatal(checked, err)
	}
	assertSource := func() {
		t.Helper()
		err := db.InspectContext(t.Context(), path, vault, func(ctx context.Context, database *sql.DB) error {
			assertRetainedManifest(t, database, db.Schema50ManifestHash)
			var code string
			if err := database.QueryRowContext(ctx, "SELECT error_code FROM dispatch_caller_parameter_errors WHERE claim_id=?", proofClaim).Scan(&code); err != nil || code != "invalid_value" {
				t.Fatal("source caller proof changed", code, err)
			}
			var version int
			return database.QueryRowContext(ctx, "SELECT version FROM schema_state WHERE id=1 AND version=50").Scan(&version)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	assertSource()
	index := migrationIndex(t, 50)
	original := storageSchema.versions[index].apply
	t.Cleanup(func() { storageSchema.versions[index].apply = original })
	storageSchema.versions[index].apply = func(ctx context.Context, tx *sql.Tx) error {
		var path string
		if err := tx.QueryRowContext(ctx, "SELECT rerank_path FROM endpoints WHERE id=?", endpoint).Scan(&path); err != nil {
			return err
		}
		return errors.New("injected after rerank DDL")
	}
	if _, err = Run(t.Context(), path, vault, Options{Apply: true}); err == nil {
		t.Fatal("injected migration committed")
	}
	storageSchema.versions[index].apply = original
	assertSource()
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.Status != "converted" || result.TargetSchemaVersion != 51 {
		t.Fatal(result, err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if after := endpointKeyHistoryDigests(t, store.DB()); !reflect.DeepEqual(before, after) {
		t.Fatal("credential or claim history changed")
	}
	var endpointPath string
	var nondefault int
	if err = store.DB().QueryRow("SELECT rerank_path FROM endpoints WHERE id=?", endpoint).Scan(&endpointPath); err != nil || endpointPath != "" {
		t.Fatal(endpointPath, err)
	}
	if err = store.DB().QueryRow("SELECT (SELECT count(*) FROM donation_uses WHERE rerank_path_override IS NOT NULL)+(SELECT count(*) FROM donation_use_revisions WHERE rerank_path<>'')+(SELECT count(*) FROM dispatch_claim_targets WHERE rerank_path<>'')").Scan(&nondefault); err != nil || nondefault != 0 {
		t.Fatal(nondefault, err)
	}
	if count := countRows(t, store, "SELECT count(*) FROM dispatch_claim_targets WHERE rerank_path=''"); count != 1 {
		t.Fatal("retained claim targets", count)
	}
	var proofCode string
	if err := store.DB().QueryRow("SELECT error_code FROM dispatch_caller_parameter_errors WHERE claim_id=?", proofClaim).Scan(&proofCode); err != nil || proofCode != "invalid_value" {
		t.Fatal("retained caller proof", proofCode, err)
	}
	for index, code := range []string{"context_length_exceeded", "input_too_long", "prompt_too_long", "string_above_max_length", "AI_APICallError"} {
		id := hostileOIDVariant("clm_", byte('a'+index), 'Q')
		hostileMustExec(t, store.DB(), "INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,endpoint_key_id,secret_ref_id,donation_key_id,claim_now,state,dispatched_at,donor_reward_state) SELECT ?,?,?,'charity',endpoint_key_id,secret_ref_id,?,0,'dispatched',1,'pending' FROM endpoint_grant_authorizations WHERE endpoint_key_id=?", id, proofRequest, index+2, member, key)
		if _, err := store.DB().Exec("INSERT INTO dispatch_caller_parameter_errors(claim_id,http_status,error_code) VALUES(?,500,?)", id, code); err == nil {
			t.Fatal("accepted unrelated HTTP status")
		}
		if code == "AI_APICallError" {
			if _, err := store.DB().Exec("INSERT INTO dispatch_caller_parameter_errors(claim_id,http_status,error_code) VALUES(?,422,?)", id, code); err == nil {
				t.Fatal("accepted unsupported Gateway HTTP status")
			}
		}
		hostileMustExec(t, store.DB(), "INSERT INTO dispatch_caller_parameter_errors(claim_id,http_status,error_code) VALUES(?,?,?)", id, 400+(index%2)*22, code)
		if _, err := store.DB().Exec("UPDATE dispatch_caller_parameter_errors SET error_code='invalid_value' WHERE claim_id=?", id); err == nil {
			t.Fatal("caller proof became mutable")
		}
	}
	assertRetainedManifest(t, store.DB(), db.PinnedGenerationTwoManifestHash)
	hostileMustExec(t, store.DB(), "DELETE FROM donation_keys WHERE id=?", member)
	if count := countRows(t, store, "SELECT count(*) FROM donation_uses WHERE donation_key_id=?", member); count != 0 {
		t.Fatal("deleted use survived")
	}
}
