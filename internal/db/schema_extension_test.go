package db

import (
	"context"
	"errors"
	"testing"
)

func TestRoutingExtensionPreservesExistingGenerationTwoData(t *testing.T) {
	path := bootstrapTestPath(t, "routing-extension.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	userID := seedUserRaw(t, store, "routing-extension-fixture")
	if _, err := store.DB().Exec(`UPDATE users SET username='Preserved name',total_requests=? WHERE id=?`, EncodeU128(U128{15: 17}), userID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE site_config SET value='Existing site',updated_at=17 WHERE key='site_name'`); err != nil {
		t.Fatal(err)
	}
	at := int64(generationTwoMaxUnixSeconds)
	ids := insertBootstrapOrphanRows(t, store, vault, 1, &at, 31)
	var envelope string
	if err := store.DB().QueryRow(`SELECT encrypted_secret FROM endpoint_key_secrets WHERE id=?`, ids[0]).Scan(&envelope); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`DROP TABLE charity_model_routing`); err != nil {
		t.Fatal(err)
	}
	manifest, err := readGenerationManifest(context.Background(), store.DB())
	if err != nil || generationManifestDigest(manifest) != preRoutingManifestHash {
		t.Fatalf("fixture does not match deployed schema: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		store, err = Open(path, vault)
		if err != nil {
			t.Fatalf("open %d: %v", attempt, err)
		}
		defer store.Close()
		if err := validateGenerationTwoManifest(context.Background(), store.DB()); err != nil {
			t.Fatal(err)
		}
		var name, site string
		var requests []byte
		var updated int64
		var currentEnvelope string
		if err := store.DB().QueryRow(`SELECT username,total_requests FROM users WHERE id=?`, userID).Scan(&name, &requests); err != nil {
			t.Fatal(err)
		}
		if err := store.DB().QueryRow(`SELECT value,updated_at FROM site_config WHERE key='site_name'`).Scan(&site, &updated); err != nil {
			t.Fatal(err)
		}
		if err := store.DB().QueryRow(`SELECT encrypted_secret FROM endpoint_key_secrets WHERE id=?`, ids[0]).Scan(&currentEnvelope); err != nil {
			t.Fatal(err)
		}
		count, err := DecodeU128(requests)
		if err != nil || count != (U128{15: 17}) || name != "Preserved name" || site != "Existing site" || updated != 17 || currentEnvelope != envelope {
			t.Fatal("extension changed existing data")
		}
		if countRows(t, store, `SELECT COUNT(*) FROM charity_model_routing`) != 0 {
			t.Fatal("extension rewrote existing model settings")
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRoutingExtensionRejectsModifiedPriorSchema(t *testing.T) {
	path := bootstrapTestPath(t, "routing-extension-invalid.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`DROP TABLE charity_model_routing; DROP INDEX idx_users_created`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before := snapshotBootstrapSources(t, path)
	reopened, err := Open(path, vault)
	if reopened != nil {
		reopened.Close()
		t.Fatal("modified old schema accepted")
	}
	var startup *StartupError
	if !errors.As(err, &startup) {
		t.Fatalf("expected startup rejection: %v", err)
	}
	after := snapshotBootstrapSources(t, path)
	for suffix, image := range before {
		other := after[suffix]
		if image.present != other.present || string(image.data) != string(other.data) || image.mode != other.mode || !image.modTime.Equal(other.modTime) {
			t.Fatalf("rejected update modified %q", suffix)
		}
	}
}
