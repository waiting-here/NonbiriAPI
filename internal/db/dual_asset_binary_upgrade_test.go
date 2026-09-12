package db

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestDualAssetUpgradeFromReleasedBinary(t *testing.T) {
	source := os.Getenv("NONBIRI_LEGACY_FIXTURE")
	if source == "" {
		t.Skip("standalone upgrade gate provides the released-binary fixture")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := bootstrapTestPath(t, "upgrade.sqlite")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	prior, err := openSQLite(path, "ro")
	if err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, prior, preBetaFourManifestHash)
	before := retainedTableImages(t, prior, nil)
	var users int
	if err := prior.QueryRow(`SELECT COUNT(*) FROM credit_accounts WHERE kind='user'`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 2 {
		t.Fatalf("expected nonempty source, got %d wallets", users)
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
		defer store.Close()
		assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
		assertRetainedImages(t, store.DB(), before)
		var games, gameEntries, legacyWelfare int
		if err := store.DB().QueryRow(`SELECT COUNT(*) FROM credit_accounts WHERE kind='user' AND asset_type='game' AND balance_sign=0`).Scan(&games); err != nil || games != users {
			t.Fatal("missing zero game wallets", games, err)
		}
		if err := store.DB().QueryRow(`SELECT COUNT(*) FROM credit_entries WHERE asset_type='game'`).Scan(&gameEntries); err != nil || gameEntries != 0 {
			t.Fatal("upgrade created game money", gameEntries, err)
		}
		if err := store.DB().QueryRow(`SELECT COUNT(*) FROM welfare_claims WHERE asset_type='general'`).Scan(&legacyWelfare); err != nil || legacyWelfare != 1 {
			t.Fatal("old welfare claim changed", legacyWelfare, err)
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
		if attempt == 0 {
			before = retainedTableImages(t, store.DB(), nil)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if destination := os.Getenv("NONBIRI_UPGRADED_FIXTURE"); destination != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
