package db

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestDuelUpgradeFromReleasedBinary(t *testing.T) {
	source := os.Getenv("NONBIRI_DUAL_WALLET_FIXTURE")
	if source == "" {
		t.Skip("released-source gate supplies the fixture")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := bootstrapTestPath(t, "dual-upgrade.sqlite")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	prior, err := openSQLite(path, "ro")
	if err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, prior, preRCOneManifestHash)
	before := retainedTableImages(t, prior, nil)
	var wallets, awards int
	if err := prior.QueryRow(`SELECT COUNT(*) FROM credit_accounts WHERE kind='user' AND balance_sign<>0`).Scan(&wallets); err != nil || wallets != 4 {
		t.Fatal(wallets, err)
	}
	if err := prior.QueryRow(`SELECT COUNT(*) FROM game_checkins`).Scan(&awards); err != nil || awards != 2 {
		t.Fatal(awards, err)
	}
	if err := prior.Close(); err != nil {
		t.Fatal(err)
	}
	vault, err := secret.New(bytes.Repeat([]byte{0x42}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	for attempt := 0; attempt < 2; attempt++ {
		store, err := Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
		assertRetainedImages(t, store.DB(), before)
		for key, want := range duelConfigDefaults() {
			var value string
			if err := store.DB().QueryRow(`SELECT value FROM site_config WHERE key=?`, key).Scan(&value); err != nil || value != want {
				t.Fatal(key, value, err)
			}
		}
		tx, err := store.DB().BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateAssetLedger(context.Background(), tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := validateAssetCapacity(context.Background(), tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		before = retainedTableImages(t, store.DB(), nil)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if destination := os.Getenv("NONBIRI_DUAL_UPGRADED_FIXTURE"); destination != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
