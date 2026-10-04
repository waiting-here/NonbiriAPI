package db

import (
	"context"
	"testing"
)

func TestAccountProtectionSchemaPins(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	manifest, err := readGenerationManifest(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	if hash := GenerationTwoSchemaHash(); hash != PinnedGenerationTwoSchemaHash {
		t.Errorf("schema hash: %s", hash)
	}
	if hash := generationManifestDigest(manifest); hash != PinnedGenerationTwoManifestHash {
		t.Errorf("manifest hash: %s", hash)
	}
}
