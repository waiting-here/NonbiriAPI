package secret

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func newTestVault(t *testing.T, fill byte) *Vault {
	t.Helper()
	key := bytes.Repeat([]byte{fill}, MasterKeyBytes)
	v, err := New(key)
	clear(key)
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return v
}

func TestSealOpenRoundTripUsesFreshNonce(t *testing.T) {
	v := newTestVault(t, 0x31)
	plaintext := []byte("upstream-credential-sentinel")

	first, err := v.Seal(plaintext)
	if err != nil {
		t.Fatalf("first Seal returned an error: %v", err)
	}
	second, err := v.Seal(plaintext)
	if err != nil {
		t.Fatalf("second Seal returned an error: %v", err)
	}
	if first == second {
		t.Fatal("two Seal calls produced the same envelope")
	}
	if !strings.HasPrefix(first, envelopePrefix) {
		t.Fatal("Seal did not emit the documented versioned envelope")
	}
	if strings.Contains(first, string(plaintext)) || strings.Contains(second, string(plaintext)) {
		t.Fatal("ciphertext envelope contains the plaintext sample")
	}

	firstNonce, _, ok := parseEnvelope(first, legacyEnvelopePrefix)
	if !ok {
		t.Fatal("first envelope did not parse")
	}
	secondNonce, _, ok := parseEnvelope(second, legacyEnvelopePrefix)
	if !ok {
		t.Fatal("second envelope did not parse")
	}
	if bytes.Equal(firstNonce, secondNonce) {
		t.Fatal("two Seal calls reused a nonce")
	}

	opened, err := v.Open(first)
	if err != nil {
		t.Fatalf("Open returned an error: %v", err)
	}
	defer clear(opened)
	if !bytes.Equal(opened, plaintext) {
		t.Fatal("round trip changed the plaintext")
	}
}

func TestOpenRejectsEveryTamperedNonceCiphertextAndTagByte(t *testing.T) {
	v := newTestVault(t, 0x42)
	envelope, err := v.Seal([]byte("authenticated-boundary-sample"))
	if err != nil {
		t.Fatalf("Seal returned an error: %v", err)
	}
	nonce, sealed, ok := parseEnvelope(envelope, legacyEnvelopePrefix)
	if !ok {
		t.Fatal("fresh envelope did not parse")
	}

	assertRejected := func(candidate string) {
		t.Helper()
		opened, err := v.Open(candidate)
		clear(opened)
		if !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatalf("tampered envelope returned %v, want ErrInvalidCiphertext", err)
		}
	}
	makeEnvelope := func(n, payload []byte) string {
		return envelopePrefix + base64.RawURLEncoding.EncodeToString(n) + ":" +
			base64.RawURLEncoding.EncodeToString(payload)
	}

	for i := range nonce {
		mutated := append([]byte(nil), nonce...)
		mutated[i] ^= 0x01
		assertRejected(makeEnvelope(mutated, sealed))
	}
	for i := range sealed {
		mutated := append([]byte(nil), sealed...)
		mutated[i] ^= 0x01
		assertRejected(makeEnvelope(nonce, mutated))
	}
}

