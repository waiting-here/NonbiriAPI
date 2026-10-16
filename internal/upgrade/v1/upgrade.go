package v1

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/gatewaypolicy"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const stableSchema17Manifest = "7289b13cd57bb2d6a50a31ffde33cae17ceac2e1e130e0cd759dd2f0c2a98d4b"
const stableSchema18Manifest = "86c1689f93835a22c90c22234906433abe438b7883db41916b01f6553d6ded23"

var storageSchema = schemaRegistry{baseline: 17, target: db.CurrentSchemaVersion, versions: []schemaMigration{
	{version: 17, manifest: stableSchema17Manifest},
	{from: 17, version: 18, manifest: stableSchema18Manifest, apply: extendContentAndProtocols},
	{from: 18, version: 19, manifest: db.PinnedGenerationTwoManifestHash, sql: accountMFASchema},
}}

type Options struct {
	Apply               bool
	LegacyGatewayModels string
}

type Result struct {
	Status              string `json:"status"`
	SourceSchemaVersion int    `json:"source_schema_version"`
	TargetSchemaVersion int    `json:"target_schema_version"`
}

func SchemaDescriptor() db.StorageSchemaDescriptor {
	descriptor := db.SchemaDescriptor()
	for _, source := range []struct {
		version      int
		hash, commit string
	}{
		{16, SourceManifestHash, SourceCommit},
		{17, stableSchema17Manifest, "117c0cb009ab285451a3e0238ab97b383faebde3"},
		{18, stableSchema18Manifest, "0f02e60704934b589b51ca91eb34bac9c5e77c02"},
	} {
		descriptor.OfflineConversions = append(descriptor.OfflineConversions, db.OfflineConversionDescriptor{
			Tool: "nonbiri-upgrade-v1", SourceCommit: source.commit, SourceSchemaVersion: source.version,
			SourceManifestHash: source.hash, TargetSchemaVersion: db.CurrentSchemaVersion})
	}
	return descriptor
}

func Run(ctx context.Context, path string, vault *secret.Vault, options Options) (Result, error) {
	return run(ctx, path, vault, options, false)
}

func RunRC7(ctx context.Context, path string, vault *secret.Vault, options Options) (Result, error) {
	return run(ctx, path, vault, options, true)
}

func run(ctx context.Context, path string, vault *secret.Vault, options Options, rc7Only bool) (Result, error) {
	sources := []OfflineSchema{{Version: 16, ManifestHash: SourceManifestHash}}
	if !rc7Only {
		sources = append(sources, OfflineSchema{Version: 17, ManifestHash: stableSchema17Manifest}, OfflineSchema{Version: 18, ManifestHash: stableSchema18Manifest})
	}
	sourceVersion := db.CurrentSchemaVersion
	invalidLegacyConfiguration := false
	needed, err := convertOffline(ctx, path, vault, sources, options.Apply, func(ctx context.Context, database *sql.DB) error {
		if err := database.QueryRowContext(ctx, "SELECT version FROM schema_state WHERE id=1").Scan(&sourceVersion); err != nil {
			return err
		}
		if sourceVersion != 16 {
			return nil
		}
		var initialized int
		if err := database.QueryRowContext(ctx, "SELECT count(*) FROM gateway_model_capabilities_state WHERE singleton=1").Scan(&initialized); err != nil {
			return err
		}
		if initialized == 0 && strings.TrimSpace(options.LegacyGatewayModels) != "" {
			_, err := gatewaypolicy.Parse(options.LegacyGatewayModels)
			invalidLegacyConfiguration = err != nil
			return err
		}
		return nil
	}, func(ctx context.Context, database *sql.DB, tx *sql.Tx) error {
		version, err := readSchemaVersion(ctx, tx)
		if err != nil {
			return err
		}
		if version == 16 {
			return convert(ctx, database, tx, vault, options.LegacyGatewayModels)
		}
		return nil
	})
	if err != nil {
		if invalidLegacyConfiguration {
			return Result{}, errors.New("legacy Gateway model configuration is invalid")
		}
		return Result{}, err
	}
	status := "current"
	if needed {
		status = "ready"
		if options.Apply {
			status = "converted"
		}
	}
	return Result{Status: status, SourceSchemaVersion: sourceVersion, TargetSchemaVersion: db.CurrentSchemaVersion}, nil
}
