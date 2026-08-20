package db

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestOpenRequiresSecretCodec(t *testing.T) {
	path := filepath.Join(t.TempDir(), "must-not-create.db")
	st, err := Open(path, nil)
	if err == nil || st != nil {
		t.Fatal("Open accepted a nil secret codec")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("Open created a database before validating its secret codec")
	}

	var nilVault *secret.Vault
	var typedNil secret.Codec = nilVault
	st, err = Open(path, typedNil)
	if err == nil || st != nil {
		t.Fatal("Open accepted a typed-nil secret codec")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("typed-nil validation created a database")
	}
}

func TestEndpointSecretPersistsOnlyAsCiphertext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret-boundary.db")
	key := bytes.Repeat([]byte{0x93}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("create secret vault: %v", err)
	}
	defer vault.Close()

	plaintext := []byte("database-plaintext-leak-sentinel-7f31")
	defer clear(plaintext)
	st, err := Open(path, vault)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	d := st.DB()
	if _, err := d.Exec(`INSERT INTO users (discord_id, username, created_at, updated_at) VALUES ('secret-owner', 'owner', 1, 1)`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO endpoints (user_id, connector_type, base_url, created_at, updated_at) VALUES (1, 'openai-compatible', 'https://example.com', 1, 1)`); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	created, err := st.CreateEndpointKey(context.Background(), 1, 1, bytes.Clone(plaintext), "", "", "metadata-only", true, 1)
	if err != nil {
		t.Fatalf("create encrypted endpoint key: %v", err)
	}

	var stored string
	if err := d.QueryRow(`SELECT encrypted_secret FROM endpoint_keys WHERE id=?`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("read encrypted endpoint key: %v", err)
	}
	if !strings.HasPrefix(stored, "nbsec:v2:aes-256-gcm:") || stored == string(plaintext) || strings.Contains(stored, string(plaintext)) {
		t.Fatal("database did not retain a contextual ciphertext envelope")
	}
	endpoint := Endpoint{ID: 1, BaseURL: "https://example.com"}
	opened, err := st.secrets.OpenForContext(stored, testEndpointKeyContext(t, 1, endpoint, created.ID))
	if err != nil {
		t.Fatalf("decrypt stored endpoint key: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		clear(opened)
		t.Fatal("stored ciphertext decrypted to different bytes")
	}
	clear(opened)

	if _, err := d.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint encrypted database: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close encrypted database: %v", err)
	}
	assertDatabaseFilesExclude(t, path, plaintext)

	st, err = Open(path, vault)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	d = st.DB()
	if _, err := d.Exec(`UPDATE endpoint_keys SET enabled=0, updated_at=2 WHERE id=?`, created.ID); err != nil {
		t.Fatalf("disable endpoint key: %v", err)
	}
	var disabled int
	if err := d.QueryRow(`SELECT encrypted_secret, enabled FROM endpoint_keys WHERE id=?`, created.ID).Scan(&stored, &disabled); err != nil {
		t.Fatalf("read disabled endpoint key: %v", err)
	}
	if disabled != 0 || !strings.HasPrefix(stored, "nbsec:v2:aes-256-gcm:") || strings.Contains(stored, string(plaintext)) {
		t.Fatal("disabling an endpoint key altered or exposed its secret material")
	}
	if _, err := d.Exec(`DELETE FROM endpoint_keys WHERE id=?`, created.ID); err != nil {
		t.Fatalf("delete endpoint key: %v", err)
	}
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM endpoint_keys`).Scan(&count); err != nil {
		t.Fatalf("count endpoint keys: %v", err)
	}
	if count != 0 {
		t.Fatal("endpoint key deletion did not remove the row")
	}
	if _, err := d.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint after delete: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close after delete: %v", err)
	}
	assertDatabaseFilesExclude(t, path, plaintext)
}

func assertDatabaseFilesExclude(t *testing.T, path string, plaintext []byte) {
	t.Helper()
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		data, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read database artifact: %v", err)
		}
		contains := bytes.Contains(data, plaintext)
		clear(data)
		if contains {
			t.Fatal("a database artifact contains plaintext secret bytes")
		}
	}
}