func TestOpenStrictlyRejectsMalformedEnvelopes(t *testing.T) {
	v := newTestVault(t, 0x53)
	envelope, err := v.Seal([]byte("strict-envelope-sample"))
	if err != nil {
		t.Fatalf("Seal returned an error: %v", err)
	}
	nonce, sealed, ok := parseEnvelope(envelope, legacyEnvelopePrefix)
	if !ok {
		t.Fatal("fresh envelope did not parse")
	}

	candidates := []string{
		"",
		strings.Replace(envelope, ":v1:", ":v2:", 1),
		strings.Replace(envelope, "aes-256-gcm", "aes-256-cbc", 1),
		"other:v1:aes-256-gcm:" + strings.TrimPrefix(envelope, envelopePrefix),
		envelope + "A",
		envelope + "=",
		envelope + ":garbage",
		envelope + "\n",
		envelope + " ",
		envelopePrefix + "*" + ":" + base64.RawURLEncoding.EncodeToString(sealed),
		envelopePrefix + base64.RawURLEncoding.EncodeToString(nonce) + ":*",
		envelopePrefix + base64.RawURLEncoding.EncodeToString(nonce[:len(nonce)-1]) + ":" + base64.RawURLEncoding.EncodeToString(sealed),
		envelopePrefix + base64.RawURLEncoding.EncodeToString(append(nonce, 0)) + ":" + base64.RawURLEncoding.EncodeToString(sealed),
		envelopePrefix + base64.RawURLEncoding.EncodeToString(nonce) + ":" + base64.RawURLEncoding.EncodeToString(sealed[:gcmTagBytes]),
	}
	for i, candidate := range candidates {
		opened, err := v.Open(candidate)
		clear(opened)
		if !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatalf("malformed candidate %d returned %v, want ErrInvalidCiphertext", i, err)
		}
	}

	for i := 0; i < len(envelope); i++ {
		opened, err := v.Open(envelope[:i])
		clear(opened)
		if !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatalf("truncation at byte %d returned %v, want ErrInvalidCiphertext", i, err)
		}
	}

	oversized := envelopePrefix + strings.Repeat("A", base64.RawURLEncoding.EncodedLen(gcmNonceBytes)) + ":" +
		strings.Repeat("A", base64.RawURLEncoding.EncodedLen(MaxPlaintextBytes+gcmTagBytes)+1)
	opened, err := v.Open(oversized)
	clear(opened)
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("oversized envelope returned %v, want ErrInvalidCiphertext", err)
	}
}

func TestErrorsNeverContainSecretInputs(t *testing.T) {
	invalidKey := []byte("invalid-master-key-marker")
	_, err := New(invalidKey)
	if !errors.Is(err, ErrInvalidMasterKey) || strings.Contains(err.Error(), string(invalidKey)) {
		t.Fatal("master-key validation error exposed its input")
	}

	v := newTestVault(t, 0x5d)
	oversized := append([]byte("plaintext-error-marker"), make([]byte, MaxPlaintextBytes)...)
	_, err = v.Seal(oversized)
	clear(oversized)
	if !errors.Is(err, ErrInvalidPlaintext) || strings.Contains(err.Error(), "plaintext-error-marker") {
		t.Fatal("plaintext validation error exposed its input")
	}

	malformed := "ciphertext-error-marker"
	opened, err := v.Open(malformed)
	clear(opened)
	if !errors.Is(err, ErrInvalidCiphertext) || strings.Contains(err.Error(), malformed) {
		t.Fatal("ciphertext validation error exposed its input")
	}
}

func TestWrongMasterKeyCannotDecrypt(t *testing.T) {
	first := newTestVault(t, 0x61)
	second := newTestVault(t, 0x62)
	envelope, err := first.Seal([]byte("wrong-key-sample"))
	if err != nil {
		t.Fatalf("Seal returned an error: %v", err)
	}
	opened, err := second.Open(envelope)
	clear(opened)
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("wrong-key Open returned %v, want ErrInvalidCiphertext", err)
	}
	if strings.Contains(err.Error(), envelope) {
		t.Fatal("wrong-key error contains ciphertext material")
	}
}

func TestPlaintextBoundsUnicodeAndBinary(t *testing.T) {
	v := newTestVault(t, 0x74)
	for _, plaintext := range [][]byte{
		[]byte("日本語の鍵-🔐-é"),
		{0x00, 0xff, 0xfe, 0x80, 0x01, 0x7f},
		bytes.Repeat([]byte{0xa5}, MaxPlaintextBytes),
	} {
		envelope, err := v.Seal(plaintext)
		if err != nil {
			t.Fatalf("Seal rejected an in-bound plaintext: %v", err)
		}
		opened, err := v.Open(envelope)
		if err != nil {
			t.Fatalf("Open rejected an in-bound plaintext: %v", err)
		}
		if !bytes.Equal(opened, plaintext) {
			clear(opened)
			t.Fatal("binary or Unicode round trip changed bytes")
		}
		clear(opened)
	}

	for _, plaintext := range [][]byte{nil, {}, make([]byte, MaxPlaintextBytes+1)} {
		if envelope, err := v.Seal(plaintext); envelope != "" || !errors.Is(err, ErrInvalidPlaintext) {
			t.Fatalf("out-of-bound Seal returned an unexpected result: %v", err)
		}
	}
}

