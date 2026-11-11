package lifecycle

import (
	"errors"
	"reflect"
	"testing"
)

func TestConnectionExportRetainedOwnersAndMixedEvidenceLimit(t *testing.T) {
	f := newLifecycleTestFixture(t, 100)
	owner := seedLifecycleUser(t, f.store.DB(), "export-owner", false, 1)
	other := seedLifecycleUser(t, f.store.DB(), "export-other", false, 1)
	tx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	seedExportConnection(t, tx, owner)
	seedExportConnection(t, tx, other)
	for _, query := range []string{
		`INSERT INTO endpoint_key_grant_revisions(endpoint_key_id,endpoint_id,owner_user_id,credential_id,credential_version_id,grant_revision,native_service_mask,rerank_format,report_fingerprint,origin,created_at) SELECT endpoint_key_id,endpoint_id,owner_user_id,credential_id,credential_version_id,2,native_service_mask,rerank_format,report_fingerprint,origin,created_at FROM endpoint_key_grant_revisions WHERE owner_user_id=?`,
		`INSERT INTO discovery_source_revisions(source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at) SELECT source_id,owner_user_id,2,credential_id,credential_version_id,canonical_base_url,list_format,created_at FROM discovery_source_revisions WHERE owner_user_id=?`,
		`INSERT INTO model_catalog_entries(endpoint_key_id,source_type,source_identity,normalized_model_id,source_revision,native_service_mask,created_at,updated_at) SELECT k.id,'manual','manual-a','manual-a',1,0,2,2 FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE e.user_id=?`,
		`INSERT INTO model_catalog_entries(endpoint_key_id,source_type,source_identity,normalized_model_id,source_revision,native_service_mask,created_at,updated_at) SELECT k.id,'manual','manual-b','manual-b',1,0,2,2 FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE e.user_id=?`,
	} {
		if _, err := tx.ExecContext(t.Context(), query, owner); err != nil {
			t.Fatal(err)
		}
	}
	// A retained legacy run whose source was removed has no export owner.
	result, err := tx.ExecContext(t.Context(), `INSERT INTO discovery_source_revisions(source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at) SELECT source_id,owner_user_id,3,credential_id,credential_version_id,canonical_base_url,list_format,created_at FROM discovery_source_revisions WHERE owner_user_id=? AND source_revision=1`, owner)
	if err != nil {
		t.Fatal(err)
	}
	orphanRevision, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	result, err = tx.ExecContext(t.Context(), `INSERT INTO discovery_runs(source_revision_id,source_revision_snapshot,credential_version_id,result_generation,authorization_kind,state,started_at,completed_at,model_count) SELECT id,source_revision,credential_version_id,1,'legacy','succeeded',2,2,1 FROM discovery_source_revisions WHERE id=?`, orphanRevision)
	if err != nil {
		t.Fatal(err)
	}
	run, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO model_catalog_entries(discovery_run_id,source_type,source_identity,normalized_model_id,source_revision,native_service_mask,created_at,updated_at) VALUES(?,'automatic','1:0','unowned',1,0,2,2)`, run); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM discovery_source_revisions WHERE id=?`, orphanRevision); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.QueryContext(t.Context(), `SELECT CAST(c.id AS TEXT) FROM model_catalog_entries c LEFT JOIN endpoint_keys k ON k.id=c.endpoint_key_id LEFT JOIN endpoints e ON e.id=k.endpoint_id LEFT JOIN discovery_runs r ON r.id=c.discovery_run_id LEFT JOIN discovery_source_revisions sr ON sr.id=r.source_revision_id WHERE COALESCE(e.user_id,sr.owner_user_id)=? ORDER BY c.id`, owner)
	if err != nil {
		t.Fatal(err)
	}
	var expected []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		expected = append(expected, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	out, err := exportConnections(t.Context(), tx, ExportRequest{UserID: owner, Limit: 10000})
	if err != nil {
		t.Fatal(err)
	}
	var actual []string
	for _, item := range out.NativeServiceEvidence {
		actual = append(actual, item.CatalogEntryID)
	}
	if len(expected) != 3 || !reflect.DeepEqual(actual, expected) || len(out.Grants) != 2 || len(out.SourceRevisions) != 2 {
		t.Fatal("retained history or evidence ownership/order changed", actual, expected, len(out.Grants), len(out.SourceRevisions))
	}
	if _, err := exportConnections(t.Context(), tx, ExportRequest{UserID: owner, Limit: 2}); !errors.Is(err, ErrTooLarge) {
		t.Fatal("mixed evidence overflow was not rejected", err)
	}
}
