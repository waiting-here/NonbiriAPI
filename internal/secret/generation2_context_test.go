package secret

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func testGenerationTwoContext(t *testing.T, idHex string) GenerationTwoEndpointKeyContext {
	t.Helper()
	id, err := hex.DecodeString(idHex)
	if err != nil {
		t.Fatalf("decode context id: %v", err)
	}
	ctx, err := NewGenerationTwoEndpointKeyContext(id)
	clear(id)
	if err != nil {
		t.Fatalf("new generation two context: %v", err)
	}
	return ctx
}

func testGenerationTwoVault(t *testing.T) *Vault {
	t.Helper()
	key := make([]byte, MasterKeyBytes)
	for i := range key {
		key[i] = byte(i)
	}
	v, err := New(key)
	clear(key)
	if err != nil {
		t.Fatalf("new vault: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return v
}

func TestGenerationTwoContextAADGoldenAndIsolation(t *testing.T) {
	const idHex = "00112233445566778899aabbccddeeff"
	ctx := testGenerationTwoContext(t, idHex)
	aad, ok := ctx.associatedData()
	if !ok {
		t.Fatal("constructed context was rejected")
	}
	want := "0000000e67656e65726174696f6e5f74776f00000016656e64706f696e742d6b65792d7365637265742f76320000001000112233445566778899aabbccddeeff"
	if got := hex.EncodeToString(aad); got != want {
		t.Fatalf("AAD mismatch: got %s want %s", got, want)
	}
	clear(aad)

	// The constructor copies the input and ContextID returns another defensive
	// copy. Neither caller-owned slice can mutate future AAD.
	id, err := hex.DecodeString(idHex)
	if err != nil {
		t.Fatalf("decode context id for isolation check: %v", err)
	}
	isolated, err := NewGenerationTwoEndpointKeyContext(id)
	if err != nil {
		t.Fatalf("construct isolated context: %v", err)
	}
	id[0] ^= 0xff
	clear(id)
	returned := isolated.ContextID()
	returned[1] ^= 0xff
	clear(returned)
	aad, ok = isolated.associatedData()
	if !ok || !strings.HasSuffix(hex.EncodeToString(aad), idHex) {
		t.Fatal("context input or ContextID mutation changed the authenticated context")
	}
	clear(aad)

	zero := GenerationTwoEndpointKeyContext{}
	if zero.valid() || zero.ContextID() != nil {
		t.Fatal("zero-value context became usable")
	}
	if _, ok := zero.associatedData(); ok {
		t.Fatal("zero-value context produced AAD")
	}
	for _, size := range []int{0, 1, 15, 17, 32} {
		if _, err := NewGenerationTwoEndpointKeyContext(make([]byte, size)); !errors.Is(err, ErrInvalidContext) {
			t.Fatalf("context length %d error = %v", size, err)
		}
	}
	// BLOB16 is an opaque random identity; its all-zero byte representation is
	// structurally valid when explicitly constructed. The zero-value context
	// above remains invalid because it was never constructed.
	if allZero, err := NewGenerationTwoEndpointKeyContext(make([]byte, 16)); err != nil || !allZero.valid() {
		t.Fatalf("constructed all-zero context: valid=%v err=%v", allZero.valid(), err)
	}

	formatted := fmt.Sprintf("%+v %#v", ctx, ctx)
	if strings.Contains(strings.ToLower(formatted), idHex) || !strings.Contains(formatted, "redacted") {
		t.Fatalf("context formatting exposed identity: %s", formatted)
	}
}

func TestGenerationTwoContextRoundTripAndHostileEnvelopes(t *testing.T) {
	v := testGenerationTwoVault(t)
	ctx := testGenerationTwoContext(t, "00112233445566778899aabbccddeeff")
	other := testGenerationTwoContext(t, "ffeeddccbbaa99887766554433221100")
	plaintext := []byte("generation-two credential")
	envelope, err := v.SealForGenerationTwoContext(plaintext, ctx)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	secondEnvelope, err := v.SealForGenerationTwoContext(plaintext, ctx)
	if err != nil {
		t.Fatalf("second seal: %v", err)
	}
	if secondEnvelope == envelope {
		t.Fatal("two seals reused the same authenticated nonce/envelope")
	}
	if !strings.HasPrefix(envelope, contextEnvelopePrefix) {
		t.Fatalf("seal emitted an unexpected envelope prefix: %q", envelope[:min(len(envelope), len(contextEnvelopePrefix))])
	}
	if version, err := ParseEnvelopeVersion(envelope); err != nil || version != EnvelopeVersionV2 {
		t.Fatalf("parse sealed envelope: version=%v err=%v", version, err)
	}
	opened, err := v.OpenForGenerationTwoContext(envelope, ctx)
	if err != nil || !bytes.Equal(opened, plaintext) {
		clear(opened)
		t.Fatalf("round trip failed: err=%v", err)
	}
	clear(opened)

	if _, err := v.OpenForGenerationTwoContext(envelope, other); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("wrong context error = %v", err)
	}
	otherKey := bytes.Repeat([]byte{0xa5}, MasterKeyBytes)
	wrongKeyVault, err := New(otherKey)
	clear(otherKey)
	if err != nil {
		t.Fatalf("construct wrong-key vault: %v", err)
	}
	defer wrongKeyVault.Close()
	if _, err := wrongKeyVault.OpenForGenerationTwoContext(envelope, ctx); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("wrong key error = %v", err)
	}

	if _, err := v.SealForGenerationTwoContext(nil, ctx); !errors.Is(err, ErrInvalidPlaintext) {
		t.Fatalf("empty plaintext error = %v", err)
	}
	tooLarge := make([]byte, MaxPlaintextBytes+1)
	if _, err := v.SealForGenerationTwoContext(tooLarge, ctx); !errors.Is(err, ErrInvalidPlaintext) {
		t.Fatalf("oversized plaintext error = %v", err)
	}
	clear(tooLarge)

	// This is a canonical-shape retired envelope fixture. It must be rejected
	// by the closed parser before any key or authentication operation.
	legacy := "nbsec:" + "v1:aes-256-gcm:" + strings.Repeat("A", 16) + ":" + strings.Repeat("A", 23)
	nonCanonical := contextEnvelopePrefix + strings.Repeat("A", 16) + ":" + strings.Repeat("A", 22) + "A="
	unknown := "nbsec:v3:aes-256-gcm:" + strings.Repeat("A", 16) + ":" + strings.Repeat("A", 23)
	hostile := []string{
		"",
		legacy,
		unknown,
		nonCanonical,
		contextEnvelopePrefix + strings.Repeat("A", 16) + ":" + strings.Repeat("A", 23) + "=",
		contextEnvelopePrefix + strings.Repeat("A", 15) + ":" + strings.Repeat("A", 23),
		strings.Repeat("x", MaxEnvelopeBytes+1),
		envelope[:len(envelope)-1],
	}
	for i, encoded := range hostile {
		if _, err := ParseEnvelopeVersion(encoded); !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatalf("hostile envelope %d parser error = %v", i, err)
		}
		if _, err := v.OpenForGenerationTwoContext(encoded, ctx); !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatalf("hostile envelope %d open error = %v", i, err)
		}
	}

	for _, index := range []int{0, len(envelope) / 2, len(envelope) - 1} {
		mutated := []byte(envelope)
		mutated[index] ^= 1
		if _, err := v.OpenForGenerationTwoContext(string(mutated), ctx); !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatalf("tamper at %d error = %v", index, err)
		}
		clear(mutated)
	}

	for _, err := range []error{
		func() error { _, err := v.OpenForGenerationTwoContext(envelope, other); return err }(),
		func() error { _, err := wrongKeyVault.OpenForGenerationTwoContext(envelope, ctx); return err }(),
	} {
		if strings.Contains(err.Error(), string(plaintext)) || strings.Contains(err.Error(), envelope) || strings.Contains(err.Error(), "00112233") {
			t.Fatalf("secret material leaked through error: %v", err)
		}
	}
}

