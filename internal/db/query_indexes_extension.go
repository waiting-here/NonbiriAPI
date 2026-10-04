package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const preQueryIndexesManifestHash = "79baedb87e0c3f8732cf68ed00b7686504032e17f147af84f21887d835c9207b"
const queryIndexesMarker = "\n-- History query indexes\n"

func preQueryIndexesSchema() string {
	previous, _, _ := strings.Cut(generationTwoSchema, queryIndexesMarker)
	return previous
}

func applyQueryIndexesExtension(ctx context.Context, tx *sql.Tx) error {
	_, additive, ok := strings.Cut(generationTwoSchema, queryIndexesMarker)
	if !ok {
		return errors.New("canonical history indexes are missing")
	}
	_, err := tx.ExecContext(ctx, additive)
	return err
}
