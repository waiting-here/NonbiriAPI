package db

import (
	"context"
	"database/sql"
	"errors"
)

// This is the exact deployed schema immediately before per-model routing.
// Other incomplete or modified schemas remain invalid.
const preRoutingManifestHash = "22c5d92ad7b1b077298caa2220f41b911414248ed87acbdd7cf978eb29483c36"

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
	switch generationManifestDigest(actual) {
	case expected:
		return false, nil
	case preRoutingManifestHash:
		return true, nil
	default:
		return false, errors.New("generation-two schema manifest mismatch")
	}
}

func extendKnownGenerationTwoSchema(ctx context.Context, database *sql.DB) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	needed, err := generationTwoExtensionNeeded(ctx, tx)
	if err != nil {
		return err
	}
	if !needed {
		return nil
	}
	if _, err := tx.ExecContext(ctx, charityModelRoutingSchema); err != nil {
		return err
	}
	if err := validateGenerationTwoManifest(ctx, tx); err != nil {
		return err
	}
	if err := foreignKeyCheck(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
