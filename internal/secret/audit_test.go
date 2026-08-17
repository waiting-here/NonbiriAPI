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

func auditVault(t *testing.T) (*secret.Vault, string, string) {
	t.Helper()
	key := bytes.Repeat([]byte{0x6a}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := "sk-super-secret-upstream-token-0123456789"
	encoded, err := vault.Seal([]byte(plaintext))
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
	header := strings.SplitN(encoded, ":", 4)
	if len(header) != 4 {
		t.Fatalf("unexpected envelope shape: %q", encoded)
	}
	noncePart := header[2]
	sealedPart := header[3]

	cases := []struct {
		name    string
		encoded string
	}{
		{"header prefix byte flipped", mutateAt(t, encoded, 2, 0x01)},
		{"header truncated", encoded[:len("nbsec:v1:aes-256-g")]},
		{"header replaced", "nbsec:v1:aes-256-cbc:" + noncePart + ":" + sealedPart},
		{"header doubled", "nbsec:v1:aes-256-gcm:" + encoded},
		{"missing colon", strings.ReplaceAll(encoded, ":", "!")},
		{"nonce first byte flipped", mutateAt(t, encoded, len(encoded)-len(sealedPart)-len(noncePart)-1, 0x80)},
		{"nonce last byte flipped", mutateAt(t, encoded, len(encoded)-len(sealedPart)-2, 0x01)},
		{"nonce padding added", encoded[:len(encoded)-len(sealedPart)] + "A" + sealedPart},
		{"ciphertext first byte flipped", mutateAt(t, encoded, len(encoded)-len(sealedPart), 0x40)},
		{"ciphertext last byte flipped", mutateAt(t, encoded, len(encoded)-1, 0x02)},
		{"ciphertext truncated one byte", encoded[:len(encoded)-1]},
		{"ciphertext extended", encoded + "A"},
		{"non-canonical base64 padding", strings.Replace(sealedPart, sealedPart[:2], "==", 1)},
		{"base64 alphabet injection", strings.Replace(sealedPart, sealedPart[:1], "+", 1)},
		{"sealed text contains colon", sealedPart + ":" + "AAAA"},
		{"empty sealed part", header[0] + ":" + header[1] + ":" + noncePart + ":"},
		{"empty nonce part", header[0] + ":" + header[1] + "::" + sealedPart},
		{"huge envelope", strings.Repeat("A", 1<<20)},
		{"random garbage", "not-an-envelope-at-all"},
		{"nonce wrong length", header[0] + ":" + header[1] + ":" + noncePart[:8] + ":" + sealedPart},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vault.Open(tc.encoded)
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
	opened, err := vault.Open(encoded)
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
	got, err := other.Open(encoded)
	if err != secret.ErrInvalidCiphertext {
		t.Fatalf("wrong key returned %v (plaintext %q)", err, string(got))
	}
	_ = other.Close()
}

func TestAuditSecretNonceFreshness(t *testing.T) {
	vault := mustVault(t)
	a, err := vault.Seal([]byte("same plaintext"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := vault.Seal([]byte("same plaintext"))
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two Seals of identical plaintext produced identical envelopes (nonce reuse)")
	}
	// Same plaintext must still decrypt under both envelopes.
	pa, err := vault.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := vault.Open(b)
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
	if _, err := vault.Seal(nil); err != secret.ErrInvalidPlaintext {
		t.Fatalf("empty plaintext: %v", err)
	}
	oversized := make([]byte, secret.MaxPlaintextBytes+1)
	if _, err := vault.Seal(oversized); err != secret.ErrInvalidPlaintext {
		t.Fatalf("oversized plaintext: %v", err)
	}
	// Envelope with plaintext just at the ceiling must still fail any
	// ciphertext that decodes beyond the bound (parse-level ceiling).
	big, err := vault.Seal(make([]byte, secret.MaxPlaintextBytes))
	if err != nil {
		t.Fatalf("ceiling plaintext rejected: %v", err)
	}
	if _, err := vault.Open(big); err != nil {
		t.Fatalf("ceiling envelope failed: %v", err)
	}
	if _, err := secret.New(make([]byte, 16)); err != secret.ErrInvalidMasterKey {
		t.Fatalf("short master key: %v", err)
	}
	if _, err := secret.New(make([]byte, 64)); err != secret.ErrInvalidMasterKey {
		t.Fatalf("long master key: %v", err)
	}
}

func TestAuditSecretClosedVault(t *testing.T) {
	vault := mustVault(t)
	encoded, err := vault.Seal([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Seal([]byte("x")); err != secret.ErrClosed {
		t.Fatalf("Seal after Close: %v", err)
	}
	if _, err := vault.Open(encoded); err != secret.ErrClosed {
		t.Fatalf("Open after Close: %v", err)
	}
	if vault.String() == "" || strings.Contains(vault.String(), "key") {
		t.Fatalf("vault String leaks state: %q", vault.String())
	}
}

func TestAuditSecretErrorNeverEchoesEnvelope(t *testing.T) {
	vault := mustVault(t)
	encoded, err := vault.Seal([]byte("sk-leak-check"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = vault.Open("nbsec:v1:aes-256-gcm:AAAA:BBBB")
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
	encoded, err := vault.Seal([]byte("payload"))
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
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	return vault
}
