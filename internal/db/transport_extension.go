package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const preTransportManifestHash = "950934669d973f4329edc39768d413fccd085f284fe533982e4c98f86d99dc00"
const transportRulesMarker = "\n-- Model chat transport rules\n"

func preTransportSchema() string {
	previous, _, _ := strings.Cut(generationTwoSchema, transportRulesMarker)
	return previous
}

func applyTransportExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preTransportManifestHash {
		return errors.New("unrecognized model transport source manifest")
	}
	_, additive, ok := strings.Cut(generationTwoSchema, transportRulesMarker)
	if !ok {
		return errors.New("canonical model transport schema is missing")
	}
	_, err = tx.ExecContext(ctx, additive)
	return err
}