func TestAuthenticatedEmptyPlaintextIsStillRejected(t *testing.T) {
	v := newTestVault(t, 0x7a)
	v.mu.RLock()
	gcm, err := v.gcmLocked()
	v.mu.RUnlock()
	if err != nil {
		t.Fatalf("construct GCM: %v", err)
	}
	nonce := bytes.Repeat([]byte{0x19}, gcmNonceBytes)
	sealed := gcm.Seal(nil, nonce, nil, []byte(envelopeHeader))
	envelope := envelopePrefix + base64.RawURLEncoding.EncodeToString(nonce) + ":" +
		base64.RawURLEncoding.EncodeToString(sealed)

	opened, err := v.Open(envelope)
	clear(opened)
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("authenticated empty plaintext returned %v, want ErrInvalidCiphertext", err)
	}
}

func TestMasterKeyOwnershipCloseAndSafeFormatting(t *testing.T) {
	for _, size := range []int{0, MasterKeyBytes - 1, MasterKeyBytes + 1} {
		if v, err := New(make([]byte, size)); v != nil || !errors.Is(err, ErrInvalidMasterKey) {
			t.Fatalf("New accepted master-key size %d", size)
		}
	}

	key := bytes.Repeat([]byte{0xbd}, MasterKeyBytes)
	keyMarker := hex.EncodeToString(key)
	v, err := New(key)
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	clear(key)

	envelope, err := v.Seal([]byte("ownership-copy-sample"))
	if err != nil {
		t.Fatalf("Seal failed after caller input was cleared: %v", err)
	}
	opened, err := v.Open(envelope)
	if err != nil {
		t.Fatalf("Open failed after caller input was cleared: %v", err)
	}
	clear(opened)

	formatted := fmt.Sprintf("%v %#v", v, v)
	//lint:ignore SA9005 Deliberately verify that an opaque Vault serializes without exposing its unexported key state.
	jsonValue, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal Vault: %v", err)
	}
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logOutput, nil))
	logger.Info("vault formatting", "value", v)
	for _, output := range []string{formatted, string(jsonValue), logOutput.String()} {
		if strings.Contains(output, keyMarker) || strings.Contains(output, "189 189 189") || strings.Contains(output, "0xbd") {
			t.Fatal("a formatting path exposed master-key bytes")
		}
	}

	if err := v.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if err := v.Close(); err != nil {
		t.Fatalf("second Close returned an error: %v", err)
	}
	for i, b := range v.key {
		if b != 0 {
			t.Fatalf("retained master-key byte %d was not cleared", i)
		}
	}
	if _, err := v.Seal([]byte("after-close")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Seal after Close returned %v, want ErrClosed", err)
	}
	if opened, err := v.Open(envelope); !errors.Is(err, ErrClosed) {
		clear(opened)
		t.Fatalf("Open after Close returned %v, want ErrClosed", err)
	}
}

