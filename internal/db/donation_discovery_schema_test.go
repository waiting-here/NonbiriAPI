package db

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const previousDiscoveryRunGuardSQL = `CREATE TRIGGER discovery_run_version_guard BEFORE INSERT ON discovery_runs
WHEN NEW.authorization_kind<>'legacy' AND NOT EXISTS(SELECT 1 FROM discovery_source_authorizations a WHERE a.source_revision_id=NEW.source_revision_id AND a.source_revision=NEW.source_revision_snapshot AND a.credential_version_id=NEW.credential_version_id AND a.available=1 AND ((NEW.authorization_kind='self' AND EXISTS(SELECT 1 FROM discovery_sources ds WHERE ds.id=a.source_id AND ds.current_revision_id=a.source_revision_id)) OR (NEW.authorization_kind='donation_use' AND EXISTS(SELECT 1 FROM donation_uses u JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id AND ur.use_id=u.id JOIN charity_use_availability av ON av.use_id=u.id WHERE u.id=NEW.authorization_use_id AND ur.id=NEW.authorization_use_revision_id AND ur.discovery_source_revision_id=a.source_revision_id AND u.discovery_enabled=1 AND av.credential_version_id=a.credential_version_id))))
BEGIN SELECT RAISE(ABORT,'discovery run authorization mismatch'); END;`

func schema34CatalogFixture(t *testing.T) (string, *secret.Vault, *Store) {
	t.Helper()
	path := bootstrapTestPath(t, "schema34.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
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
	if hash, err := SchemaManifestHash(t.Context(), store.DB()); err != nil || hash != Schema34ManifestHash {
		t.Fatalf("source identity: %s %v", hash, err)
	}
	return path, vault, store
}

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