func TestGenerationTwoContextClosedVaultAndRedaction(t *testing.T) {
	v := testGenerationTwoVault(t)
	ctx := testGenerationTwoContext(t, "00112233445566778899aabbccddeeff")
	envelope, err := v.SealForGenerationTwoContext([]byte("close sentinel"), ctx)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := v.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := v.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	if _, err := v.SealForGenerationTwoContext([]byte("x"), ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed seal error = %v", err)
	}
	if _, err := v.OpenForGenerationTwoContext(envelope, ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed open error = %v", err)
	}
	if _, err := v.DeriveGenerationTwoSubkey([]byte("closed-check")); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed subkey error = %v", err)
	}
	formatted := fmt.Sprintf("%+v %#v", v, v)
	if strings.Contains(formatted, "close sentinel") || strings.Contains(formatted, "0 1 2") || !strings.Contains(formatted, "redacted") {
		t.Fatalf("vault formatting exposed key state: %s", formatted)
	}
}

func TestGenerationTwoContextConcurrentUse(t *testing.T) {
	v := testGenerationTwoVault(t)
	const workers = 16
	const rounds = 8
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := make([]byte, 16)
			id[0] = byte(worker)
			ctx, err := NewGenerationTwoEndpointKeyContext(id)
			clear(id)
			if err != nil {
				t.Errorf("worker %d context construction failed", worker)
				return
			}
			for round := 0; round < rounds; round++ {
				plaintext := []byte{byte(worker), byte(round), 'x'}
				envelope, err := v.SealForGenerationTwoContext(plaintext, ctx)
				if err != nil {
					t.Errorf("worker %d seal failed", worker)
					clear(plaintext)
					return
				}
				opened, err := v.OpenForGenerationTwoContext(envelope, ctx)
				if err != nil || !bytes.Equal(opened, plaintext) {
					t.Errorf("worker %d round %d round trip failed: err=%v", worker, round, err)
					clear(opened)
					clear(plaintext)
					return
				}
				clear(opened)
				clear(plaintext)
			}
		}()
	}
	wg.Wait()
}
