// Independent audit: AES-256-GCM envelope tamper / no-leak matrix.
//
// Every tamper class must collapse to the single ErrInvalidCiphertext sentinel
// with no input material in any error text, no plaintext fallback, and no
// panic. Nonces must be fresh per Seal, and the vault must refuse use after
// Close.
package secret_test

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func auditContext(t *testing.T) secret.EndpointKeyContext {
	t.Helper()
	credentialContext, err := secret.NewEndpointKeyContext(11, 22, 33, "https://audit.example:443")
	if err != nil {
		t.Fatal(err)
	}
	return credentialContext
}

func auditVault(t *testing.T) (*secret.Vault, string, string) {
	t.Helper()
	key := bytes.Repeat([]byte{0x6a}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	plaintext := "sk-super-secret-upstream-token-0123456789"
	encoded, err := vault.SealForContext([]byte(plaintext), auditContext(t))
	if err != nil {
		t.Fatal(err)
	}
	return vault, plaintext, encoded
}

func mutateAt(t *testing.T, encoded string, offset int, delta byte) string {
	t.Helper()
	raw := []byte(encoded)
	if offset < 0 || offset >= len(raw) {
		t.Fatalf("mutation offset %d out of range %d", offset, len(raw))
	}
	raw[offset] ^= delta
	return string(raw)
}

func TestAuditSecretTamperMatrix(t *testing.T) {
	vault, plaintext, encoded := auditVault(t)
	credentialContext := auditContext(t)
	parts := strings.Split(encoded, ":")
	if len(parts) != 5 {
		t.Fatalf("unexpected envelope shape: %q", encoded)
	}
	prefix := "nbsec:v2:aes-256-gcm:"
	noncePart := parts[3]
	sealedPart := parts[4]

	cases := []struct {
		name    string
		encoded string
	}{
		{"header prefix byte flipped", mutateAt(t, encoded, 2, 0x01)},
		{"header truncated", encoded[:len("nbsec:v2:aes-256-g")]},
		{"header replaced", "nbsec:v2:aes-256-cbc:" + noncePart + ":" + sealedPart},
		{"header doubled", "nbsec:v2:aes-256-gcm:" + encoded},
		{"missing colon", strings.ReplaceAll(encoded, ":", "!")},
		{"nonce first byte flipped", mutateAt(t, encoded, len(encoded)-len(sealedPart)-len(noncePart)-1, 0x80)},
		{"nonce last byte flipped", mutateAt(t, encoded, len(encoded)-len(sealedPart)-2, 0x01)},
		{"nonce extended", prefix + noncePart + "A:" + sealedPart},
		{"ciphertext first byte flipped", mutateAt(t, encoded, len(encoded)-len(sealedPart), 0x40)},
		{"ciphertext last byte flipped", mutateAt(t, encoded, len(encoded)-1, 0x02)},
		{"ciphertext truncated one byte", encoded[:len(encoded)-1]},
		{"ciphertext extended", encoded + "A"},
		{"non-canonical base64 padding", prefix + noncePart + ":" + sealedPart + "="},
		{"base64 alphabet injection", prefix + noncePart + ":" + strings.Replace(sealedPart, sealedPart[:1], "+", 1)},
		{"sealed text contains colon", prefix + noncePart + ":" + sealedPart + ":AAAA"},
		{"empty sealed part", prefix + noncePart + ":"},
		{"empty nonce part", prefix + ":" + sealedPart},
		{"huge envelope", strings.Repeat("A", 1<<20)},
		{"random garbage", "not-an-envelope-at-all"},
		{"nonce wrong length", prefix + noncePart[:8] + ":" + sealedPart},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vault.OpenForContext(tc.encoded, credentialContext)
			if err == nil {
				t.Fatalf("tampered envelope decrypted: %q", string(got))
			}
			if !bytes.Equal(got, nil) {
				// Open must never return partial plaintext with an error.
				t.Fatalf("Open returned data with error: %q", string(got))
			}
			if err != secret.ErrInvalidCiphertext {
				t.Fatalf("tamper error %v is not the single sentinel", err)
			}
			if strings.Contains(err.Error(), tc.encoded) && len(tc.encoded) < 512 {
				t.Fatalf("error echoes ciphertext material")
			}
		})
	}

	// The genuine envelope still opens with the original plaintext.
	opened, err := vault.OpenForContext(encoded, credentialContext)
	if err != nil {
		t.Fatalf("genuine envelope failed: %v", err)
	}
	defer clear(opened)
	if string(opened) != plaintext {
		t.Fatalf("round trip mismatch")
	}
}

