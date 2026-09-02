package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestGenerationTwoManifestUsesIndependentFixture(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	defer db.Close()

	manifest, err := readGenerationManifest(context.Background(), db)
	if err != nil {
		t.Fatalf("read canonical schema manifest: %v", err)
	}
	schemaHash := GenerationTwoSchemaHash()
	manifestHash := generationManifestDigest(manifest)
	t.Logf("generation-two schema sha256=%s", schemaHash)
	t.Logf("generation-two manifest sha256=%s", manifestHash)
	const previousSchemaHash = "05d8376329ac61a18d1ddd9292879d04c68d86ecad5193487c3c3e26d822bcac"
	const previousManifestHash = "cec081daae9f26bdc261ea459b74b4251635e86af93e2870266d650bc1a3ad56"
	if schemaHash == previousSchemaHash || manifestHash == previousManifestHash {
		t.Fatal("previous donation lifecycle and report-lineage schema hash remained canonical")
	}

	if err := validateGenerationTwoManifest(context.Background(), db); err != nil {
		t.Fatalf("canonical schema does not match checked-in manifest fixture: %v", err)
	}
	if _, err := db.Exec(`DROP INDEX idx_users_created`); err != nil {
		t.Fatalf("drop index for drift test: %v", err)
	}
	if err := validateGenerationTwoManifest(context.Background(), db); err == nil {
		t.Fatal("manifest validation accepted a schema object drift")
	}
}

func TestGenerationTwoFreshConfigSeedUsesIndependentGolden(t *testing.T) {
	t.Run("canonical", func(t *testing.T) {
		store := openTestStore(t, filepath.Join(privateDBDir(t), "fresh-config-canonical.sqlite"))
		if err := validateGenerationTwoFreshSeedManifest(context.Background(), store.DB()); err != nil {
			t.Fatalf("canonical fresh seed rejected: %v", err)
		}
	})

	tests := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "valid-non-default-value",
			query: `UPDATE site_config SET value='non-default-site-name' WHERE key='site_name'`,
		},
		{
			name:  "added-key",
			query: `INSERT INTO site_config(key,value,updated_at) VALUES('site_config_extra','',0)`,
		},
		{
			name:  "deleted-key",
			query: `DELETE FROM site_config WHERE key='site_name'`,
		},
		{
			name:  "updated-at-drift",
			query: `UPDATE site_config SET updated_at=1 WHERE key='site_name'`,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			store := openTestStore(t, filepath.Join(privateDBDir(t), "fresh-config-"+tt.name+".sqlite"))
			if _, err := store.DB().Exec(tt.query, tt.args...); err != nil {
				t.Fatalf("apply %s drift: %v", tt.name, err)
			}
			if err := validateGenerationTwoFreshConfigSeed(context.Background(), store.DB()); err == nil {
				t.Fatalf("fresh config golden accepted %s", tt.name)
			}
		})
	}
}
