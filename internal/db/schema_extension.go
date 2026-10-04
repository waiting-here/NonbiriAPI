package db

import (
	"context"
	"database/sql"
	"errors"
)

// GenerationTwoCompatibility describes the exact structural identities accepted
// before startup recovery. It does not replace data, credential or ledger checks.
type GenerationTwoCompatibility struct {
	SchemaHash           string   `json:"schema_hash"`
	ManifestHash         string   `json:"manifest_hash"`
	SourceManifestHashes []string `json:"source_manifest_hashes"`
}

var generationTwoSourceManifestHashes = [...]string{preLedgerRetentionManifestHash, preQueryIndexesManifestHash}

// GenerationTwoCompatibilityDescriptor returns a detached read-only snapshot
// of the same migration registry used by startup validation.
func GenerationTwoCompatibilityDescriptor() GenerationTwoCompatibility {
	return GenerationTwoCompatibility{
		SchemaHash:           PinnedGenerationTwoSchemaHash,
		ManifestHash:         PinnedGenerationTwoManifestHash,
		SourceManifestHashes: append([]string(nil), generationTwoSourceManifestHashes[:]...),
	}
}

func generationTwoExtensionNeeded(ctx context.Context, q queryer) (bool, error) {
	if GenerationTwoSchemaHash() != PinnedGenerationTwoSchemaHash {
		return false, errors.New("generation-two schema hash drift")
	}
	expected, err := expectedGenerationTwoManifestHash()
	if err != nil {
		return false, err
	}
	actual, err := readGenerationManifest(ctx, q)
	if err != nil {
		return false, err
	}
	digest := generationManifestDigest(actual)
	if digest == expected {
		return false, nil
	}
	for _, source := range generationTwoSourceManifestHashes {
		if digest == source {
			return true, nil
		}
	}
	return false, errors.New("generation-two schema manifest mismatch")
}

func extendKnownGenerationTwoSchema(ctx context.Context, database *sql.DB) error {
	return runGenerationTwoExtension(ctx, database, extendGenerationTwoTransaction)
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
	// All migration statements retain ctx. Only the transaction lifetime is
	// detached so database/sql cannot return before an async rollback finishes.
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
	// BeginTx's detached context cannot provide Commit's original cancel gate.
	if err := ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func extendGenerationTwoTransaction(ctx context.Context, tx *sql.Tx) error {
	needed, err := generationTwoExtensionNeeded(ctx, tx)
	if err != nil || !needed {
		return err
	}
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) == preLedgerRetentionManifestHash {
		if err := applyLedgerRetentionExtension(ctx, tx); err != nil {
			return err
		}
	}
	if err := applyQueryIndexesExtension(ctx, tx); err != nil {
		return err
	}
	// Additive retention storage preserves existing monetary facts. Full history
	// checks remain available through offline maintenance verification.
	return validateGenerationTwoManifest(ctx, tx)
}
