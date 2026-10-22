package claim

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// discoveryInput creates an explicit source and active operation before a test
// exercises claim admission. Repeated attempts retain the same current run.
func (f *claimFixture) discoveryInput(input DiscoveryClaimInput) DiscoveryClaimInput {
	f.t.Helper()
	ctx := context.Background()
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	source, err := testseed.SeedDiscoverySourceTx(ctx, tx, testseed.DiscoverySourceInput{EndpointKeyID: input.Candidate.EndpointKeyID, ListFormat: "openai", At: f.clock.Load()})
	if err != nil {
		f.t.Fatal(err)
	}
	var owner, version int64
	if err := tx.QueryRow(`SELECT owner_user_id,credential_version_id FROM discovery_source_revisions WHERE id=?`, source.SourceRevisionID).Scan(&owner, &version); err != nil {
		f.t.Fatal(err)
	}
	var operationID string
	err = tx.QueryRow(`SELECT r.operation_id FROM discovery_source_states s JOIN discovery_runs r ON r.id=s.current_run_id WHERE s.source_revision_id=? AND r.state='checking'`, source.SourceRevisionID).Scan(&operationID)
	if errors.Is(err, sql.ErrNoRows) {
		operationID, err = db.GenerateOpaqueID("op_")
		if err != nil {
			f.t.Fatal(err)
		}
		hash := make([]byte, 32)
		if _, err := tx.Exec(`INSERT INTO accepted_operations(id,kind,actor_user_id,payload_hash,state,created_at) VALUES(?,'model_discovery',?,?,'running',?)`, operationID, owner, hash, f.clock.Load()); err != nil {
			f.t.Fatal(err)
		}
		result, err := tx.Exec(`INSERT INTO discovery_runs(source_revision_id,source_revision_snapshot,credential_version_id,operation_id,operation_hash,result_generation,authorization_kind,state,started_at) VALUES(?,1,?,?,?,?,'self','checking',?)`, source.SourceRevisionID, version, operationID, hash, source.ResultGeneration+1, f.clock.Load())
		if err != nil {
			f.t.Fatal(err)
		}
		runID, err := result.LastInsertId()
		if err != nil {
			f.t.Fatal(err)
		}
		if _, err := tx.Exec(`UPDATE discovery_source_states SET result_generation=?,current_run_id=?,updated_at=? WHERE source_revision_id=?`, source.ResultGeneration+1, runID, f.clock.Load(), source.SourceRevisionID); err != nil {
			f.t.Fatal(err)
		}
	} else if err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
	input.Source = connectionstore.SourceAuthorizationRef{OwnerUserID: owner, SourceID: source.SourceID, Purpose: connectionstore.SourceSelf}
	input.SourceRevisionID, input.CredentialVersionID, input.OperationID = source.SourceRevisionID, version, operationID
	return input
}
