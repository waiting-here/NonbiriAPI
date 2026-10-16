package v1

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type generationManifest struct {
	Objects []schemaObject
	hash    string
}

func readGenerationManifest(ctx context.Context, q queryer) (generationManifest, error) {
	var manifest generationManifest
	hash, err := db.SchemaManifestHash(ctx, q)
	if err != nil {
		return manifest, err
	}
	manifest.hash = hash
	rows, err := q.QueryContext(ctx, "SELECT type,name,tbl_name,COALESCE(sql,'') FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' AND type IN ('table','index','trigger','view') ORDER BY type,name")
	if err != nil {
		return manifest, err
	}
	defer rows.Close()
	for rows.Next() {
		var object schemaObject
		if err := rows.Scan(&object.Type, &object.Name, &object.Table, &object.SQL); err != nil {
			return manifest, err
		}
		manifest.Objects = append(manifest.Objects, object)
	}
	return manifest, rows.Err()
}

func generationManifestDigest(manifest generationManifest) string { return manifest.hash }

func validateGenerationTwoManifest(ctx context.Context, q queryer) error {
	hash, err := db.SchemaManifestHash(ctx, q)
	if err != nil {
		return err
	}
	if hash != db.PinnedGenerationTwoManifestHash {
		return errors.New("unexpected target manifest")
	}
	return nil
}

func extendKnownGenerationTwoSchema(ctx context.Context, database *sql.DB) error {
	return runGenerationTwoExtension(ctx, database, func(ctx context.Context, tx *sql.Tx) error {
		plan, err := storageSchema.plan(ctx, tx)
		if err != nil {
			return err
		}
		return plan.apply(ctx, tx)
	})
}
