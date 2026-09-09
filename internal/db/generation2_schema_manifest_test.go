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
	const previousSchemaHash = "17e56090fc9be4a8d183b20b2d630424d128caee196e83df920e6c0ba0ac2214"
	const previousManifestHash = "4950042e2cec6d7a0fabf6f4cc3591322825da57b40a0cb8c27c1eef502a540c"
	if schemaHash == previousSchemaHash || manifestHash == previousManifestHash {
		t.Fatal("previous fishing presentation hash remained canonical")
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
