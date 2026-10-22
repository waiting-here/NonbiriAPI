package lifecycle

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestConnectionExportOwnsExactPublicVersionsAndHealth(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 100)
	owner := seedLifecycleUser(t, fixture.store.DB(), "connections-owner", false, 1)
	other := seedLifecycleUser(t, fixture.store.DB(), "connections-other", false, 1)
	tx, err := fixture.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, user := range []int64{owner, other} {
		seedExportConnection(t, tx, user)
	}
	out, err := exportConnections(t.Context(), tx, ExportRequest{UserID: owner, Limit: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Credentials) != 1 || len(out.CredentialVersions) != 1 || len(out.Grants) != 1 || len(out.DiscoverySources) != 1 || len(out.SourceRevisions) != 1 || len(out.BindingTargets) != 1 || len(out.NativeServiceEvidence) != 1 || len(out.DonationUses) != 3 {
		t.Fatalf("unexpected owner projection: %+v", out)
	}
	if out.CredentialVersions[0].CredentialVersion != "1" || out.Grants[0].GrantRevision != "1" || out.SourceRevisions[0].DiscoverySourceRevision != "1" || out.DiscoverySources[0].ResultGeneration != "2" {
		t.Fatal("public revisions changed")
	}
	if len(out.DonationUses[0].Health) != 1 || out.DonationUses[0].Health[0].FailureStreak != "2" || out.DonationUses[0].Health[0].BackoffRetryAt == nil || *out.DonationUses[0].Health[0].BackoffRetryAt != 20 {
		t.Fatalf("health: %+v", out.DonationUses[0])
	}
	body, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret_ref_id", "secret-sentinel", "fingerprint", "hmac", "context_id", "claim_id", "health_id", "next_claim_seq", "streak_generation"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("internal material exported: %s", forbidden)
		}
	}
	if _, err := exportConnections(t.Context(), tx, ExportRequest{UserID: owner, Limit: 6}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("nested health did not consume collection bound: %v", err)
	}
}

func seedExportConnection(t *testing.T, tx *sql.Tx, owner int64) {
	t.Helper()
	execute := func(query string, args ...any) sql.Result {
		result, err := tx.ExecContext(t.Context(), query, args...)
		if err != nil {
			t.Fatal(err, query)
		}
		return result
	}
	endpoint, err := testseed.InsertEndpointTx(t.Context(), tx, testseed.EndpointInput{OwnerUserID: owner, Type: connectorcontract.TypeOpenAICompatible, BaseURL: "https://export.example/v1", At: 1})
	if err != nil {
		t.Fatal(err)
	}
	secret := execute("INSERT INTO endpoint_key_secrets(context_id,encrypted_secret,key_body_review_hmac,created_at) VALUES(?,'nbsec:v2:secret-sentinel',?,1)", bytes.Repeat([]byte{byte(owner)}, 16), bytes.Repeat([]byte{byte(owner)}, 32))
	secretID, err := secret.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	key, err := testseed.InsertEndpointKeyTx(t.Context(), tx, testseed.KeyInput{EndpointID: endpoint, SecretRefID: secretID, Fingerprint: bytes.Repeat([]byte{byte(owner)}, 32), DisplayHead: "head", DisplayTail: "tail", At: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testseed.SeedDiscoveryCatalogTx(t.Context(), tx, testseed.DiscoveryCatalogInput{EndpointKeyID: key.KeyID, ListFormat: "openai", Models: []testseed.DiscoveryModel{{ID: "model"}}, At: 2}); err != nil {
		t.Fatal(err)
	}
	model := execute("INSERT INTO models(user_id,provider,model,full_name,created_at,updated_at) VALUES(?,'provider','model','provider/model',1,1)", owner)
	modelID, err := model.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testseed.InsertModelBindingTx(t.Context(), tx, testseed.BindingInput{ModelID: modelID, KeyID: key.KeyID, UpstreamModelID: "model", At: 2}); err != nil {
		t.Fatal(err)
	}
	result := execute("INSERT INTO donations(user_id,status,revision,description,review_note,created_at,updated_at) VALUES(?,'pending',1,'','',1,1)", owner)
	donation, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	zero, one, two := make([]byte, 16), make([]byte, 16), make([]byte, 16)
	one[15] = 1
	two[15] = 2
	result = execute(`INSERT INTO donation_keys(donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,failure_streak,streak_generation,next_claim_seq,next_fold_seq,safe_note,created_at,updated_at,source_endpoint_key_id,report_fingerprint)
 VALUES(?,?,'head','tail','https://export.example/v1','openai-compatible',?,?,?,?,?,?,?,?,?,?,'',1,1,?,?)`, donation, key.KeyID, zero, zero, zero, zero, zero, zero, zero, one, one, zero, key.KeyID, bytes.Repeat([]byte{byte(owner)}, 32))
	member, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	execute("INSERT INTO donation_key_memberships(endpoint_key_id,donation_id,donation_key_id,created_at) VALUES(?,?,?,1)", key.KeyID, donation, member)
	uses, err := testseed.SeedDonationUsesWithDiscoveryTx(t.Context(), tx, member, true, 2)
	if err != nil {
		t.Fatal(err)
	}
	execute(`INSERT INTO donation_service_health(use_id,endpoint_key_id,native_service_bit,credential_version_id,streak_generation,failure_streak,failure_disabled,next_claim_seq,next_fold_seq,retry_after_until,backoff_generation,backoff_episode,backoff_period,backoff_source,backoff_stage,backoff_retry_at,updated_at)
 VALUES(?,?,1,?,?,?,0,?,?,10,1,1,'day','builtin',1,20,3)`, uses[0], key.KeyID, key.CredentialVersionID, one, two, zero, zero)
}

func TestConnectionExportFieldSetsAreClosed(t *testing.T) {
	assertClosedJSONKeys(t, CredentialExport{}, "id", "connection_id", "note", "enabled", "revision", "credential_version", "deleted_at", "created_at", "updated_at")
	assertClosedJSONKeys(t, CredentialVersionExport{}, "credential_id", "credential_version", "display_head", "display_tail", "enabled", "revision", "created_at", "retired_at")
	assertClosedJSONKeys(t, DonationUseExport{}, "id", "donation_id", "donation_key_id", "native_services", "enabled", "discovery_enabled", "revision", "approved_revision", "pending_revision", "created_at", "updated_at", "revisions", "health")
	assertClosedJSONKeys(t, DonationUseHealthExport{}, "credential_id", "credential_version", "failure_streak", "failure_disabled", "retry_after_until", "backoff_retry_at", "backoff_period", "backoff_source", "backoff_stage", "updated_at")
	assertClosedJSONKeys(t, SourceRevisionExport{}, "discovery_source_id", "discovery_source_revision", "credential_id", "credential_version", "base_url", "list_format", "created_at")
}
