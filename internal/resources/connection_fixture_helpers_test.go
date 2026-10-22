package resources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func seedResourceBindingTx(t *testing.T, tx *sql.Tx, modelID, keyID int64, upstream string, order int) {
	t.Helper()
	if _, err := testseed.InsertModelBindingTx(context.Background(), tx, testseed.BindingInput{ModelID: modelID, KeyID: keyID, UpstreamModelID: upstream, Order: order, At: resourceTestNow}); err != nil {
		t.Fatal(err)
	}
}

func seedResourceCheckingDiscoveryTx(t *testing.T, tx *sql.Tx, keyID int64, operationID any, operationHash []byte, startedAt int64, diagnostic string) testseed.DiscoveryCatalogIdentity {
	t.Helper()
	source, err := testseed.SeedDiscoverySourceTx(context.Background(), tx, testseed.DiscoverySourceInput{EndpointKeyID: keyID, ListFormat: "openai", At: startedAt})
	if err != nil {
		t.Fatal(err)
	}
	if operationID == nil {
		id, err := db.GenerateOpaqueID("op_")
		if err != nil {
			t.Fatal(err)
		}
		operationID = id
		if operationHash == nil {
			hash := sha256.Sum256([]byte(id))
			operationHash = hash[:]
		}
		if _, err := tx.Exec(`INSERT INTO accepted_operations(id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,created_at) SELECT ?,'model_discovery',user_id,'user',?,'accepted','',? FROM discovery_sources WHERE id=?`, id, operationHash, startedAt, source.SourceID); err != nil {
			t.Fatal(err)
		}
	}
	source.ResultGeneration++
	result, err := tx.Exec(`INSERT INTO discovery_runs(source_revision_id,source_revision_snapshot,credential_version_id,operation_id,operation_hash,result_generation,authorization_kind,state,safe_class,safe_diagnostic,started_at,model_count)
 SELECT id,source_revision,credential_version_id,?,?,?,'self','checking','none',?,?,0 FROM discovery_source_revisions WHERE id=?`, operationID, operationHash, source.ResultGeneration, diagnostic, startedAt, source.SourceRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	source.RunID, err = result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE discovery_source_states SET result_generation=?,current_run_id=?,updated_at=? WHERE source_revision_id=?`, source.ResultGeneration, source.RunID, startedAt, source.SourceRevisionID); err != nil {
		t.Fatal(err)
	}
	return source
}