func TestConcurrentSealOpen(t *testing.T) {
	v := newTestVault(t, 0x8c)
	const workers = 32
	const iterations = 64

	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				plaintext := []byte(fmt.Sprintf("worker-%d-iteration-%d", worker, iteration))
				envelope, err := v.Seal(plaintext)
				if err != nil {
					errs <- errors.New("concurrent Seal failed")
					return
				}
				opened, err := v.Open(envelope)
				if err != nil {
					errs <- errors.New("concurrent Open failed")
					return
				}
				equal := bytes.Equal(opened, plaintext)
				clear(opened)
				if !equal {
					errs <- errors.New("concurrent round trip changed bytes")
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestDeriveSubkeyIsPurposeBoundStableAndOneWay(t *testing.T) {
	v := newTestVault(t, 0x9a)
	admin, err := v.DeriveSubkey([]byte("admin-cred-gen-v1"))
	if err != nil {
		t.Fatalf("first DeriveSubkey returned an error: %v", err)
	}
	defer clear(admin)
	if len(admin) != SubkeyBytes {
		t.Fatalf("subkey length=%d, want %d", len(admin), SubkeyBytes)
	}

	adminAgain, err := v.DeriveSubkey([]byte("admin-cred-gen-v1"))
	if err != nil {
		t.Fatalf("second DeriveSubkey returned an error: %v", err)
	}
	defer clear(adminAgain)
	if !bytes.Equal(admin, adminAgain) {
		t.Fatal("the same info label produced a different subkey")
	}

	other, err := v.DeriveSubkey([]byte("endpoint-key-envelope-v1"))
	if err != nil {
		t.Fatalf("third DeriveSubkey returned an error: %v", err)
	}
	defer clear(other)
	if bytes.Equal(admin, other) {
		t.Fatal("different info labels produced the same subkey")
	}

	// The derived subkey must not equal or contain the master-key material.
	masterMarker := hex.EncodeToString(bytes.Repeat([]byte{0x9a}, MasterKeyBytes))
	adminHex := hex.EncodeToString(admin)
	if adminHex == masterMarker || strings.Contains(adminHex, masterMarker) {
		t.Fatal("a derived subkey exposed master-key material")
	}

	// A second vault with a different master key derives a different subkey
	// for the same info label, so the subkey is bound to the master key.
	v2 := newTestVault(t, 0x9b)
	admin2, err := v2.DeriveSubkey([]byte("admin-cred-gen-v1"))
	if err != nil {
		t.Fatalf("v2 DeriveSubkey returned an error: %v", err)
	}
	defer clear(admin2)
	if bytes.Equal(admin, admin2) {
		t.Fatal("different master keys produced the same subkey")
	}
}

func TestDeriveSubkeyRejectsBadInfoAndClosedVault(t *testing.T) {
	v := newTestVault(t, 0x9c)
	for _, info := range [][]byte{nil, {}, bytes.Repeat([]byte{0x01}, maxSubkeyInfoBytes+1)} {
		if _, err := v.DeriveSubkey(info); !errors.Is(err, ErrInvalidSubkeyInfo) {
			t.Fatalf("info len=%d returned %v, want ErrInvalidSubkeyInfo", len(info), err)
		}
	}
	// An exactly-max info label is accepted.
	maxInfo := bytes.Repeat([]byte{0x02}, maxSubkeyInfoBytes)
	out, err := v.DeriveSubkey(maxInfo)
	if err != nil {
		t.Fatalf("max-length info returned an error: %v", err)
	}
	clear(out)

	if err := v.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if _, err := v.DeriveSubkey([]byte("admin-cred-gen-v1")); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed vault DeriveSubkey returned %v, want ErrClosed", err)
	}
}

func TestDeriveSubkeyIsConcurrencySafeWithSealOpen(t *testing.T) {
	v := newTestVault(t, 0x8d)
	const workers = 16
	const iterations = 32
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				if worker%2 == 0 {
					subkey, err := v.DeriveSubkey([]byte(fmt.Sprintf("purpose-%d", worker)))
					if err != nil {
						errs <- fmt.Errorf("concurrent DeriveSubkey failed: %w", err)
						return
					}
					clear(subkey)
				} else {
					plaintext := []byte(fmt.Sprintf("mix-worker-%d-iter-%d", worker, iteration))
					envelope, err := v.Seal(plaintext)
					if err != nil {
						errs <- errors.New("concurrent Seal failed")
						return
					}
					opened, err := v.Open(envelope)
					if err != nil {
						errs <- errors.New("concurrent Open failed")
						return
					}
					clear(opened)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
