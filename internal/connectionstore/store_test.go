package connectionstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func connectionFixture(t *testing.T) (*sql.DB, *sql.Tx) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "db", "generation_two.sql"))
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	if _, err := database.Exec(string(body) + db.ServiceKeyScopeSQL + db.ManagementOperationsSQL + strings.Split(db.ManagementDetailsSQL, "\nCREATE TRIGGER")[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	zero := make([]byte, 16)
	for _, id := range []int64{1, 2} {
		execConnection(t, tx, `INSERT INTO users(id,discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?, 'fixture',0,?,?,?,?,?,?,?,?,1,1)`, id, string(rune('a'+id)), zero, zero, zero, zero, zero, zero, zero, zero)
	}
	execConnection(t, tx, "INSERT INTO endpoints(id,user_id,connector_type,base_url,native_service_mask,capability_origin,created_at,updated_at) VALUES(1,1,'openai-compatible','https://upstream.example/v1',49,'legacy',1,1)")
	execConnection(t, tx, "INSERT INTO credentials(id,user_id,note,enabled,revision,current_version_id,created_at,updated_at) VALUES(1,1,'',1,1,1,1,1)")
	seedVersion(t, tx, 1)
	execConnection(t, tx, "INSERT INTO endpoint_keys(id,endpoint_id,current_grant_revision_id,created_at,updated_at) VALUES(1,1,1,1,1)")
	seedGrant(t, tx, 1)
	return database, tx
}

func execConnection(t *testing.T, tx *sql.Tx, query string, args ...any) sql.Result {
	t.Helper()
	result, err := tx.ExecContext(t.Context(), query, args...)
	if err != nil {
		t.Fatalf("SQL: %v\n%s", err, query)
	}
	return result
}
func seedVersion(t *testing.T, tx *sql.Tx, version int64) {
	execConnection(t, tx, "INSERT INTO endpoint_key_secrets(id,context_id,encrypted_secret,key_body_review_hmac,created_at) VALUES(?,?, 'nbsec:v2:fixture',?,1)", version, bytes.Repeat([]byte{byte(version)}, 16), bytes.Repeat([]byte{byte(version)}, 32))
	execConnection(t, tx, "INSERT INTO credential_versions(id,credential_id,version,secret_ref_id,display_head,display_tail,created_at) VALUES(?,1,?,?,'head','tail',1)", version, version, version)
}
func seedGrant(t *testing.T, tx *sql.Tx, version int64) {
	execConnection(t, tx, "INSERT INTO endpoint_key_grant_revisions(id,endpoint_key_id,endpoint_id,owner_user_id,credential_id,credential_version_id,grant_revision,native_service_mask,report_fingerprint,origin,created_at) VALUES(?,1,1,1,1,?,?,49,?,'legacy',1)", version, version, version, bytes.Repeat([]byte{byte(version)}, 32))
}
func seedApprovedUse(t *testing.T, tx *sql.Tx, legacyMissingBody bool, sourceRevisionIDs ...int64) {
	zero := make([]byte, 16)
	one := make([]byte, 16)
	one[15] = 1
	execConnection(t, tx, "INSERT INTO donations(id,user_id,status,revision,description,review_note,created_at,updated_at) VALUES(1,1,'pending',1,'','',1,1)")
	execConnection(t, tx, `INSERT INTO donation_keys(id,donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,failure_streak,streak_generation,next_claim_seq,next_fold_seq,safe_note,created_at,updated_at,source_endpoint_key_id,report_fingerprint)
 VALUES(1,1,1,'head','tail','https://upstream.example/v1','openai-compatible',?,?,?,?,?,?,?,?,?,?,'',1,1,1,?)`, zero, zero, zero, zero, zero, zero, zero, one, one, zero, bytes.Repeat([]byte{1}, 32))
	execConnection(t, tx, "INSERT INTO donation_key_memberships(endpoint_key_id,donation_id,donation_key_id,created_at) VALUES(1,1,1,1)")
	execConnection(t, tx, "INSERT INTO donation_uses(id,donation_key_id,native_service_bit,created_at,updated_at) VALUES(1,1,1,1,1)")
	var source any
	if len(sourceRevisionIDs) > 0 {
		source = sourceRevisionIDs[0]
	}
	var hmac any = bytes.Repeat([]byte{1}, 32)
	origin := "initial"
	if legacyMissingBody {
		hmac = nil
		origin = "legacy"
	}
	execConnection(t, tx, `INSERT INTO donation_use_revisions(id,use_id,use_revision,grant_revision_id,source_grant_revision_id,credential_id_snapshot,credential_version,grant_revision,display_head,display_tail,report_fingerprint,key_body_review_hmac,origin,review_state,created_at,discovery_source_revision_id,source_discovery_revision_id)
 VALUES(1,1,1,1,1,1,1,1,'head','tail',?, ?,?,'pending',1,?,?)`, bytes.Repeat([]byte{1}, 32), hmac, origin, source, source)
	execConnection(t, tx, "UPDATE donation_use_revisions SET review_state='approved',reviewed_at=2 WHERE id=1")
	execConnection(t, tx, "UPDATE donation_uses SET current_approved_revision_id=1,revision=2,updated_at=2 WHERE id=1")
}
func rotateSelf(t *testing.T, tx *sql.Tx) {
	seedVersion(t, tx, 2)
	seedGrant(t, tx, 2)
	execConnection(t, tx, "UPDATE credentials SET current_version_id=2,revision=2,updated_at=3 WHERE id=1")
	execConnection(t, tx, "UPDATE endpoint_keys SET current_grant_revision_id=2,revision=2,updated_at=3 WHERE id=1")
}

func TestSelfAndApprovedUseKeepExactVersionsAndReviewGate(t *testing.T) {
	_, tx := connectionFixture(t)
	ctx := t.Context()
	seedApprovedUse(t, tx, false)
	rotateSelf(t, tx)
	self, err := connectionstore.LoadSelfGrantTx(ctx, tx, 1, 1)
	if err != nil || self.CredentialVersion != 2 {
		t.Fatal(self, err)
	}
	use, err := connectionstore.LoadApprovedUseTx(ctx, tx, 1, connectorcontract.NativeServiceChatCompletions)
	if err != nil || use.Grant.CredentialVersion != 1 {
		t.Fatal(use, err)
	}
	if _, err := connectionstore.LoadSelfGrantTx(ctx, tx, 2, 1); !errors.Is(err, connectionstore.ErrNotFound) {
		t.Fatalf("cross-owner grant = %v", err)
	}
	if retired, err := connectionstore.RetireUnreferencedVersionTx(ctx, tx, 1, 3); err != nil || retired {
		t.Fatal(retired, err)
	}
	batch, err := connectionstore.LoadSelfGrantsTx(ctx, tx, 1, []int64{1, 1, 99})
	if err != nil || len(batch) != 1 || batch[1].CredentialVersion != 2 {
		t.Fatal(batch, err)
	}
	execConnection(t, tx, "INSERT INTO donation_key_review_requirements(hmac,required,revision,created_at,updated_at) VALUES(?,1,1,3,3)", bytes.Repeat([]byte{1}, 32))
	if _, err := connectionstore.LoadApprovedUseTx(ctx, tx, 1, connectorcontract.NativeServiceChatCompletions); !errors.Is(err, connectionstore.ErrUnavailable) {
		t.Fatalf("required review not enforced: %v", err)
	}
	if batch, err := connectionstore.LoadApprovedUsesTx(ctx, tx, []int64{1, 1}); err != nil || len(batch) != 0 {
		t.Fatal(batch, err)
	}
	if _, err := connectionstore.LoadSelfGrantTx(ctx, tx, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE donation_use_revisions SET report_fingerprint=NULL WHERE id=1"); err == nil {
		t.Fatal("current fingerprint cleared")
	}
	if _, err := tx.Exec("UPDATE endpoint_key_grant_revisions SET credential_version_id=2 WHERE id=1"); err == nil {
		t.Fatal("grant changed version")
	}
}
func TestPendingReplacementAndMemberTermination(t *testing.T) {
	_, tx := connectionFixture(t)
	seedApprovedUse(t, tx, false)
	rotateSelf(t, tx)
	staged, err := connectionstore.StagePendingUseRevisionTx(t.Context(), tx, connectionstore.StagePendingUseRevisionInput{UseID: 1, ExpectedRevision: 2, GrantRevisionID: 2, Origin: connectionstore.UseOriginRotation, At: 3})
	if err != nil || staged.Revision != 2 || staged.UseRevision != 3 {
		t.Fatal(staged, err)
	}
	use, err := connectionstore.LoadApprovedUseTx(t.Context(), tx, 1, connectorcontract.NativeServiceChatCompletions)
	if err != nil || use.Grant.CredentialVersion != 1 {
		t.Fatal(use, err)
	}
	staged, err = connectionstore.StagePendingUseRevisionTx(t.Context(), tx, connectionstore.StagePendingUseRevisionInput{UseID: 1, ExpectedRevision: 3, GrantRevisionID: 2, Origin: connectionstore.UseOriginRotation, At: 4})
	if err != nil || staged.Revision != 3 {
		t.Fatal(staged, err)
	}
	var state string
	if err := tx.QueryRow("SELECT review_state FROM donation_use_revisions WHERE use_id=1 AND use_revision=2").Scan(&state); err != nil || state != "superseded" {
		t.Fatal(state, err)
	}
	execConnection(t, tx, "DELETE FROM donation_key_memberships WHERE donation_key_id=1")
	execConnection(t, tx, "UPDATE donation_keys SET enabled=0,ended_at=5,ended_reason='expired',report_match_until=7776005,updated_at=5 WHERE id=1")
	if err := connectionstore.EndDonationKeyUsesTx(t.Context(), tx, 1, 5); err != nil {
		t.Fatal(err)
	}
	if err := connectionstore.EndDonationKeyUsesTx(t.Context(), tx, 1, 5); err != nil {
		t.Fatal(err)
	}
	var secret sql.NullInt64
	if err := tx.QueryRow("SELECT secret_ref_id FROM credential_versions WHERE id=1").Scan(&secret); err != nil || secret.Valid {
		t.Fatal(secret, err)
	}
	if _, err := connectionstore.StagePendingUseRevisionTx(t.Context(), tx, connectionstore.StagePendingUseRevisionInput{UseID: 1, ExpectedRevision: 5, GrantRevisionID: 2, Origin: connectionstore.UseOriginRotation, At: 6}); !errors.Is(err, connectionstore.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := connectionstore.ClearExpiredUseFingerprintsTx(t.Context(), tx, 1, 7776005); !errors.Is(err, connectionstore.ErrInvariant) {
		t.Fatalf("cleared before original retention: %v", err)
	}
	execConnection(t, tx, "UPDATE donation_keys SET report_fingerprint=NULL,updated_at=7776005 WHERE id=1")
	if _, err := connectionstore.ClearExpiredUseFingerprintsTx(t.Context(), tx, 1, 7776005); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE donation_use_revisions SET report_fingerprint=zeroblob(32) WHERE id=1"); err == nil {
		t.Fatal("retired fingerprint restored")
	}
}
func TestLegacyMissingBodyDoesNotRevokeApprovedInference(t *testing.T) {
	_, tx := connectionFixture(t)
	seedApprovedUse(t, tx, true)
	if _, err := connectionstore.LoadApprovedUseTx(t.Context(), tx, 1, connectorcontract.NativeServiceChatCompletions); err != nil {
		t.Fatal(err)
	}
}
func TestKeyCurrentGrantCycleRejectsIncompleteCommit(t *testing.T) {
	database, tx := connectionFixture(t)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	next, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Rollback()
	if _, err := next.Exec("INSERT INTO endpoint_keys(id,endpoint_id,current_grant_revision_id,created_at,updated_at) VALUES(2,1,999,1,1)"); err != nil {
		t.Fatal(err)
	}
	if err := next.Commit(); err == nil {
		t.Fatal("key without complete grant committed")
	}
}
func TestAccountCleanupRetiresUnreferencedVersions(t *testing.T) {
	_, tx := connectionFixture(t)
	execConnection(t, tx, "DELETE FROM endpoints WHERE user_id=1")
	if err := connectionstore.PrepareAccountDeletionTx(t.Context(), tx, 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := connectionstore.PrepareAccountDeletionTx(t.Context(), tx, 1, 10); err != nil {
		t.Fatal(err)
	}
	var owner, secret sql.NullInt64
	if err := tx.QueryRow("SELECT user_id FROM credentials WHERE id=1").Scan(&owner); err != nil || owner.Valid {
		t.Fatal(owner, err)
	}
	if err := tx.QueryRow("SELECT secret_ref_id FROM credential_versions WHERE id=1").Scan(&secret); err != nil || secret.Valid {
		t.Fatal(secret, err)
	}
	execConnection(t, tx, "DELETE FROM users WHERE id=1")
}

func TestDiscoveryUsesKeepPendingAndApprovedVersionsSeparate(t *testing.T) {
	_, tx := connectionFixture(t)
	ctx := t.Context()
	seedApprovedUse(t, tx, false)
	old, err := connectionstore.LoadDiscoveryUseTx(ctx, tx, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	oldSource, err := connectionstore.EnsureDiscoverySourceTx(ctx, tx, old, 2)
	if err != nil {
		t.Fatal(err)
	}
	rotateSelf(t, tx)
	execConnection(t, tx, "INSERT INTO discovery_sources(id,user_id,current_revision_id,created_at,updated_at) VALUES(2,1,2,3,3)")
	execConnection(t, tx, `INSERT INTO discovery_source_revisions(id,source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at)
 VALUES(2,2,1,1,1,2,'https://catalog.example/special','anthropic',3)`)
	execConnection(t, tx, "INSERT INTO discovery_source_states(source_revision_id,result_generation,updated_at) VALUES(2,1,3)")
	execConnection(t, tx, "INSERT INTO endpoint_key_discovery_sources(endpoint_key_id,source_id,grant_revision_id,source_revision_id,credential_version_id) VALUES(1,2,2,2,2)")
	staged, err := connectionstore.StagePendingUseRevisionTx(ctx, tx, connectionstore.StagePendingUseRevisionInput{UseID: 1, ExpectedRevision: 2, GrantRevisionID: 2, Origin: connectionstore.UseOriginRotation, At: 3})
	if err != nil {
		t.Fatal(err)
	}
	execConnection(t, tx, "UPDATE donation_uses SET discovery_enabled=0,revision=revision+1 WHERE id=1")
	pending, err := connectionstore.LoadDiscoveryUseTx(ctx, tx, 1, 0)
	if err != nil || pending.RevisionID != staged.RevisionID || pending.Grant.CredentialVersion != 2 || pending.DiscoverySourceRevisionID != 2 {
		t.Fatalf("pending: %+v %v", pending, err)
	}
	source, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: 1, SourceID: 2, Purpose: connectionstore.SourceCharity, DonationUseID: 1, UseRevisionID: pending.RevisionID})
	if err != nil || source.CredentialVersion != 2 || source.CanonicalBaseURL != "https://catalog.example/special" || source.ListFormat != "anthropic" {
		t.Fatalf("configured source: %+v %v", source, err)
	}
	approved, err := connectionstore.LoadApprovedUseTx(ctx, tx, 1, connectorcontract.NativeServiceChatCompletions)
	if err != nil || approved.Grant.CredentialVersion != 1 {
		t.Fatalf("inference changed: %+v %v", approved, err)
	}
	old, err = connectionstore.LoadDiscoveryUseTx(ctx, tx, 1, approved.ApprovedRevisionID)
	if err != nil || old.DiscoverySourceRevisionID != oldSource || old.Grant.CredentialVersion != 1 {
		t.Fatalf("approved catalog borrowed current version: %+v %v", old, err)
	}
	if _, err = connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: 1, SourceID: 2, Purpose: connectionstore.SourceCharity, DonationUseID: 1, UseRevisionID: approved.ApprovedRevisionID}); !errors.Is(err, connectionstore.ErrUnavailable) {
		t.Fatalf("wrong-version source accepted: %v", err)
	}
	next, err := connectionstore.StagePendingUseRevisionTx(ctx, tx, connectionstore.StagePendingUseRevisionInput{UseID: 1, ExpectedRevision: staged.UseRevision + 1, GrantRevisionID: 2, Origin: connectionstore.UseOriginRotation, At: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connectionstore.LoadDiscoveryUseTx(ctx, tx, 1, pending.RevisionID); !errors.Is(err, connectionstore.ErrUnavailable) {
		t.Fatalf("superseded revision accepted: %v", err)
	}
	execConnection(t, tx, "UPDATE discovery_sources SET enabled=0,revision=revision+1 WHERE id=2")
	if _, err = connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: 1, SourceID: 2, Purpose: connectionstore.SourceCharity, DonationUseID: 1, UseRevisionID: next.RevisionID}); !errors.Is(err, connectionstore.ErrUnavailable) {
		t.Fatalf("disabled configured source accepted: %v", err)
	}
}

