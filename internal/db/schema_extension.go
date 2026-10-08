package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// GenerationTwoCompatibility describes accepted schema and data versions.
type GenerationTwoCompatibility struct {
	SchemaHash           string   `json:"schema_hash"`
	ManifestHash         string   `json:"manifest_hash"`
	SchemaVersion        int      `json:"schema_version"`
	SourceManifestHashes []string `json:"source_manifest_hashes"`
	SourceSchemaVersions []int    `json:"source_schema_versions"`
}

// Data-only changes also advance version, even when manifest stays unchanged.
type schemaMigration struct {
	version  int
	manifest string
	sql      string
	apply    func(context.Context, *sql.Tx) error
}

type schemaBridge struct {
	manifest string
	sql      string
}

type schemaRegistry struct {
	versions []schemaMigration
	bridges  []schemaBridge
}

// Freeze the baseline and each successor at its first stable publication.
// Fresh DDL can evolve; published migration steps remain immutable.
var storageSchema = schemaRegistry{
	versions: []schemaMigration{
		{version: 1, manifest: baselineManifestHash},
		{version: 2, manifest: terminalReservationIndexesManifestHash, sql: terminalReservationIndexesSQL},
		{version: 3, manifest: aiPlayersManifestHash, sql: aiPlayersSQL},
		{version: 4, manifest: managementAndGamesManifestHash, sql: managementAndGamesSQL},
		{version: 5, manifest: gwentAIManifestHash, sql: gwentAISQL},
		{version: 6, manifest: PinnedGenerationTwoManifestHash, sql: discordGateSQL},
	},
	bridges: preReleaseSchemaBridges(),
}

func GenerationTwoCompatibilityDescriptor() GenerationTwoCompatibility {
	out := GenerationTwoCompatibility{
		SchemaHash: PinnedGenerationTwoSchemaHash, ManifestHash: PinnedGenerationTwoManifestHash,
		SchemaVersion: len(storageSchema.versions), SourceManifestHashes: []string{}, SourceSchemaVersions: []int{},
	}
	seen := map[string]bool{}
	add := func(hash string) {
		if !seen[hash] {
			out.SourceManifestHashes = append(out.SourceManifestHashes, hash)
			seen[hash] = true
		}
	}
	for _, bridge := range storageSchema.bridges {
		add(bridge.manifest)
	}
	for _, version := range storageSchema.versions[:len(storageSchema.versions)-1] {
		add(version.manifest)
		out.SourceSchemaVersions = append(out.SourceSchemaVersions, version.version)
	}
	return out
}

func (registry schemaRegistry) validate() error {
	if len(registry.versions) == 0 {
		return errors.New("schema baseline is missing")
	}
	validHash := func(value string) bool {
		raw, err := hex.DecodeString(value)
		return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == value
	}
	for i, step := range registry.versions {
		if step.version != i+1 || !validHash(step.manifest) ||
			i == 0 && (step.sql != "" || step.apply != nil) ||
			i > 0 && step.sql == "" && step.apply == nil {
			return errors.New("schema migration chain is incomplete")
		}
	}
	seen := map[string]bool{}
	for _, bridge := range registry.bridges {
		if !validHash(bridge.manifest) || bridge.sql == "" || seen[bridge.manifest] {
			return errors.New("invalid prerelease schema bridge")
		}
		seen[bridge.manifest] = true
	}
	return nil
}

type schemaPlan struct {
	bridge *schemaBridge
	steps  []schemaMigration
	target schemaMigration
}

func (plan schemaPlan) needed() bool { return plan.bridge != nil || len(plan.steps) != 0 }

func (registry schemaRegistry) plan(ctx context.Context, q queryer) (schemaPlan, error) {
	var plan schemaPlan
	if err := registry.validate(); err != nil {
		return plan, err
	}
	manifest, err := readGenerationManifest(ctx, q)
	if err != nil {
		return plan, err
	}
	hash := generationManifestDigest(manifest)
	plan.target = registry.versions[len(registry.versions)-1]
	versioned := false
	for _, table := range manifest.Tables {
		versioned = versioned || table.Name == "schema_state"
	}
	if !versioned {
		for i := range registry.bridges {
			if registry.bridges[i].manifest == hash {
				plan.bridge = &registry.bridges[i]
				plan.steps = registry.versions[1:]
				return plan, nil
			}
		}
		return plan, errors.New("unrecognized prerelease schema")
	}
	version, err := readSchemaVersion(ctx, q)
	if err != nil {
		return plan, err
	}
	if version < 1 || version > len(registry.versions) || registry.versions[version-1].manifest != hash {
		return plan, errors.New("unsupported schema version or manifest")
	}
	plan.steps = registry.versions[version:]
	return plan, nil
}

