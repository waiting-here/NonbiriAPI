package db

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type blockingContextCodec struct {
	vault   *secret.Vault
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *blockingContextCodec) Seal(plaintext []byte) (string, error) {
	return c.vault.Seal(plaintext)
}

func (c *blockingContextCodec) Open(ciphertext string) ([]byte, error) {
	return c.vault.Open(ciphertext)
}

func (c *blockingContextCodec) SealForContext(plaintext []byte, credentialContext secret.EndpointKeyContext) (string, error) {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return c.vault.SealForContext(plaintext, credentialContext)
}

func (c *blockingContextCodec) OpenForContext(ciphertext string, credentialContext secret.EndpointKeyContext) ([]byte, error) {
	return c.vault.OpenForContext(ciphertext, credentialContext)
}

func TestCreateEndpointKeyPlaceholderIsNeverVisible(t *testing.T) {
	key := bytes.Repeat([]byte{0xa4}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	codec := &blockingContextCodec{
		vault:   vault,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	path := filepath.Join(t.TempDir(), "placeholder.db")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := Open(path, codec)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	userID := seedTestUser(t, store, "placeholder-owner", nil)
	endpoint := mustCreateTestEndpoint(t, store, userID, "https://placeholder.example/v1")

	plaintext := []byte("placeholder-visibility-secret")
	result := make(chan error, 1)
	go func() {
		_, err := store.CreateEndpointKey(context.Background(), userID, endpoint.ID, plaintext, "", "", "", true, 1)
		result <- err
	}()
	<-codec.entered

	reader, err := sql.Open("sqlite", path)
	if err != nil {
		close(codec.release)
		t.Fatal(err)
	}
	defer reader.Close()
	reader.SetMaxOpenConns(1)
	if _, err := reader.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		close(codec.release)
		t.Fatal(err)
	}
	var count int
	if err := reader.QueryRow(`SELECT COUNT(*) FROM endpoint_keys`).Scan(&count); err != nil {
		close(codec.release)
		t.Fatal(err)
	}
	if count != 0 {
		close(codec.release)
		t.Fatalf("another connection observed %d uncommitted placeholders", count)
	}

	close(codec.release)
	if err := <-result; err != nil {
		t.Fatalf("create endpoint key: %v", err)
	}
	for _, b := range plaintext {
		if b != 0 {
			t.Fatal("create did not clear the transferred plaintext")
		}
	}
	var empty int
	if err := reader.QueryRow(`SELECT COUNT(*), COALESCE(SUM(encrypted_secret=''),0) FROM endpoint_keys`).Scan(&count, &empty); err != nil {
		t.Fatal(err)
	}
	if count != 1 || empty != 0 {
		t.Fatalf("committed rows=%d empty placeholders=%d", count, empty)
	}
}
