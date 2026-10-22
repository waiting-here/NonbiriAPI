package connectionstore_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestDeletedCurrentCredentialPreservesDifferentApprovedCredential(t *testing.T) {
	_, tx := connectionFixture(t)
	ctx := t.Context()
	execConnection(t, tx, "INSERT INTO discovery_sources(id,user_id,current_revision_id,created_at,updated_at) VALUES(1,1,1,1,1)")
	execConnection(t, tx, "INSERT INTO discovery_source_revisions(id,source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at) VALUES(1,1,1,1,1,1,'https://upstream.example/v1','openai',1)")
	execConnection(t, tx, "INSERT INTO discovery_source_states(source_revision_id,result_generation,updated_at) VALUES(1,1,1)")
	seedApprovedUse(t, tx, false, 1)
	execConnection(t, tx, "INSERT INTO credentials(id,user_id,current_version_id,created_at,updated_at) VALUES(2,1,2,1,1)")
	execConnection(t, tx, "INSERT INTO endpoint_key_secrets(id,context_id,encrypted_secret,key_body_review_hmac,created_at) VALUES(2,?,'nbsec:v2:fixture',?,1)", bytes.Repeat([]byte{2}, 16), bytes.Repeat([]byte{2}, 32))
	execConnection(t, tx, "INSERT INTO credential_versions(id,credential_id,version,secret_ref_id,display_head,display_tail,created_at) VALUES(2,2,1,2,'new','tail',1)")
	execConnection(t, tx, "INSERT INTO endpoint_key_grant_revisions(id,endpoint_key_id,endpoint_id,owner_user_id,credential_id,credential_version_id,grant_revision,native_service_mask,report_fingerprint,origin,created_at) VALUES(2,1,1,1,2,2,2,49,?,'explicit',2)", bytes.Repeat([]byte{2}, 32))
	execConnection(t, tx, "UPDATE endpoint_keys SET current_grant_revision_id=2,revision=2,updated_at=2 WHERE id=1")
	execConnection(t, tx, "INSERT INTO discovery_source_revisions(id,source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at) VALUES(2,1,1,2,2,2,'https://upstream.example/v1','openai',2)")
	execConnection(t, tx, "UPDATE discovery_sources SET current_revision_id=2,revision=2,updated_at=2 WHERE id=1")
	execConnection(t, tx, "UPDATE credentials SET current_version_id=NULL,enabled=0,deleted_at=3,revision=2,updated_at=3 WHERE id=2")
	retired, err := connectionstore.RetireUnreferencedVersionTx(ctx, tx, 2, 3)
	if err != nil || !retired {
		t.Fatal(retired, err)
	}
	if _, err = connectionstore.LoadSelfGrantTx(ctx, tx, 1, 1); !errors.Is(err, connectionstore.ErrUnavailable) {
		t.Fatal(err)
	}
	approved, err := connectionstore.LoadApprovedUseTx(ctx, tx, 1, connectorcontract.NativeServiceChatCompletions)
	if err != nil || approved.Grant.CredentialID != 1 {
		t.Fatal(approved, err)
	}
	source, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: 1, SourceID: 1, Purpose: connectionstore.SourceCharity, DonationUseID: 1})
	if err != nil || source.CredentialID != 1 || source.SourceRevisionID != 1 {
		t.Fatal(source, err)
	}
	if _, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, connectionstore.SourceAuthorizationRef{OwnerUserID: 1, SourceID: 1, Purpose: connectionstore.SourceSelf}); !errors.Is(err, connectionstore.ErrUnavailable) {
		t.Fatal(err)
	}
	var head, tail string
	if err := tx.QueryRow("SELECT display_head,display_tail FROM credential_versions WHERE id=2").Scan(&head, &tail); err != nil || head != "" || tail != "" {
		t.Fatal(head, tail, err)
	}
	var fingerprint [32]byte
	copy(fingerprint[:], bytes.Repeat([]byte{1}, 32))
	evidence, err := connectionstore.FindKeyCredentialEvidenceTx(ctx, tx, fingerprint, 0, 10)
	if err != nil || len(evidence) != 1 || evidence[0].CredentialVersionID != 1 {
		t.Fatal(evidence, err)
	}
	copy(fingerprint[:], bytes.Repeat([]byte{2}, 32))
	evidence, err = connectionstore.FindKeyCredentialEvidenceTx(ctx, tx, fingerprint, 0, 10)
	if err != nil || len(evidence) != 0 {
		t.Fatal(evidence, err)
	}
	if retired, err := connectionstore.RetireUnreferencedVersionTx(ctx, tx, 1, 3); err != nil || retired {
		t.Fatal(retired, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteDiscoverySourceRemovesItsRunCatalogAndPreservesOtherReferences(t *testing.T) {
	_, tx := connectionFixture(t)
	for _, id := range []int64{1, 2} {
		execConnection(t, tx, "INSERT INTO discovery_sources(id,user_id,current_revision_id,created_at,updated_at) VALUES(?,1,?,1,1)", id, id)
		execConnection(t, tx, "INSERT INTO discovery_source_revisions(id,source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at) VALUES(?,?,1,1,1,1,'https://upstream.example/v1','openai',1)", id, id)
		execConnection(t, tx, "INSERT INTO discovery_runs(id,source_revision_id,source_revision_snapshot,credential_version_id,result_generation,authorization_kind,state,started_at,completed_at,model_count) VALUES(?,?,1,1,1,'legacy','succeeded',1,2,1)", id, id)
		execConnection(t, tx, "INSERT INTO discovery_source_states(source_revision_id,result_generation,current_run_id,last_successful_run_id,updated_at) VALUES(?,1,?,?,2)", id, id, id)
		execConnection(t, tx, "INSERT INTO model_catalog_entries(discovery_run_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at,native_service_mask) VALUES(?,'automatic','1:0','model','',1,2,2,0)", id)
	}
	execConnection(t, tx, "INSERT INTO accepted_operations(id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,created_at) VALUES('op_AAAAAAAAAAAAAAAAAAAAAA','model_discovery',1,'user',?,'accepted','source:1:2',3)", bytes.Repeat([]byte{3}, 32))
	execConnection(t, tx, "INSERT INTO discovery_runs(id,source_revision_id,source_revision_snapshot,credential_version_id,operation_id,operation_hash,result_generation,authorization_kind,state,started_at) VALUES(3,1,1,1,'op_AAAAAAAAAAAAAAAAAAAAAA',?,2,'self','checking',3)", bytes.Repeat([]byte{3}, 32))
	execConnection(t, tx, "UPDATE discovery_source_states SET current_run_id=3,result_generation=2,updated_at=3 WHERE source_revision_id=1")
	if err := connectionstore.DeleteDiscoverySourceTx(t.Context(), tx, 2, 1, 3); !errors.Is(err, connectionstore.ErrNotFound) {
		t.Fatal(err)
	}
	if err := connectionstore.DeleteDiscoverySourceTx(t.Context(), tx, 1, 1, 4); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"SELECT count(*) FROM discovery_sources WHERE id=1", "SELECT count(*) FROM discovery_runs WHERE id=1", "SELECT count(*) FROM model_catalog_entries WHERE discovery_run_id=1"} {
		var count int
		if err := tx.QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatal(query, count, err)
		}
	}
	var operationState string
	var terminal int64
	if err := tx.QueryRow("SELECT state,terminal_at FROM accepted_operations WHERE id='op_AAAAAAAAAAAAAAAAAAAAAA'").Scan(&operationState, &terminal); err != nil || operationState != "completed" || terminal != 4 {
		t.Fatal(operationState, terminal, err)
	}
	var remaining int
	if err := tx.QueryRow("SELECT count(*) FROM model_catalog_entries WHERE discovery_run_id=2").Scan(&remaining); err != nil || remaining != 1 {
		t.Fatal(remaining, err)
	}
	if retired, err := connectionstore.RetireUnreferencedVersionTx(t.Context(), tx, 1, 3); err != nil || retired {
		t.Fatal(retired, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
