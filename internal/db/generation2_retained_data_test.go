package db

import (
	"context"
	"database/sql"
	"testing"
)

func assertRetainedManifest(t *testing.T, database *sql.DB, want string) {
	t.Helper()
	manifest, err := readGenerationManifest(context.Background(), database)
	if err != nil || generationManifestDigest(manifest) != want {
		t.Fatalf("manifest = %s, want %s: %v", generationManifestDigest(manifest), want, err)
	}
}
