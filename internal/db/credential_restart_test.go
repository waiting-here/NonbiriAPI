package db

import (
	"bytes"
	"os"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestCredentialRestartValidatesStoredURLsAndLiveTargets(t *testing.T) {
	// Keep one real fresh bootstrap, then exercise each restart from a private
	// copy of its closed, credential-free seed database.
	baselinePath := bootstrapTestPath(t, "credentials-baseline.db")
	baseline, err := Open(baselinePath, bootstrapTestVault(t))
	if err != nil {
		t.Fatalf("open fresh credential fixture: %v", err)
	}
	t.Cleanup(func() { _ = baseline.Close() })
	var busy, logFrames, checkpointed int
	if err := baseline.DB().QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
		t.Fatalf("checkpoint credential fixture: %v", err)
	}
	if busy != 0 || logFrames < 0 || checkpointed < 0 || checkpointed > logFrames {
		t.Fatalf("credential fixture checkpoint returned (%d,%d,%d)", busy, logFrames, checkpointed)
	}
	if err := baseline.Close(); err != nil {
		t.Fatalf("close credential fixture: %v", err)
	}
	image, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read credential fixture: %v", err)
	}
	for _, tc := range []struct {
		name, stored, linked string
		accepted             bool
	}{
		{"https-default", "https://api.example.com/v1", "https://api.example.com/v1", true},
		{"http-default", "http://api.example.com/v1", "http://api.example.com/v1", true},
		{"explicit-default", "https://api.example.com:443/v1", "https://api.example.com:443/v1", true},
		{"equivalent-default", "https://api.example.com/v1", "https://api.example.com:443/v1", true},
		{"custom-port", "https://api.example.com:8443/v1", "https://api.example.com:8443/v1", true},
		{"ipv6", "https://[2606:4700:4700::1111]/v1", "https://[2606:4700:4700::1111]/v1", true},
		{"orphan", "https://api.example.com/v1", "", true},
		{"host-drift", "https://api.example.com/v1", "https://other.example.com/v1", false},
		{"port-drift", "https://api.example.com/v1", "https://api.example.com:8443/v1", false},
		{"path-drift", "https://api.example.com/v1", "https://api.example.com/v2", false},
		{"scheme-drift", "https://api.example.com/v1", "http://api.example.com/v1", false},
		{"noncanonical-secret", "https://API.EXAMPLE.COM/v1", "", false},
		{"query-in-secret", "https://api.example.com/v1?key=removed", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := copyPrivateSQLiteTestImage(t, image)
			vault := bootstrapTestVault(t)
			store, err := Open(path, vault)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			var credentialCount int
			if err := store.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_key_secrets`).Scan(&credentialCount); err != nil {
				t.Fatal(err)
			}
			if credentialCount != 0 {
				t.Fatalf("credential fixture copy contains %d prior secrets", credentialCount)
			}
			contextID := bytes.Repeat([]byte{0x37}, 16)
			credentialContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
			if err != nil {
				t.Fatal(err)
			}
			envelope, err := vault.SealForGenerationTwoContext([]byte("restart-test-credential"), credentialContext)
			if err != nil {
				t.Fatal(err)
			}
			result := hostileMustExec(t, store.DB(), `INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,?,'openai-compatible',?,0)`, contextID, tc.stored, envelope)
			secretID := hostileMustLastID(t, result)
			if tc.linked != "" {
				owner := hostileInsertUser(t, store.DB(), "credential-restart", 0, 0)
				endpoint := hostileInsertEndpoint(t, store.DB(), owner, tc.linked)
				hostileInsertEndpointKey(t, store.DB(), endpoint, secretID)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			before := snapshotBootstrapSources(t, path)
			reopened, err := Open(path, vault)
			if !tc.accepted {
				if reopened != nil {
					_ = reopened.Close()
				}
				assertBootstrapStartupKind(t, err, StartupCredentialReject)
				assertBootstrapSourcesUnchanged(t, path, before)
				return
			}
			if err != nil {
				t.Fatalf("reopen valid credential: %v", err)
			}
			defer reopened.Close()
			var stored, encrypted string
			if err := reopened.DB().QueryRow(`SELECT canonical_base_url,encrypted_secret FROM endpoint_key_secrets WHERE id=?`, secretID).Scan(&stored, &encrypted); err != nil {
				t.Fatal(err)
			}
			if stored != tc.stored || encrypted != envelope {
				t.Fatal("restart rewrote immutable credential material")
			}
		})
	}
}
