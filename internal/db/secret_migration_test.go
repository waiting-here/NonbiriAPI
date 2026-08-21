package db

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func migrationVault(t *testing.T) *secret.Vault {
	t.Helper()
	key := bytes.Repeat([]byte{0x83}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("create migration vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}

func openMigrationStore(t *testing.T, path string, vault secret.Codec) *Store {
	t.Helper()
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatalf("open migration store: %v", err)
	}
	return store
}

func insertLegacyEndpointKey(t *testing.T, store *Store, vault *secret.Vault, endpointID int64, plaintext string) (int64, string) {
	t.Helper()
	material := []byte(plaintext)
	ciphertext, err := vault.Seal(material)
	clear(material)
	if err != nil {
		t.Fatalf("seal legacy endpoint key: %v", err)
	}
	result, err := store.DB().Exec(`
INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, display_head, display_tail, note, enabled, created_at, updated_at)
VALUES (?, ?, '', '', '', 1, 1, 1)`, endpointID, ciphertext)
	if err != nil {
		t.Fatalf("insert legacy endpoint key: %v", err)
	}
	keyID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("legacy endpoint key id: %v", err)
	}
	return keyID, ciphertext
}

func storedEndpointCiphertext(t *testing.T, store *Store, keyID int64) string {
	t.Helper()
	var ciphertext string
	if err := store.DB().QueryRow(`SELECT encrypted_secret FROM endpoint_keys WHERE id=?`, keyID).Scan(&ciphertext); err != nil {
		t.Fatalf("read stored endpoint ciphertext: %v", err)
	}
	return ciphertext
}

func assertContextualPlaintext(t *testing.T, vault *secret.Vault, ciphertext string, credentialContext secret.EndpointKeyContext, want string) {
	t.Helper()
	if !strings.HasPrefix(ciphertext, "nbsec:v2:aes-256-gcm:") {
		t.Fatal("stored credential is not a contextual envelope")
	}
	opened, err := vault.OpenForContext(ciphertext, credentialContext)
	if err != nil {
		t.Fatalf("open contextual credential: %v", err)
	}
	if string(opened) != want {
		clear(opened)
		t.Fatal("contextual credential plaintext differs")
	}
	clear(opened)
}

func TestEndpointCredentialMigrationMixedFixtureAndRepeatedRun(t *testing.T) {
	vault := migrationVault(t)
	store := openMigrationStore(t, filepath.Join(t.TempDir(), "mixed.db"), vault)
	defer store.Close()
	userID := seedTestUser(t, store, "mixed-owner", nil)
	endpoint := mustCreateTestEndpoint(t, store, userID, "https://mixed.example/api/v1")

	current, err := store.CreateEndpointKey(context.Background(), userID, endpoint.ID, []byte("already-contextual"), "", "", "", true, 1)
	if err != nil {
		t.Fatalf("create contextual key: %v", err)
	}
	currentBefore := storedEndpointCiphertext(t, store, current.ID)
	legacyID, legacyBefore := insertLegacyEndpointKey(t, store, vault, endpoint.ID, "legacy-alpha-credential")
	pathOnly := "https://mixed.example/another/v2"
	updatedEndpoint, changes, err := store.UpdateEndpoint(context.Background(), userID, endpoint.ID, &pathOnly, nil, nil, 2)
	if err != nil || !changes.Has(EndpointChangeUpstreamPath) || changes.Has(EndpointChangeOrigin) {
		t.Fatalf("same-origin path update: changes=%b err=%v", changes, err)
	}
	endpoint = updatedEndpoint

	if err := store.MigrateEndpointKeyEnvelopes(context.Background()); err != nil {
		t.Fatalf("migrate mixed credentials: %v", err)
	}
	currentAfter := storedEndpointCiphertext(t, store, current.ID)
	if currentAfter != currentBefore {
		t.Fatal("idempotent migration rewrote an existing contextual envelope")
	}
	legacyAfter := storedEndpointCiphertext(t, store, legacyID)
	if legacyAfter == legacyBefore {
		t.Fatal("legacy credential was not rewritten")
	}
	assertContextualPlaintext(t, vault, currentAfter, testEndpointKeyContext(t, userID, endpoint, current.ID), "already-contextual")
	assertContextualPlaintext(t, vault, legacyAfter, testEndpointKeyContext(t, userID, endpoint, legacyID), "legacy-alpha-credential")
	if opened, err := vault.Open(legacyAfter); !errors.Is(err, secret.ErrInvalidCiphertext) {
		clear(opened)
		t.Fatalf("legacy Open accepted migrated envelope: %v", err)
	}

	if err := store.MigrateEndpointKeyEnvelopes(context.Background()); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	if got := storedEndpointCiphertext(t, store, legacyID); got != legacyAfter {
		t.Fatal("repeat migration rewrote a contextual envelope")
	}
}

func TestEndpointCredentialMigrationFreshDatabaseIsNoOp(t *testing.T) {
	vault := migrationVault(t)
	store := openMigrationStore(t, filepath.Join(t.TempDir(), "fresh.db"), vault)
	defer store.Close()
	if err := store.MigrateEndpointKeyEnvelopes(context.Background()); err != nil {
		t.Fatalf("fresh database migration: %v", err)
	}
	if err := store.MigrateEndpointKeyEnvelopes(context.Background()); err != nil {
		t.Fatalf("fresh database repeat migration: %v", err)
	}
}

func TestEndpointCredentialMigrationFailureRollsBackWholeBatch(t *testing.T) {
	cases := []struct {
		name       string
		failingRow func(*testing.T, *Store, *secret.Vault, int64, int64)
	}{
		{
			name: "damaged legacy",
			failingRow: func(t *testing.T, store *Store, _ *secret.Vault, endpointID, _ int64) {
				if _, err := store.DB().Exec(`INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, created_at, updated_at) VALUES (?, 'nbsec:v1:aes-256-gcm:AAAA:BBBB', 1, 1)`, endpointID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "oversized envelope",
			failingRow: func(t *testing.T, store *Store, _ *secret.Vault, endpointID, _ int64) {
				if _, err := store.DB().Exec(`INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, created_at, updated_at) VALUES (?, ?, 1, 1)`, endpointID, strings.Repeat("A", 129<<10)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unknown future version",
			failingRow: func(t *testing.T, store *Store, vault *secret.Vault, endpointID, userID int64) {
				var keyID int64
				if err := store.DB().QueryRow(`SELECT COALESCE(MAX(id),0)+1 FROM endpoint_keys`).Scan(&keyID); err != nil {
					t.Fatal(err)
				}
				endpoint, err := store.GetEndpoint(context.Background(), userID, endpointID)
				if err != nil {
					t.Fatal(err)
				}
				current, err := vault.SealForContext([]byte("future-version-marker"), testEndpointKeyContext(t, userID, endpoint, keyID))
				if err != nil {
					t.Fatal(err)
				}
				future := strings.Replace(current, ":v2:", ":v9:", 1)
				if _, err := store.DB().Exec(`INSERT INTO endpoint_keys (id, endpoint_id, encrypted_secret, created_at, updated_at) VALUES (?, ?, ?, 1, 1)`, keyID, endpointID, future); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "damaged contextual",
			failingRow: func(t *testing.T, store *Store, vault *secret.Vault, endpointID, userID int64) {
				var keyID int64
				if err := store.DB().QueryRow(`SELECT COALESCE(MAX(id),0)+1 FROM endpoint_keys`).Scan(&keyID); err != nil {
					t.Fatal(err)
				}
				endpoint, err := store.GetEndpoint(context.Background(), userID, endpointID)
				if err != nil {
					t.Fatal(err)
				}
				current, err := vault.SealForContext([]byte("damaged-context-marker"), testEndpointKeyContext(t, userID, endpoint, keyID))
				if err != nil {
					t.Fatal(err)
				}
				last := current[len(current)-1]
				replacement := byte('A')
				if last == replacement {
					replacement = 'B'
				}
				damaged := current[:len(current)-1] + string(replacement)
				if _, err := store.DB().Exec(`INSERT INTO endpoint_keys (id, endpoint_id, encrypted_secret, created_at, updated_at) VALUES (?, ?, ?, 1, 1)`, keyID, endpointID, damaged); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "wrong contextual owner",
			failingRow: func(t *testing.T, store *Store, vault *secret.Vault, endpointID, userID int64) {
				otherUser := seedTestUser(t, store, "wrong-context-owner", nil)
				var keyID int64
				if err := store.DB().QueryRow(`SELECT COALESCE(MAX(id),0)+1 FROM endpoint_keys`).Scan(&keyID); err != nil {
					t.Fatal(err)
				}
				endpoint, err := store.GetEndpoint(context.Background(), userID, endpointID)
				if err != nil {
					t.Fatal(err)
				}
				_, origin, err := egress.CanonicalEndpointTarget(endpoint.BaseURL)
				if err != nil {
					t.Fatal(err)
				}
				wrongContext, err := secret.NewEndpointKeyContext(otherUser, endpointID, keyID, origin)
				if err != nil {
					t.Fatal(err)
				}
				current, err := vault.SealForContext([]byte("wrong-context-marker"), wrongContext)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.DB().Exec(`INSERT INTO endpoint_keys (id, endpoint_id, encrypted_secret, created_at, updated_at) VALUES (?, ?, ?, 1, 1)`, keyID, endpointID, current); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := migrationVault(t)
			store := openMigrationStore(t, filepath.Join(t.TempDir(), "rollback.db"), vault)
			defer store.Close()
			userID := seedTestUser(t, store, "rollback-owner", nil)
			endpoint := mustCreateTestEndpoint(t, store, userID, "https://rollback-marker.example/api/v1")
			firstID, firstBefore := insertLegacyEndpointKey(t, store, vault, endpoint.ID, "first-secret-rollback-marker")
			tc.failingRow(t, store, vault, endpoint.ID, userID)

			err := store.MigrateEndpointKeyEnvelopes(context.Background())
			if err != ErrEndpointCredentialMigration {
				t.Fatalf("migration error=%v, want opaque sentinel", err)
			}
			if got := storedEndpointCiphertext(t, store, firstID); got != firstBefore {
				t.Fatal("failed batch partially migrated an earlier row")
			}
			for _, marker := range []string{"rollback-marker", "first-secret", firstBefore, "nbsec", "future-version-marker", "wrong-context-marker"} {
				if strings.Contains(err.Error(), marker) {
					t.Fatalf("migration error exposed protected material")
				}
			}
		})
	}
}

func TestEndpointCredentialMigrationSQLiteFailureRollsBackAndStaysOpaque(t *testing.T) {
	vault := migrationVault(t)
	store := openMigrationStore(t, filepath.Join(t.TempDir(), "sqlite-rollback.db"), vault)
	defer store.Close()
	userID := seedTestUser(t, store, "sqlite-owner", nil)
	endpoint := mustCreateTestEndpoint(t, store, userID, "https://sqlite-rollback.example/v1")
	firstID, firstBefore := insertLegacyEndpointKey(t, store, vault, endpoint.ID, "sqlite-first-secret")
	secondID, secondBefore := insertLegacyEndpointKey(t, store, vault, endpoint.ID, "sqlite-second-secret")
	if _, err := store.DB().Exec(`
CREATE TRIGGER reject_second_credential_update
BEFORE UPDATE OF encrypted_secret ON endpoint_keys
WHEN OLD.id=` + strconv.FormatInt(secondID, 10) + `
BEGIN
  SELECT RAISE(ABORT, 'sqlite-trigger-detail-marker');
END;`); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}

	err := store.MigrateEndpointKeyEnvelopes(context.Background())
	if err != ErrEndpointCredentialMigration || strings.Contains(err.Error(), "sqlite-trigger-detail-marker") {
		t.Fatalf("migration error is not opaque: %v", err)
	}
	if got := storedEndpointCiphertext(t, store, firstID); got != firstBefore {
		t.Fatal("SQLite failure left an earlier row migrated")
	}
	if got := storedEndpointCiphertext(t, store, secondID); got != secondBefore {
		t.Fatal("SQLite failure changed the rejected row")
	}
}

func TestCreateEndpointKeyRollsBackPlaceholderAndClearsPlaintext(t *testing.T) {
	vault := migrationVault(t)
	path := filepath.Join(t.TempDir(), "create-rollback.db")
	store := openMigrationStore(t, path, vault)
	userID := seedTestUser(t, store, "create-owner", nil)
	endpoint := mustCreateTestEndpoint(t, store, userID, "https://create.example/v1")
	if _, err := store.DB().Exec(`
CREATE TRIGGER reject_credential_finalize
BEFORE UPDATE OF encrypted_secret ON endpoint_keys
BEGIN
  SELECT RAISE(ABORT, 'finalize-failure-marker');
END;`); err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("plaintext-lifecycle-marker")
	_, err := store.CreateEndpointKey(context.Background(), userID, endpoint.ID, plaintext, "head", "tail", "", true, 1)
	if err == nil {
		t.Fatal("forced finalize failure succeeded")
	}
	for i, b := range plaintext {
		if b != 0 {
			t.Fatalf("plaintext byte %d was not cleared", i)
		}
	}
	var count, empty int
	if err := store.DB().QueryRow(`SELECT COUNT(*), COALESCE(SUM(encrypted_secret=''),0) FROM endpoint_keys`).Scan(&count, &empty); err != nil {
		t.Fatal(err)
	}
	if count != 0 || empty != 0 {
		t.Fatalf("failed create left count=%d empty=%d", count, empty)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openMigrationStore(t, path, vault)
	defer store.Close()
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_keys`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("reopen found a placeholder: count=%d err=%v", count, err)
	}
}

func TestCreateEndpointKeyNeverPresentsPlaintextToSQLite(t *testing.T) {
	vault := migrationVault(t)
	store := openMigrationStore(t, filepath.Join(t.TempDir(), "sql-boundary.db"), vault)
	defer store.Close()
	userID := seedTestUser(t, store, "sql-owner", nil)
	endpoint := mustCreateTestEndpoint(t, store, userID, "https://sql-boundary.example/v1")
	const marker = "sql-plaintext-parameter-marker"
	if _, err := store.DB().Exec(`
CREATE TRIGGER reject_plaintext_insert
BEFORE INSERT ON endpoint_keys
WHEN instr(NEW.encrypted_secret, '` + marker + `') > 0
BEGIN
  SELECT RAISE(ABORT, 'plaintext reached insert');
END;
CREATE TRIGGER reject_plaintext_update
BEFORE UPDATE OF encrypted_secret ON endpoint_keys
WHEN instr(NEW.encrypted_secret, '` + marker + `') > 0
BEGIN
  SELECT RAISE(ABORT, 'plaintext reached update');
END;`); err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(marker)
	key, err := store.CreateEndpointKey(context.Background(), userID, endpoint.ID, plaintext, "", "", "", true, 1)
	if err != nil {
		t.Fatalf("contextual create: %v", err)
	}
	for _, b := range plaintext {
		if b != 0 {
			t.Fatal("successful create did not clear plaintext")
		}
	}
	ciphertext := storedEndpointCiphertext(t, store, key.ID)
	if ciphertext == "" || strings.Contains(ciphertext, marker) {
		t.Fatal("stored credential is empty or exposes plaintext")
	}
	assertContextualPlaintext(t, vault, ciphertext, testEndpointKeyContext(t, userID, endpoint, key.ID), marker)
}

func TestEndpointCredentialMigrationBackupRestore(t *testing.T) {
	vault := migrationVault(t)
	dir := t.TempDir()
	primaryPath := filepath.Join(dir, "primary.db")
	backupPath := filepath.Join(dir, "backup.db")
	restoredPath := filepath.Join(dir, "restored.db")

	store := openMigrationStore(t, primaryPath, vault)
	userID := seedTestUser(t, store, "backup-owner", nil)
	endpoint := mustCreateTestEndpoint(t, store, userID, "https://backup.example/v1")
	keyID, legacyCiphertext := insertLegacyEndpointKey(t, store, vault, endpoint.ID, "backup-restore-secret")
	if _, err := store.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	copyDatabaseFile(t, primaryPath, backupPath)

	store = openMigrationStore(t, primaryPath, vault)
	if err := store.MigrateEndpointKeyEnvelopes(context.Background()); err != nil {
		t.Fatal(err)
	}
	migratedCiphertext := storedEndpointCiphertext(t, store, keyID)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if migratedCiphertext == legacyCiphertext {
		t.Fatal("primary database did not migrate")
	}
	if opened, err := vault.Open(migratedCiphertext); err != secret.ErrInvalidCiphertext {
		clear(opened)
		t.Fatalf("legacy reader accepted migrated backup: %v", err)
	}

	copyDatabaseFile(t, backupPath, restoredPath)
	restored := openMigrationStore(t, restoredPath, vault)
	defer restored.Close()
	if got := storedEndpointCiphertext(t, restored, keyID); got != legacyCiphertext {
		t.Fatal("restored pre-migration backup differs")
	}
	opened, err := vault.Open(legacyCiphertext)
	if err != nil || string(opened) != "backup-restore-secret" {
		clear(opened)
		t.Fatalf("restored legacy credential cannot open: %v", err)
	}
	clear(opened)
	if err := restored.MigrateEndpointKeyEnvelopes(context.Background()); err != nil {
		t.Fatalf("migrate restored backup: %v", err)
	}
	restoredCiphertext := storedEndpointCiphertext(t, restored, keyID)
	assertContextualPlaintext(t, vault, restoredCiphertext, testEndpointKeyContext(t, userID, endpoint, keyID), "backup-restore-secret")
}

func copyDatabaseFile(t *testing.T, source, destination string) {
	t.Helper()
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
