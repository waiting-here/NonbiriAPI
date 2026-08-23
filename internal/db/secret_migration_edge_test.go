package db

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestEndpointCredentialMigrationRejectsMissingContextAndBadOrigin(t *testing.T) {
	t.Run("orphaned key", func(t *testing.T) {
		vault := migrationVault(t)
		store := openMigrationStore(t, filepath.Join(t.TempDir(), "orphan.db"), vault)
		defer store.Close()
		material := []byte("orphan-secret")
		ciphertext, err := vault.Seal(material)
		clear(material)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB().Exec(`PRAGMA foreign_keys=OFF`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB().Exec(`INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, created_at, updated_at) VALUES (999, ?, 1, 1)`, ciphertext); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB().Exec(`PRAGMA foreign_keys=ON`); err != nil {
			t.Fatal(err)
		}
		if err := store.MigrateEndpointKeyEnvelopes(context.Background()); err != ErrEndpointCredentialMigration {
			t.Fatalf("orphan migration error=%v", err)
		}
		if got := storedEndpointCiphertext(t, store, 1); got != ciphertext {
			t.Fatal("orphan failure changed ciphertext")
		}
	})

	t.Run("malformed stored origin", func(t *testing.T) {
		vault := migrationVault(t)
		store := openMigrationStore(t, filepath.Join(t.TempDir(), "origin.db"), vault)
		defer store.Close()
		userID := seedTestUser(t, store, "origin-owner", nil)
		first := mustCreateTestEndpoint(t, store, userID, "https://valid-origin.example/v1")
		firstID, firstBefore := insertLegacyEndpointKey(t, store, vault, first.ID, "first-origin-secret")
		second := mustCreateTestEndpoint(t, store, userID, "https://second-origin.example/v1")
		if _, err := store.DB().Exec(`UPDATE endpoints SET base_url='https://origin-context-marker.example:bad/path' WHERE id=?`, second.ID); err != nil {
			t.Fatal(err)
		}
		insertLegacyEndpointKey(t, store, vault, second.ID, "second-origin-secret")

		err := store.MigrateEndpointKeyEnvelopes(context.Background())
		if err != ErrEndpointCredentialMigration || strings.Contains(err.Error(), "origin-context-marker") {
			t.Fatalf("malformed-origin migration is not opaque: %v", err)
		}
		if got := storedEndpointCiphertext(t, store, firstID); got != firstBefore {
			t.Fatal("malformed origin left a partial migration")
		}
	})
}

func TestEndpointCredentialMigrationClosedVaultAndCanceledContextRollback(t *testing.T) {
	t.Run("closed vault", func(t *testing.T) {
		key := bytes.Repeat([]byte{0x91}, secret.MasterKeyBytes)
		vault, err := secret.New(key)
		clear(key)
		if err != nil {
			t.Fatal(err)
		}
		store := openMigrationStore(t, filepath.Join(t.TempDir(), "closed.db"), vault)
		defer store.Close()
		userID := seedTestUser(t, store, "closed-owner", nil)
		endpoint := mustCreateTestEndpoint(t, store, userID, "https://closed.example/v1")
		keyID, before := insertLegacyEndpointKey(t, store, vault, endpoint.ID, "closed-vault-secret")
		if err := vault.Close(); err != nil {
			t.Fatal(err)
		}
		if err := store.MigrateEndpointKeyEnvelopes(context.Background()); err != ErrEndpointCredentialMigration {
			t.Fatalf("closed-vault migration error=%v", err)
		}
		if got := storedEndpointCiphertext(t, store, keyID); got != before {
			t.Fatal("closed-vault migration changed a row")
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		vault := migrationVault(t)
		store := openMigrationStore(t, filepath.Join(t.TempDir(), "canceled.db"), vault)
		defer store.Close()
		userID := seedTestUser(t, store, "canceled-owner", nil)
		endpoint := mustCreateTestEndpoint(t, store, userID, "https://canceled.example/v1")
		keyID, before := insertLegacyEndpointKey(t, store, vault, endpoint.ID, "canceled-secret")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := store.MigrateEndpointKeyEnvelopes(ctx); !errors.Is(err, ErrEndpointCredentialMigration) {
			t.Fatalf("canceled migration error=%v", err)
		}
		if got := storedEndpointCiphertext(t, store, keyID); got != before {
			t.Fatal("canceled migration changed a row")
		}
	})
}

func TestCreateEndpointKeyClosedVaultRollsBackAndClearsInput(t *testing.T) {
	key := bytes.Repeat([]byte{0x92}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	store := openMigrationStore(t, filepath.Join(t.TempDir(), "closed-create.db"), vault)
	defer store.Close()
	userID := seedTestUser(t, store, "closed-create-owner", nil)
	endpoint := mustCreateTestEndpoint(t, store, userID, "https://closed-create.example/v1")
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("closed-create-secret")
	if _, err := store.CreateEndpointKey(context.Background(), userID, endpoint.ID, plaintext, "", "", "", true, 1); err == nil {
		t.Fatal("create with a closed vault succeeded")
	}
	for _, b := range plaintext {
		if b != 0 {
			t.Fatal("closed-vault create did not clear plaintext")
		}
	}
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_keys`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("closed-vault create left count=%d err=%v", count, err)
	}
}
