package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const preIdempotencyRecoveryManifestHash = "81cf798b4e6f943f148d8c4a5ab6a7ed99eab53e0d6317aa2870878a5597cc7b"
const idempotencyRecoveryMarker = "\n-- Idempotency recovery access\n"

func preIdempotencyRecoverySchema() string {
	previous, _, _ := strings.Cut(generationTwoSchema, idempotencyRecoveryMarker)
	return previous
}

func applyIdempotencyRecoveryExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preIdempotencyRecoveryManifestHash {
		return errors.New("unrecognized idempotency recovery source manifest")
	}
	_, additive, ok := strings.Cut(generationTwoSchema, idempotencyRecoveryMarker)
	if !ok {
		return errors.New("canonical idempotency recovery schema is missing")
	}
	_, err = tx.ExecContext(ctx, additive)
	return err
}
