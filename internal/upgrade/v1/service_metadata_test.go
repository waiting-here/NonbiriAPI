package v1

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestServiceMetadataConversionPreservesPopulatedSource(t *testing.T) {
	path, vault := sourceFixtureFromDDL(t, schema20SQLForTest(t), 20, stableSchema20Manifest)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	user := hostileInsertUser(t, database, "metadata-preservation", 0, 1)
	const base = "https://models.example/v1"
	keyContext, err := secret.NewGenerationTwoEndpointKeyContext(hostileBlob16(7))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := vault.SealForGenerationTwoContext([]byte("synthetic-upstream-key"), keyContext)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO endpoints(id,user_id,connector_type,base_url,created_at,updated_at) VALUES(1,?,'openai-compatible',?,1,1)`, user, base)
	mustExec(t, database, `INSERT INTO endpoint_key_secrets(id,context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(1,?,?,'openai-compatible',?,1)`, hostileBlob16(7), base, envelope)
	mustExec(t, database, `INSERT INTO endpoint_keys(id,endpoint_id,secret_ref_id,secret_fingerprint,created_at,updated_at) VALUES(1,1,1,?,1,1)`, hostileBlob32(1))
	mustExec(t, database, `INSERT INTO model_discovery_evidence(endpoint_key_id,state,revision,safe_class,safe_diag,started_at,completed_at) VALUES(1,'failed',1,'auth','credential rejected',1,2)`)
	for _, tag := range []string{"abusive_third_party", "community_charity"} {
		mustExec(t, database, `INSERT INTO admin_endpoint_tags(base_url,tag) VALUES(?,?)`, base, tag)
	}
	before := allIndexSourceFacts(t, database)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := Run(t.Context(), path, vault, Options{Apply: true})
	if err != nil || result.SourceSchemaVersion != 20 || result.TargetSchemaVersion != 28 {
		t.Fatal(result, err)
	}
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if after := allIndexSourceFacts(t, store.DB()); !reflect.DeepEqual(before, after) {
		t.Fatal("metadata conversion changed saved facts")
	}
	mustExec(t, store.DB(), `INSERT INTO admin_endpoint_tags(base_url,tag) VALUES(?,'noncommercial_gateway')`, base)
	for _, failure := range []string{"region", "unavailable"} {
		mustExec(t, store.DB(), `UPDATE model_discovery_evidence SET safe_class=? WHERE endpoint_key_id=1`, failure)
	}
	if _, err := store.DB().Exec(`UPDATE model_discovery_evidence SET safe_class='unknown' WHERE endpoint_key_id=1`); err == nil {
		t.Fatal("unknown discovery class accepted")
	}
	if _, err := store.DB().Exec(`INSERT INTO admin_endpoint_tags(base_url,tag) VALUES(?,'unknown')`, base); err == nil {
		t.Fatal("unknown tag accepted")
	}
	if err := db.ValidateCurrentStorage(t.Context(), store.DB(), vault); err != nil {
		t.Fatal(err)
	}
}

func TestServiceMetadataConversionRollsBackRebuild(t *testing.T) {
	for _, mode := range []string{"failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			path, vault := sourceFixtureFromDDL(t, schema20SQLForTest(t), 20, stableSchema20Manifest)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			_, err := ConvertOffline(ctx, path, vault, OfflineSchema{Version: 20, ManifestHash: stableSchema20Manifest}, true, nil,
				func(ctx context.Context, _ *sql.DB, tx *sql.Tx) error {
					if err := extendServiceMetadata(ctx, tx); err != nil {
						return err
					}
					if mode == "cancel" {
						cancel()
						return ctx.Err()
					}
					return errors.New("injected failure after metadata rebuild")
				})
			if err == nil || mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			hash, err := db.SchemaManifestHash(t.Context(), database)
			var version int
			versionErr := database.QueryRow("SELECT version FROM schema_state WHERE id=1").Scan(&version)
			closeErr := database.Close()
			if err != nil || versionErr != nil || closeErr != nil || hash != stableSchema20Manifest || version != 20 {
				t.Fatal("rebuild did not roll back", hash, version, err, versionErr, closeErr)
			}
			if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServiceMetadataConversionRejectsWrongIdentity(t *testing.T) {
	for _, mode := range []string{"key", "drift", "version"} {
		t.Run(mode, func(t *testing.T) {
			path, vault := sourceFixtureFromDDL(t, schema20SQLForTest(t), 20, stableSchema20Manifest)
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "key" {
				vault = testVault(t, 0x72)
			}
			if mode == "drift" {
				mustExec(t, database, "DROP INDEX idx_admin_endpoint_tags_tag")
			}
			if mode == "version" {
				mustExec(t, database, "UPDATE schema_state SET version=999")
			}
			before, err := db.SchemaManifestHash(t.Context(), database)
			if err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err == nil {
				t.Fatal("invalid source accepted")
			}
			database, err = sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			after, err := db.SchemaManifestHash(t.Context(), database)
			closeErr := database.Close()
			if err != nil || closeErr != nil || before != after {
				t.Fatal("rejection changed source", err, closeErr)
			}
		})
	}
}