func TestUnpinnedApprovedCatalogRetainsConfiguredSourceAcrossRotation(t *testing.T) {
	_, tx := connectionFixture(t)
	ctx := t.Context()
	seedApprovedUse(t, tx, false)
	execConnection(t, tx, "INSERT INTO discovery_sources(id,user_id,current_revision_id,created_at,updated_at) VALUES(1,1,1,1,1)")
	execConnection(t, tx, `INSERT INTO discovery_source_revisions(id,source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at)
 VALUES(1,1,1,1,1,1,'https://catalog.example/special-v1','openai',1)`)
	execConnection(t, tx, "INSERT INTO discovery_source_states(source_revision_id,result_generation,updated_at) VALUES(1,1,1)")
	execConnection(t, tx, "INSERT INTO endpoint_key_discovery_sources(endpoint_key_id,source_id,grant_revision_id,source_revision_id,credential_version_id) VALUES(1,1,1,1,1)")
	rotateSelf(t, tx)
	execConnection(t, tx, `INSERT INTO discovery_source_revisions(id,source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at)
 VALUES(2,1,1,2,1,2,'https://catalog.example/special-v2','openai',3)`)
	execConnection(t, tx, "INSERT INTO discovery_source_states(source_revision_id,result_generation,updated_at) VALUES(2,1,3)")
	execConnection(t, tx, "UPDATE discovery_sources SET current_revision_id=2,revision=revision+1,updated_at=3 WHERE id=1")
	execConnection(t, tx, "UPDATE endpoint_key_discovery_sources SET grant_revision_id=2,credential_version_id=2,source_revision_id=2 WHERE endpoint_key_id=1")
	staged, err := connectionstore.StagePendingUseRevisionTx(ctx, tx, connectionstore.StagePendingUseRevisionInput{UseID: 1, ExpectedRevision: 2, GrantRevisionID: 2, Origin: connectionstore.UseOriginRotation, At: 3})
	if err != nil {
		t.Fatal(err)
	}
	old, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: 1, SourceID: 1, Purpose: connectionstore.SourceCharity, DonationUseID: 1, UseRevisionID: 1})
	if err != nil || old.CredentialVersion != 1 || old.SourceRevisionID != 1 || old.CanonicalBaseURL != "https://catalog.example/special-v1" {
		t.Fatalf("lost configured historical catalog: %+v %v", old, err)
	}
	current, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: 1, SourceID: 1, Purpose: connectionstore.SourceCharity, DonationUseID: 1, UseRevisionID: staged.RevisionID})
	if err != nil || current.CredentialVersion != 2 || current.SourceRevisionID != 2 || current.CanonicalBaseURL != "https://catalog.example/special-v2" {
		t.Fatalf("pending catalog: %+v %v", current, err)
	}
}

