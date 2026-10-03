package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestDonationReviewStartupResumesCommittedBatches(t *testing.T) {
	vault, err := secret.New(bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	path := filepath.Join(t.TempDir(), "review.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tx, err := store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := uint64(1); i <= 1001; i++ {
		contextID := make([]byte, 16)
		binary.BigEndian.PutUint64(contextID[8:], i)
		credential, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := vault.SealForGenerationTwoContext([]byte("original-credential-body"), credential)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO endpoint_key_secrets(id,context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,?,'https://upstream.example/v1','openai-compatible',?,0)`, i, contextID, envelope); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TRIGGER stop_review_batch BEFORE UPDATE OF key_body_review_hmac ON endpoint_key_secrets
WHEN NEW.id=1001 BEGIN SELECT RAISE(ABORT,'batch failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeDonationReview(context.Background(), store.DB(), vault); err == nil {
		t.Fatal("failed batch accepted")
	}
	var completed int
	if err := store.DB().QueryRow("SELECT count(*) FROM endpoint_key_secrets WHERE key_body_review_hmac IS NOT NULL").Scan(&completed); err != nil || completed != 1000 {
		t.Fatalf("committed batch count=%d: %v", completed, err)
	}
	var before, after []byte
	if err := store.DB().QueryRow("SELECT envelope FROM donation_review_material WHERE id=1").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec("DROP TRIGGER stop_review_batch"); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeDonationReview(context.Background(), store.DB(), vault); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow("SELECT count(*) FROM endpoint_key_secrets WHERE key_body_review_hmac IS NOT NULL").Scan(&completed); err != nil || completed != 1001 {
		t.Fatalf("resumed batch count=%d: %v", completed, err)
	}
	if err := store.DB().QueryRow("SELECT envelope FROM donation_review_material WHERE id=1").Scan(&after); err != nil || !bytes.Equal(before, after) {
		t.Fatal("resume replaced stable material", err)
	}
}

func sealRootTestCredential(t *testing.T) ([]byte, string) {
	t.Helper()
	vault, err := secret.New(bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	contextID := make([]byte, 16)
	if _, err := rand.Read(contextID); err != nil {
		t.Fatal(err)
	}
	credential, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := vault.SealForGenerationTwoContext([]byte("original-credential-body"), credential)
	if err != nil {
		t.Fatal(err)
	}
	return contextID, envelope
}
