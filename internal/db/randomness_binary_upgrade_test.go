package db

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestRandomnessUpgradeFromPreviousBinary(t *testing.T) {
	directory := os.Getenv("NONBIRI_RANDOMNESS_FIXTURES")
	if directory == "" {
		t.Skip("previous binary gate supplies fixtures")
	}
	vault, err := secret.New(bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	for _, phase := range []string{"seating", "decision", "result"} {
		t.Run(phase, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(directory, phase+".db"))
			if err != nil {
				t.Fatal(err)
			}
			path := bootstrapTestPath(t, "upgrade.sqlite")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			prior, err := openSQLite(path, "ro")
			if err != nil {
				t.Fatal(err)
			}
			assertRetainedManifest(t, prior, preRandomnessManifestHash)
			before := retainedTableImages(t, prior, nil)
			if err := prior.Close(); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				store, err := Open(path, vault)
				if err != nil {
					t.Fatal(err)
				}
				assertRetainedImages(t, store.DB(), before)
				assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
				var count int
				if err := store.DB().QueryRow(`SELECT COUNT(*) FROM game_random_proofs`).Scan(&count); err != nil || count != 0 {
					t.Fatal("historical proof invented", count, err)
				}
				tx, err := store.DB().BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := ValidateAssetLedger(context.Background(), tx); err != nil {
					t.Fatal(err)
				}
				if err := validateAssetCapacity(context.Background(), tx); err != nil {
					t.Fatal(err)
				}
				tx.Rollback()
				before = retainedTableImages(t, store.DB(), nil)
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			upgraded, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, phase+"-upgraded.db"), upgraded, 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}
