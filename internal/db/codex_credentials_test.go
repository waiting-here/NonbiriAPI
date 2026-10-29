package db

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type codecWithoutCodex struct {
	secret.GenerationTwoContextCodec
}

func TestCodexEnvelopeValidation(t *testing.T) {
	vault := bootstrapTestVault(t)
	other, err := secret.New(bytes.Repeat([]byte{0x71}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	payload := []byte("synthetic-codex-credential")
	seal := func(value string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	valid := seal(vault.SealCodex(payload))
	wrongKey := seal(other.SealCodex(payload))
	wrongPurpose := seal(vault.SealChatGPT(payload, "credentials"))
	agentPurpose := seal(vault.SealAgentKey(payload, "model"))
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if _, err = database.Exec(CodexSessionSQL); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, encrypted string
		row             bool
		codec           secret.GenerationTwoContextCodec
		rejected        bool
	}{
		{"absent", "", false, codecWithoutCodex{vault}, false},
		{"disconnected", "", true, codecWithoutCodex{vault}, false},
		{"codex_aad", valid, true, vault, false},
		{"chatgpt_aad", wrongPurpose, true, vault, true},
		{"agent_aad", agentPurpose, true, vault, true},
		{"different_master_key", wrongKey, true, vault, true},
		{"damaged_ciphertext", valid[:len(valid)/2], true, vault, true},
		{"invalid_ciphertext", "private-corrupt-envelope", true, vault, true},
		{"missing_codex_codec", valid, true, codecWithoutCodex{vault}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := database.Exec("DELETE FROM agent_codex_session"); err != nil {
				t.Fatal(err)
			}
			if tc.row {
				if _, err := database.Exec("INSERT INTO agent_codex_session(id,revision,encrypted_credentials,updated_at) VALUES(1,1,?,0)", tc.encrypted); err != nil {
					t.Fatal(err)
				}
			}
			err := validateCodexEnvelope(t.Context(), database, tc.codec)
			if (err != nil) != tc.rejected {
				t.Fatalf("envelope rejection=%t, want %t: %v", err != nil, tc.rejected, err)
			}
			if err != nil && (strings.Contains(err.Error(), tc.encrypted) || strings.Contains(err.Error(), string(payload))) {
				t.Fatal("credential error exposed encrypted or plaintext material")
			}
		})
	}
}

type codexVerificationProbe struct {
	*secret.Vault
	opens atomic.Int64
}

func (p *codexVerificationProbe) OpenCodex(encrypted string) ([]byte, error) {
	p.opens.Add(1)
	return p.Vault.OpenCodex(encrypted)
}

func TestCodexFullVerificationAndStartupBoundary(t *testing.T) {
	vault := &codexVerificationProbe{Vault: bootstrapTestVault(t)}
	path := bootstrapTestPath(t, "codex-verification.sqlite")
	store, err := OpenContext(t.Context(), path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	envelope, err := vault.SealCodex([]byte("synthetic-codex-private-material"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB().Exec("INSERT INTO agent_codex_session(id,revision,encrypted_credentials,updated_at) VALUES(1,1,?,0)", envelope); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if vault.opens.Load() != 0 {
		t.Fatal("ordinary startup scanned Codex envelopes")
	}
	if err = VerifyContext(t.Context(), path, vault, nil); err != nil {
		t.Fatal(err)
	}
	if vault.opens.Load() != 1 {
		t.Fatal("full verification did not authenticate the Codex envelope")
	}
	store, err = OpenContext(t.Context(), path, vault)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB().Exec("UPDATE agent_codex_session SET encrypted_credentials='private-corrupt-envelope' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenContext(context.Background(), path, vault)
	if err != nil {
		t.Fatalf("ordinary restart unexpectedly performed full credential verification: %v", err)
	}
	if vault.opens.Load() != 1 {
		t.Fatal("ordinary restart scanned Codex envelopes")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	err = VerifyContext(t.Context(), path, vault, nil)
	assertBootstrapStartupKind(t, err, StartupCredentialReject)
	if vault.opens.Load() != 2 {
		t.Fatal("full verification skipped the corrupt Codex envelope")
	}
	if strings.Contains(err.Error(), "private-corrupt-envelope") {
		t.Fatal("verification error exposed the stored envelope")
	}
}
