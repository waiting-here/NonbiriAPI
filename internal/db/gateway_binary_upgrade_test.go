package db

import (
	"bytes"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"os"
	"path/filepath"
	"testing"
)

func TestGatewayUpgradeFromPreviousBinary(t *testing.T) {
	directory := os.Getenv("NONBIRI_GATEWAY_FIXTURES")
	if directory == "" {
		t.Skip("previous binary gate supplies fixture")
	}
	vault, err := secret.New(bytes.Repeat([]byte{0x63}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	data, err := os.ReadFile(filepath.Join(directory, "prior.db"))
	if err != nil {
		t.Fatal(err)
	}
	path := bootstrapTestPath(t, "upgrade.sqlite")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	prior, err := openSQLite(path, "ro")
	if err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, prior, preGatewayPolicyManifestHash)
	before := retainedTableImages(t, prior, nil)
	prior.Close()
	for range 2 {
		store, err := Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		assertRetainedImages(t, store.DB(), before)
		assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
		var keys, attempts int
		if err = store.DB().QueryRow(`SELECT count(*) FROM donation_keys WHERE typeof(failure_disable_threshold)='text' AND failure_disable_threshold='10' AND hex(failure_streak)='00000000000000000000000000000013' AND hex(streak_generation)='00000000000000000000000000000007' AND failure_disabled=1 AND enabled=0`).Scan(&keys); err != nil || keys != 1 {
			t.Fatal("policy state changed", keys, err)
		}
		if err = store.DB().QueryRow("SELECT count(*) FROM request_attempts").Scan(&attempts); err != nil || attempts != 3 {
			t.Fatal("missing populated attempts", attempts, err)
		}
		var setting, legal string
		if err = store.DB().QueryRow("SELECT value FROM site_config WHERE key='gateway_user_attribution_enabled'").Scan(&setting); err != nil || setting != "0" {
			t.Fatal("wrong attribution default", setting, err)
		}
		if err = store.DB().QueryRow("SELECT value FROM site_config WHERE key='legal_terms_override_en'").Scan(&legal); err != nil || legal != "Preserved custom policy" {
			t.Fatal("custom policy changed", err)
		}
		before = retainedTableImages(t, store.DB(), nil)
		if err = store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	upgraded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "upgraded.db"), upgraded, 0600); err != nil {
		t.Fatal(err)
	}
}