func readSchemaVersion(ctx context.Context, q queryer) (int, error) {
	var version int
	err := q.QueryRowContext(ctx, "SELECT version FROM schema_state WHERE id=1").Scan(&version)
	return version, err
}

func (plan schemaPlan) apply(ctx context.Context, tx *sql.Tx) error {
	if !plan.needed() {
		return nil
	}
	if plan.bridge != nil {
		if _, err := tx.ExecContext(ctx, plan.bridge.sql); err != nil {
			return fmt.Errorf("initialize stable storage: %w", err)
		}
	}
	for _, step := range plan.steps {
		if step.sql != "" {
			if _, err := tx.ExecContext(ctx, step.sql); err != nil {
				return fmt.Errorf("migrate storage to %d: %w", step.version, err)
			}
		}
		if step.apply != nil {
			if err := step.apply(ctx, tx); err != nil {
				return fmt.Errorf("migrate data to %d: %w", step.version, err)
			}
		}
		result, err := tx.ExecContext(ctx, "UPDATE schema_state SET version=? WHERE id=1 AND version=?", step.version, step.version-1)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return errors.New("schema version changed during migration")
		}
	}
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	version, err := readSchemaVersion(ctx, tx)
	if err != nil {
		return err
	}
	if version != plan.target.version || generationManifestDigest(manifest) != plan.target.manifest {
		return errors.New("schema migration did not reach its target")
	}
	return nil
}

func currentSchemaPlan(ctx context.Context, q queryer) (schemaPlan, error) {
	if GenerationTwoSchemaHash() != PinnedGenerationTwoSchemaHash {
		return schemaPlan{}, errors.New("generation-two schema hash drift")
	}
	expected, err := expectedGenerationTwoManifestHash()
	if err != nil {
		return schemaPlan{}, err
	}
	if len(storageSchema.versions) == 0 || storageSchema.versions[len(storageSchema.versions)-1].manifest != expected {
		return schemaPlan{}, errors.New("schema registry target differs from fresh storage")
	}
	return storageSchema.plan(ctx, q)
}

func generationTwoExtensionNeeded(ctx context.Context, q queryer) (bool, error) {
	plan, err := currentSchemaPlan(ctx, q)
	return plan.needed(), err
}

func extendKnownGenerationTwoSchema(ctx context.Context, database *sql.DB) error {
	return runGenerationTwoExtension(ctx, database, extendGenerationTwoTransaction)
}

func extendGenerationTwoTransaction(ctx context.Context, tx *sql.Tx) error {
	plan, err := currentSchemaPlan(ctx, tx)
	if err != nil {
		return err
	}
	return plan.apply(ctx, tx)
}

func upgradeStartupSchema(ctx context.Context, database *sql.DB, secrets secret.GenerationTwoContextCodec) error {
	needed, err := generationTwoExtensionNeeded(ctx, database)
	if err != nil || !needed {
		return err
	}
	return runGenerationTwoExtension(ctx, database, func(ctx context.Context, tx *sql.Tx) error {
		plan, err := currentSchemaPlan(ctx, tx)
		if err != nil || !plan.needed() {
			return err
		}
		if err := plan.apply(ctx, tx); err != nil {
			return err
		}
		// Predecessors may lack new keys or fixed rows. Validate the target
		// inside the migration transaction, before any change can commit.
		if err := validateStartupSeed(ctx, tx); err != nil {
			return err
		}
		if err := validateSourceConfig(ctx, tx); err != nil {
			return err
		}
		return validateEndpointKeyEnvelopes(ctx, tx, secrets)
	})
}

// Startup finishes rollback before returning, including after cancellation.
func runGenerationTwoExtension(ctx context.Context, database *sql.DB, extend func(context.Context, *sql.Tx) error) (result error) {
	conn, err := database.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Rebuilding a referenced table must not run ON DELETE actions. This
	// startup-owned connection restores its setting after commit or rollback;
	// the complete target's references are checked before committing.
	var foreignKeys int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), fmt.Sprintf("PRAGMA foreign_keys=%d", foreignKeys)); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			result = errors.Join(result, err)
		}
	}()
	// Statements retain ctx; the detached transaction lifetime prevents an
	// asynchronous database/sql rollback from outliving the startup result.
	tx, err := conn.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			result = errors.Join(result, err)
		}
	}()
	if err := extend(ctx, tx); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	invalid := rows.Next()
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if invalid {
		return errors.New("schema migration left invalid references")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}
