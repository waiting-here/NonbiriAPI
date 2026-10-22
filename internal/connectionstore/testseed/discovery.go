package testseed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type DiscoveryModel struct {
	ID, Provider   string
	NativeServices connectorcontract.NativeServiceSet
	EvidenceSource string
}
type DiscoveryCatalogInput struct {
	EndpointKeyID       int64
	BaseURL, ListFormat string
	Models              []DiscoveryModel
	At                  int64
}
type DiscoveryCatalogIdentity struct {
	SourceID, SourceRevisionID, RunID, ResultGeneration int64
}

type DiscoverySourceInput struct {
	EndpointKeyID       int64
	BaseURL, ListFormat string
	At                  int64
}

// SeedDiscoverySourceTx creates or verifies an exact self discovery source.
// A newly created source has unknown result generation 1 and no run.
func SeedDiscoverySourceTx(ctx context.Context, tx *sql.Tx, in DiscoverySourceInput) (DiscoveryCatalogIdentity, error) {
	var out DiscoveryCatalogIdentity
	if tx == nil || in.EndpointKeyID <= 0 || in.At < 0 || in.At > 253402300799 || in.ListFormat == "" {
		return out, errors.New("invalid discovery fixture")
	}
	var owner, credential, version, grant int64
	var endpointBase string
	if err := tx.QueryRowContext(ctx, "SELECT e.user_id,e.base_url,g.credential_id,g.credential_version_id,g.id FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id JOIN endpoint_key_grant_revisions g ON g.id=k.current_grant_revision_id WHERE k.id=?", in.EndpointKeyID).Scan(&owner, &endpointBase, &credential, &version, &grant); err != nil {
		return out, err
	}
	err := tx.QueryRowContext(ctx, "SELECT source_id,source_revision_id FROM endpoint_key_discovery_sources WHERE endpoint_key_id=?", in.EndpointKeyID).Scan(&out.SourceID, &out.SourceRevisionID)
	if errors.Is(err, sql.ErrNoRows) {
		out.SourceID, err = nextID(ctx, tx, "discovery_sources")
		if err != nil {
			return out, err
		}
		out.SourceRevisionID, err = nextID(ctx, tx, "discovery_source_revisions")
		if err != nil {
			return out, err
		}
		base := in.BaseURL
		if base == "" {
			base = endpointBase
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO discovery_sources(id,user_id,current_revision_id,created_at,updated_at) VALUES(?,?,?,?,?)", out.SourceID, owner, out.SourceRevisionID, in.At, in.At); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO discovery_source_revisions(id,source_id,owner_user_id,source_revision,credential_id,credential_version_id,canonical_base_url,list_format,created_at) VALUES(?,?,?,1,?,?,?,?,?)", out.SourceRevisionID, out.SourceID, owner, credential, version, base, in.ListFormat, in.At); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO discovery_source_states(source_revision_id,result_generation,updated_at) VALUES(?,1,?)", out.SourceRevisionID, in.At); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO endpoint_key_discovery_sources(endpoint_key_id,source_id,grant_revision_id,source_revision_id,credential_version_id) VALUES(?,?,?,?,?)", in.EndpointKeyID, out.SourceID, grant, out.SourceRevisionID, version); err != nil {
			return out, err
		}
	} else if err != nil {
		return out, err
	}
	var sourceVersion, sourceRevision, generation int64
	var base, format string
	if err = tx.QueryRowContext(ctx, "SELECT sr.credential_version_id,sr.source_revision,sr.canonical_base_url,sr.list_format,st.result_generation FROM discovery_source_revisions sr JOIN discovery_source_states st ON st.source_revision_id=sr.id WHERE sr.id=?", out.SourceRevisionID).Scan(&sourceVersion, &sourceRevision, &base, &format, &generation); err != nil {
		return out, err
	}
	if sourceVersion != version || format != in.ListFormat || in.BaseURL != "" && in.BaseURL != base || generation == int64(^uint64(0)>>1) {
		return out, errors.New("discovery fixture source mismatch")
	}
	out.ResultGeneration = generation
	return out, nil
}

// SeedDiscoveryCatalogTx creates an explicit source authorization and successful
// run, or appends a successful run to the key's existing exact source. It does
// not share discovery with donation uses; seed those explicitly afterward.
func SeedDiscoveryCatalogTx(ctx context.Context, tx *sql.Tx, in DiscoveryCatalogInput) (DiscoveryCatalogIdentity, error) {
	if len(in.Models) > 10000 {
		return DiscoveryCatalogIdentity{}, errors.New("too many discovery fixture models")
	}
	out, err := SeedDiscoverySourceTx(ctx, tx, DiscoverySourceInput{EndpointKeyID: in.EndpointKeyID, BaseURL: in.BaseURL, ListFormat: in.ListFormat, At: in.At})
	if err != nil {
		return out, err
	}
	var sourceVersion, sourceRevision int64
	if err = tx.QueryRowContext(ctx, "SELECT credential_version_id,source_revision FROM discovery_source_revisions WHERE id=?", out.SourceRevisionID).Scan(&sourceVersion, &sourceRevision); err != nil {
		return out, err
	}
	generation := out.ResultGeneration

	out.ResultGeneration = generation + 1
	result, err := tx.ExecContext(ctx, "INSERT INTO discovery_runs(source_revision_id,source_revision_snapshot,credential_version_id,result_generation,authorization_kind,state,started_at,completed_at,model_count) VALUES(?,?,?,?,'self','succeeded',?,?,?)", out.SourceRevisionID, sourceRevision, sourceVersion, out.ResultGeneration, in.At, in.At, len(in.Models))
	if err != nil {
		return out, err
	}
	out.RunID, err = result.LastInsertId()
	if err != nil {
		return out, err
	}
	for index, model := range in.Models {
		evidence := model.EvidenceSource
		if evidence == "" {
			evidence = "unknown"
			if !model.NativeServices.Empty() {
				evidence = "metadata"
			}
		}
		var evidenceRevision any
		if evidence != "unknown" {
			evidenceRevision = out.ResultGeneration
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO model_catalog_entries(discovery_run_id,source_type,source_identity,normalized_model_id,provider,source_revision,native_service_mask,evidence_source,evidence_revision,created_at,updated_at) VALUES(?,'automatic',?,?,?,?,?,?,?,?,?)", out.RunID, fmt.Sprintf("%d:%d", out.ResultGeneration, index), model.ID, model.Provider, out.ResultGeneration, model.NativeServices.Mask(), evidence, evidenceRevision, in.At, in.At); err != nil {
			return out, err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE discovery_source_states SET result_generation=?,current_run_id=?,last_successful_run_id=?,updated_at=? WHERE source_revision_id=?", out.ResultGeneration, out.RunID, out.RunID, in.At, out.SourceRevisionID)
	return out, err
}