func TestUnpinnedApprovedCatalogRetainsStandardSourceAcrossRotation(t *testing.T) {
	_, tx := connectionFixture(t)
	ctx := t.Context()
	seedApprovedUse(t, tx, false)
	use, err := connectionstore.LoadDiscoveryUseTx(ctx, tx, 1, 0)
	if err != nil || use.DiscoverySourceRevisionID != 0 {
		t.Fatalf("unexpected initial catalog: %+v %v", use, err)
	}
	oldSourceRevision, err := connectionstore.EnsureDiscoverySourceTx(ctx, tx, use, 2)
	if err != nil {
		t.Fatal(err)
	}
	var sourceID int64
	if err := tx.QueryRow("SELECT source_id FROM discovery_source_revisions WHERE id=?", oldSourceRevision).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	run := execConnection(t, tx, "INSERT INTO discovery_runs(source_revision_id,source_revision_snapshot,credential_version_id,result_generation,authorization_kind,authorization_use_id,authorization_use_revision_id,state,started_at,completed_at,model_count) VALUES(?,1,1,2,'donation_use',1,1,'succeeded',2,2,1)", oldSourceRevision)
	runID, err := run.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	execConnection(t, tx, "UPDATE discovery_source_states SET result_generation=2,current_run_id=?,last_successful_run_id=?,updated_at=2 WHERE source_revision_id=?", runID, runID, oldSourceRevision)
	execConnection(t, tx, "INSERT INTO model_catalog_entries(discovery_run_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at,native_service_mask) VALUES(?,'automatic','2:0','old-model','',2,2,2,1)", runID)
	assertCatalog := func() {
		t.Helper()
		var supports, generation int64
		if err := tx.QueryRow("SELECT automatic_supports,automatic_revision FROM charity_model_catalog WHERE donation_key_id=1 AND normalized_model_id='old-model'").Scan(&supports, &generation); err != nil || supports != 1 || generation != 2 {
			t.Fatalf("approved catalog lost successful result: supports=%d generation=%d err=%v", supports, generation, err)
		}
	}
	assertCatalog()
	rotateSelf(t, tx)
	execConnection(t, tx, "INSERT INTO discovery_source_revisions(id,source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at) VALUES(?,?,1,2,1,2,'https://upstream.example/v1','openai',3)", oldSourceRevision+1, sourceID)
	execConnection(t, tx, "INSERT INTO discovery_source_states(source_revision_id,result_generation,updated_at) VALUES(?,1,3)", oldSourceRevision+1)
	execConnection(t, tx, "UPDATE discovery_sources SET current_revision_id=?,revision=revision+1,updated_at=3 WHERE id=?", oldSourceRevision+1, sourceID)
	staged, err := connectionstore.StagePendingUseRevisionTx(ctx, tx, connectionstore.StagePendingUseRevisionInput{UseID: 1, ExpectedRevision: 2, GrantRevisionID: 2, Origin: connectionstore.UseOriginRotation, At: 3})
	if err != nil {
		t.Fatal(err)
	}
	old, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: 1, SourceID: sourceID, Purpose: connectionstore.SourceCharity, DonationUseID: 1, UseRevisionID: 1})
	if err != nil || old.SourceRevisionID != oldSourceRevision || old.CredentialVersion != 1 || old.SecretRefID != 1 {
		t.Fatalf("standard catalog borrowed current credential: %+v %v", old, err)
	}
	current, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: 1, SourceID: sourceID, Purpose: connectionstore.SourceCharity, DonationUseID: 1, UseRevisionID: staged.RevisionID})
	if err != nil || current.SourceRevisionID != oldSourceRevision+1 || current.CredentialVersion != 2 {
		t.Fatalf("pending standard catalog: %+v %v", current, err)
	}
	assertCatalog()
	execConnection(t, tx, "UPDATE discovery_sources SET enabled=0,revision=revision+1 WHERE id=?", sourceID)
	if _, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: 1, SourceID: sourceID, Purpose: connectionstore.SourceCharity, DonationUseID: 1, UseRevisionID: 1}); !errors.Is(err, connectionstore.ErrUnavailable) {
		t.Fatalf("disabled historical source accepted: %v", err)
	}
}