func TestAuditSecretWrongKeyNoPlaintextFallback(t *testing.T) {
	_, _, encoded := auditVault(t)
	wrong := bytes.Repeat([]byte{0x99}, secret.MasterKeyBytes)
	other, err := secret.New(wrong)
	if err != nil {
		t.Fatal(err)
	}
	clear(wrong)
	got, err := other.OpenForContext(encoded, auditContext(t))
	if err != secret.ErrInvalidCiphertext {
		clear(got)
		t.Fatalf("wrong key returned %v", err)
	}
	clear(got)
	_ = other.Close()
}

func TestAuditSecretNonceFreshness(t *testing.T) {
	vault := mustVault(t)
	credentialContext := auditContext(t)
	a, err := vault.SealForContext([]byte("same plaintext"), credentialContext)
	if err != nil {
		t.Fatal(err)
	}
	b, err := vault.SealForContext([]byte("same plaintext"), credentialContext)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two Seals of identical plaintext produced identical envelopes (nonce reuse)")
	}
	// Same plaintext must still decrypt under both envelopes.
	pa, err := vault.OpenForContext(a, credentialContext)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := vault.OpenForContext(b, credentialContext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pa, pb) {
		t.Fatalf("nonce-fresh envelopes disagree on plaintext")
	}
	clear(pa)
	clear(pb)
}

func TestAuditSecretBoundRejections(t *testing.T) {
	vault := mustVault(t)
	credentialContext := auditContext(t)
	if _, err := vault.SealForContext(nil, credentialContext); err != secret.ErrInvalidPlaintext {
		t.Fatalf("empty plaintext: %v", err)
	}
	oversized := make([]byte, secret.MaxPlaintextBytes+1)
	if _, err := vault.SealForContext(oversized, credentialContext); err != secret.ErrInvalidPlaintext {
		t.Fatalf("oversized plaintext: %v", err)
	}
	// Envelope with plaintext just at the ceiling must still fail any
	// ciphertext that decodes beyond the bound (parse-level ceiling).
	big, err := vault.SealForContext(make([]byte, secret.MaxPlaintextBytes), credentialContext)
	if err != nil {
		t.Fatalf("ceiling plaintext rejected: %v", err)
	}
	opened, err := vault.OpenForContext(big, credentialContext)
	if err != nil {
		clear(opened)
		t.Fatalf("ceiling envelope failed: %v", err)
	}
	clear(opened)
	if _, err := secret.New(make([]byte, 16)); err != secret.ErrInvalidMasterKey {
		t.Fatalf("short master key: %v", err)
	}
	if _, err := secret.New(make([]byte, 64)); err != secret.ErrInvalidMasterKey {
		t.Fatalf("long master key: %v", err)
	}
}

func TestAuditSecretClosedVault(t *testing.T) {
	vault := mustVault(t)
	credentialContext := auditContext(t)
	encoded, err := vault.SealForContext([]byte("x"), credentialContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.SealForContext([]byte("x"), credentialContext); err != secret.ErrClosed {
		t.Fatalf("Seal after Close: %v", err)
	}
	if opened, err := vault.OpenForContext(encoded, credentialContext); err != secret.ErrClosed {
		clear(opened)
		t.Fatalf("Open after Close: %v", err)
	}
	if vault.String() == "" || strings.Contains(vault.String(), "key") {
		t.Fatalf("vault String leaks state: %q", vault.String())
	}
}

func TestAuditSecretErrorNeverEchoesEnvelope(t *testing.T) {
	vault := mustVault(t)
	credentialContext := auditContext(t)
	encoded, err := vault.SealForContext([]byte("sk-leak-check"), credentialContext)
	if err != nil {
		t.Fatal(err)
	}
	_, err = vault.OpenForContext("nbsec:v2:aes-256-gcm:AAAA:BBBB", credentialContext)
	if err != secret.ErrInvalidCiphertext {
		t.Fatalf("malformed envelope: %v", err)
	}
	if strings.Contains(err.Error(), "AAAA") || strings.Contains(err.Error(), "BBBB") {
		t.Fatalf("error echoes input material: %q", err.Error())
	}
	if strings.Contains(err.Error(), "nbsec") {
		t.Fatalf("error echoes envelope header: %q", err.Error())
	}
	_ = encoded
}

func TestAuditSecretEnvelopeIsBase64URLCanonical(t *testing.T) {
	vault := mustVault(t)
	encoded, err := vault.SealForContext([]byte("payload"), auditContext(t))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(encoded, ":")
	if len(parts) != 5 {
		t.Fatalf("envelope shape: %q", encoded)
	}
	// RawURLEncoding must not emit '+' '/' '='.
	for _, part := range []string{parts[3], parts[4]} {
		if strings.ContainsAny(part, "+/=") {
			t.Fatalf("non-canonical base64 in envelope part %q", part)
		}
		if _, err := base64.RawURLEncoding.DecodeString(part); err != nil {
			t.Fatalf("invalid base64url part %q: %v", part, err)
		}
	}
}

func mustVault(t *testing.T) *secret.Vault {
	t.Helper()
	key := bytes.Repeat([]byte{0x6a}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}
