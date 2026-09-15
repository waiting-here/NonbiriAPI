package db

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestBlackjackUpgradeFromPreviousBinary(t *testing.T) {
	directory := os.Getenv("NONBIRI_PREVIOUS_TABLE_FIXTURES")
	if directory == "" {
		t.Skip("previous binary gate supplies fixtures")
	}
	vault, err := secret.New(bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	for _, game := range []string{"bidding", "likes"} {
		t.Run(game, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(directory, game+".db"))
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
			assertRetainedManifest(t, prior, preBlackjackManifestHash)
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
				var enabled string
				if err := store.DB().QueryRow(`SELECT value FROM site_config WHERE key='game_blackjack_enabled'`).Scan(&enabled); err != nil || enabled != "0" {
					t.Fatal("new game default", enabled, err)
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
			if err := os.WriteFile(filepath.Join(directory, game+"-upgraded.db"), upgraded, 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}
