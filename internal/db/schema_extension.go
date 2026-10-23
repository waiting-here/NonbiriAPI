package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// GenerationTwoCompatibility describes the exact schema accepted by this binary.
type GenerationTwoCompatibility struct {
	SchemaHash           string   `json:"schema_hash"`
	ManifestHash         string   `json:"manifest_hash"`
	SchemaVersion        int      `json:"schema_version"`
	MinimumSchemaVersion int      `json:"minimum_schema_version"`
	SourceManifestHashes []string `json:"source_manifest_hashes"`
	SourceSchemaVersions []int    `json:"source_schema_versions"`
}

const CurrentSchemaVersion = 34
const MinimumSchemaVersion = 33

// Schema33ManifestHash pins the published source accepted for an index-only upgrade.
const Schema33ManifestHash = "bf3821c938fba16a863df866b801621068722c97c57acb67191075cb3c89f550"

// EndpointKeyHistoryIndexesSQL is shared by startup and the offline upgrade chain.
const EndpointKeyHistoryIndexesSQL = `CREATE INDEX idx_request_logs_endpoint_key ON request_logs(endpoint_key_id);
CREATE INDEX idx_dispatch_claims_endpoint_key ON dispatch_claims(endpoint_key_id);
CREATE INDEX idx_report_targets_endpoint_key ON report_targets(endpoint_key_id);
CREATE INDEX idx_donation_keys_endpoint_key ON donation_keys(endpoint_key_id);
`

// Tests inject DDL failures without changing the accepted source manifest.
var startupSchemaUpgradeSQL = EndpointKeyHistoryIndexesSQL

func GenerationTwoCompatibilityDescriptor() GenerationTwoCompatibility {
	return GenerationTwoCompatibility{
		SchemaHash: PinnedGenerationTwoSchemaHash, ManifestHash: PinnedGenerationTwoManifestHash,
		SchemaVersion: CurrentSchemaVersion, MinimumSchemaVersion: MinimumSchemaVersion,
		SourceManifestHashes: []string{Schema33ManifestHash}, SourceSchemaVersions: []int{33},
	}
}

func upgradeStartupSchema(ctx context.Context, database *sql.DB, codec secret.GenerationTwoContextCodec) (result error) {
	version, err := readSchemaVersion(ctx, database)
	if err != nil || version == CurrentSchemaVersion {
		return err
	}
	if version != 33 {
		return errors.New("unsupported startup schema version")
	}
	RecordStartupStage(ctx, StageSchemaUpgrade)
	conn, err := database.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			result = errors.Join(result, err)
		}
	}()
	discard := func() {
		if err := conn.Raw(func(any) error { return driver.ErrBadConn }); err != nil && !errors.Is(err, driver.ErrBadConn) {
			result = errors.Join(result, err)
		}
	}
	// Explicit SQL keeps BEGIN and COMMIT bounded by startup, while rollback
	// uses its own cleanup deadline and finishes before the connection returns.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		result = err
		discard()
		return result
	}
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(cleanup, "ROLLBACK"); err != nil {
				result = errors.Join(result, err)
				discard()
			}
		}
	}()
	if err := ValidateDatabaseIdentity(ctx, conn); err != nil {
		return err
	}
	if err := validateGenerationTwoManifestSource(ctx, conn, true); err != nil {
		return err
	}
	if err := validateDatabaseMaterial(ctx, conn, codec); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, startupSchemaUpgradeSQL); err != nil {
		return err
	}
	changed, err := conn.ExecContext(ctx, "UPDATE schema_state SET version=? WHERE id=1 AND version=33", CurrentSchemaVersion)
	if err != nil {
		return err
	}
	if count, err := changed.RowsAffected(); err != nil || count != 1 {
		return errors.New("startup schema version changed during migration")
	}
	if err := ValidateCurrentStorage(ctx, conn, codec); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	RecordStartupCheckpoint(ctx, StageSchemaUpgrade)
	return nil
}

func readSchemaVersion(ctx context.Context, q queryer) (int, error) {
	var version int
	err := q.QueryRowContext(ctx, "SELECT version FROM schema_state WHERE id=1").Scan(&version)
	return version, err
}
