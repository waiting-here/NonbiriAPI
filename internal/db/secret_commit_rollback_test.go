package db

import (
	"context"
	"path/filepath"
	"testing"
)

const deferredCredentialFailureSchema = `
CREATE TABLE deferred_credential_guard (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER fail_credential_transaction_at_commit
AFTER UPDATE OF encrypted_secret ON endpoint_keys
BEGIN
  INSERT INTO deferred_credential_guard (user_id) VALUES (9223372036854775807);
END;`

func TestCreateEndpointKeyCommitFailureLeavesNoPlaceholder(t *testing.T) {
	vault := migrationVault(t)
	store := openMigrationStore(t, filepath.Join(t.TempDir(), "create-commit.db"), vault)
	defer store.Close()
	userID := seedTestUser(t, store, "create-commit-owner", nil)
	endpoint := mustCreateTestEndpoint(t, store, userID, "https://create-commit.example/v1")
	if _, err := store.DB().Exec(deferredCredentialFailureSchema); err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("create-commit-secret")
	if _, err := store.CreateEndpointKey(context.Background(), userID, endpoint.ID, plaintext, "", "", "", true, 1); err == nil {
		t.Fatal("deferred commit failure unexpectedly succeeded")
	}
	for _, b := range plaintext {
		if b != 0 {
			t.Fatal("commit failure did not clear plaintext")
		}
	}
	var keys, guards int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_keys`).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM deferred_credential_guard`).Scan(&guards); err != nil {
		t.Fatal(err)
	}
	if keys != 0 || guards != 0 {
		t.Fatalf("failed commit left keys=%d deferred_rows=%d", keys, guards)
	}
}

func TestEndpointCredentialMigrationCommitFailureRollsBackBatch(t *testing.T) {
	vault := migrationVault(t)
	store := openMigrationStore(t, filepath.Join(t.TempDir(), "migration-commit.db"), vault)
	defer store.Close()
	userID := seedTestUser(t, store, "migration-commit-owner", nil)
	endpoint := mustCreateTestEndpoint(t, store, userID, "https://migration-commit.example/v1")
	firstID, firstBefore := insertLegacyEndpointKey(t, store, vault, endpoint.ID, "migration-commit-first")
	secondID, secondBefore := insertLegacyEndpointKey(t, store, vault, endpoint.ID, "migration-commit-second")
	if _, err := store.DB().Exec(deferredCredentialFailureSchema); err != nil {
		t.Fatal(err)
	}

	if err := store.MigrateEndpointKeyEnvelopes(context.Background()); err != ErrEndpointCredentialMigration {
		t.Fatalf("migration commit error=%v", err)
	}
	if got := storedEndpointCiphertext(t, store, firstID); got != firstBefore {
		t.Fatal("commit failure left first row migrated")
	}
	if got := storedEndpointCiphertext(t, store, secondID); got != secondBefore {
		t.Fatal("commit failure left second row migrated")
	}
	var guards int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM deferred_credential_guard`).Scan(&guards); err != nil {
		t.Fatal(err)
	}
	if guards != 0 {
		t.Fatalf("commit failure left %d deferred rows", guards)
	}
}
