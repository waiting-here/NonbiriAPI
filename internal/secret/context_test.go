package secret

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

const testCanonicalOrigin = "https://example.com:443"

func endpointContext(t *testing.T, userID, endpointID, keyID int64, origin string) EndpointKeyContext {
	t.Helper()
	ctx, err := NewEndpointKeyContext(userID, endpointID, keyID, origin)
	if err != nil {
		t.Fatalf("NewEndpointKeyContext returned an error: %v", err)
	}
	return ctx
}

func TestContextEnvelopeRoundTripUsesFreshNonce(t *testing.T) {
	v := newTestVault(t, 0x21)
	ctx := endpointContext(t, 7, 11, 13, testCanonicalOrigin)
	plaintext := []byte("context-bound-upstream-credential")

	first, err := v.SealForContext(plaintext, ctx)
	if err != nil {
		t.Fatalf("first SealForContext: %v", err)
	}
	second, err := v.SealForContext(plaintext, ctx)
	if err != nil {
		t.Fatalf("second SealForContext: %v", err)
	}
	if first == second {
		t.Fatal("contextual seals reused an envelope")
	}
	if !strings.HasPrefix(first, contextEnvelopePrefix) || strings.Contains(first, string(plaintext)) {
		t.Fatal("contextual envelope has an invalid prefix or exposes plaintext")
	}
	firstNonce, _, ok := parseEnvelope(first, contextEnvelopePrefix)
	if !ok {
		t.Fatal("first contextual envelope is not canonical")
	}
	secondNonce, _, ok := parseEnvelope(second, contextEnvelopePrefix)
	if !ok {
		t.Fatal("second contextual envelope is not canonical")
	}
	if bytes.Equal(firstNonce, secondNonce) {
		t.Fatal("contextual seals reused a nonce")
	}

	opened, err := v.OpenForContext(first, ctx)
	if err != nil {
		t.Fatalf("OpenForContext: %v", err)
	}
	defer clear(opened)
	if !bytes.Equal(opened, plaintext) {
		t.Fatal("contextual round trip changed plaintext")
	}
	if version, err := ParseEnvelopeVersion(first); err != nil || version != EnvelopeVersionV2 {
		t.Fatalf("contextual envelope version=(%d,%v), want v2", version, err)
	}
}

func TestContextEnvelopeRejectsEveryContextExchange(t *testing.T) {
	v := newTestVault(t, 0x32)
	original := endpointContext(t, 101, 202, 303, "https://one.example:443")
	envelope, err := v.SealForContext([]byte("context-exchange-sentinel"), original)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		ctx  EndpointKeyContext
	}{
		{"user", endpointContext(t, 102, 202, 303, "https://one.example:443")},
		{"endpoint", endpointContext(t, 101, 203, 303, "https://one.example:443")},
		{"key", endpointContext(t, 101, 202, 304, "https://one.example:443")},
		{"origin", endpointContext(t, 101, 202, 303, "https://two.example:443")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened, err := v.OpenForContext(envelope, tc.ctx)
			clear(opened)
			if !errors.Is(err, ErrInvalidCiphertext) {
				t.Fatalf("context exchange returned %v, want ErrInvalidCiphertext", err)
			}
			if err != ErrInvalidCiphertext || strings.Contains(err.Error(), envelope) || strings.Contains(err.Error(), "one.example") {
				t.Fatal("context exchange did not collapse to the opaque sentinel")
			}
		})
	}
}

func TestContextAssociatedDataIsLengthPrefixedAndCollisionFree(t *testing.T) {
	left := endpointContext(t, 1, 23, 456, testCanonicalOrigin)
	right := endpointContext(t, 12, 3, 456, testCanonicalOrigin)
	leftAAD, ok := left.associatedData()
	if !ok {
		t.Fatal("left context did not encode")
	}
	defer clear(leftAAD)
	rightAAD, ok := right.associatedData()
	if !ok {
		t.Fatal("right context did not encode")
	}
	defer clear(rightAAD)
	if bytes.Equal(leftAAD, rightAAD) {
		t.Fatal("ambiguous adjacent identifiers collided")
	}

	fields := make([]string, 0, contextAssociatedFields)
	remaining := leftAAD
	for len(remaining) > 0 {
		if len(remaining) < contextLengthPrefixBytes {
			t.Fatal("associated data ended inside a length prefix")
		}
		n := int(binary.BigEndian.Uint32(remaining[:contextLengthPrefixBytes]))
		remaining = remaining[contextLengthPrefixBytes:]
		if n > len(remaining) {
			t.Fatal("associated data field length exceeds payload")
		}
		fields = append(fields, string(remaining[:n]))
		remaining = remaining[n:]
	}
	want := []string{endpointKeyPurpose, contextEnvelopeVersion, "1", "23", "456", testCanonicalOrigin}
	if len(fields) != len(want) {
		t.Fatalf("associated field count=%d, want %d", len(fields), len(want))
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Fatalf("associated field %d differs", i)
		}
	}
}

func TestEndpointKeyContextRejectsNonCanonicalShapesWithoutEcho(t *testing.T) {
	invalid := []string{
		"",
		"HTTPS://example.com:443",
		"https://EXAMPLE.com:443",
		"https://example.com",
		"https://example.com:0443",
		"https://example.com:443/",
		"https://example.com:443/path",
		"https://user@example.com:443",
		"https://example.com:443?query",
		"https://example.com:443#fragment",
		"https://example.com.:443",
		"https://a..example.com:443",
		"https://éxample.com:443",
		"https://example.com:0",
		"ftp://example.com:21",
		"https://example.com:443\ncontext-marker",
		strings.Repeat("a", maxCanonicalOriginBytes+1),
	}
	for i, origin := range invalid {
		ctx, err := NewEndpointKeyContext(1, 2, 3, origin)
		if ctx != (EndpointKeyContext{}) || !errors.Is(err, ErrInvalidContext) {
			t.Fatalf("invalid origin %d returned (%v,%v)", i, ctx, err)
		}
		if (origin != "" && strings.Contains(err.Error(), origin)) || strings.Contains(err.Error(), "context-marker") {
			t.Fatal("invalid-context error echoed input")
		}
	}
	for _, ids := range [][3]int64{{0, 2, 3}, {-1, 2, 3}, {1, 0, 3}, {1, 2, 0}} {
		if _, err := NewEndpointKeyContext(ids[0], ids[1], ids[2], testCanonicalOrigin); !errors.Is(err, ErrInvalidContext) {
			t.Fatalf("invalid identifiers %v returned %v", ids, err)
		}
	}
}

func TestOpenForContextStrictlyRejectsMalformedAndLegacyEnvelopes(t *testing.T) {
	v := newTestVault(t, 0x43)
	ctx := endpointContext(t, 1, 2, 3, testCanonicalOrigin)
	envelope, err := v.SealForContext([]byte("strict-context-envelope"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	nonce, sealed, ok := parseEnvelope(envelope, contextEnvelopePrefix)
	if !ok {
		t.Fatal("fresh contextual envelope did not parse")
	}
	legacy, err := v.Seal([]byte("legacy-must-not-open-at-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	candidates := []string{
		"",
		legacy,
		strings.Replace(envelope, ":v2:", ":v3:", 1),
		strings.Replace(envelope, "aes-256-gcm", "aes-256-cbc", 1),
		envelope + "A",
		envelope + "=",
		envelope + ":trailing",
		envelope + "\n",
		contextEnvelopePrefix + "*:" + base64.RawURLEncoding.EncodeToString(sealed),
		contextEnvelopePrefix + base64.RawURLEncoding.EncodeToString(nonce) + ":*",
		contextEnvelopePrefix + base64.RawURLEncoding.EncodeToString(nonce[:len(nonce)-1]) + ":" + base64.RawURLEncoding.EncodeToString(sealed),
		contextEnvelopePrefix + base64.RawURLEncoding.EncodeToString(append(bytes.Clone(nonce), 0)) + ":" + base64.RawURLEncoding.EncodeToString(sealed),
		contextEnvelopePrefix + base64.RawURLEncoding.EncodeToString(nonce) + ":" + base64.RawURLEncoding.EncodeToString(sealed[:gcmTagBytes]),
	}
	for i, candidate := range candidates {
		opened, err := v.OpenForContext(candidate, ctx)
		clear(opened)
		if !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatalf("malformed candidate %d returned %v", i, err)
		}
	}
	for i := 0; i < len(envelope); i++ {
		opened, err := v.OpenForContext(envelope[:i], ctx)
		clear(opened)
		if !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatalf("truncation %d returned %v", i, err)
		}
	}
	oversized := contextEnvelopePrefix + strings.Repeat("A", base64.RawURLEncoding.EncodedLen(gcmNonceBytes)) + ":" +
		strings.Repeat("A", base64.RawURLEncoding.EncodedLen(MaxPlaintextBytes+gcmTagBytes)+1)
	if opened, err := v.OpenForContext(oversized, ctx); !errors.Is(err, ErrInvalidCiphertext) {
		clear(opened)
		t.Fatalf("oversized contextual envelope returned %v", err)
	}
}

func TestOpenForContextRejectsEveryTamperedNonceCiphertextAndTagByte(t *testing.T) {
	v := newTestVault(t, 0x54)
	ctx := endpointContext(t, 8, 9, 10, testCanonicalOrigin)
	envelope, err := v.SealForContext([]byte("tamper-context-envelope"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	nonce, sealed, ok := parseEnvelope(envelope, contextEnvelopePrefix)
	if !ok {
		t.Fatal("fresh contextual envelope did not parse")
	}
	makeEnvelope := func(n, payload []byte) string {
		return encodeEnvelope(contextEnvelopePrefix, n, payload)
	}
	for i := range nonce {
		mutated := bytes.Clone(nonce)
		mutated[i] ^= 1
		opened, err := v.OpenForContext(makeEnvelope(mutated, sealed), ctx)
		clear(opened)
		if err != ErrInvalidCiphertext {
			t.Fatalf("tampered nonce byte %d returned %v", i, err)
		}
	}
	for i := range sealed {
		mutated := bytes.Clone(sealed)
		mutated[i] ^= 1
		opened, err := v.OpenForContext(makeEnvelope(nonce, mutated), ctx)
		clear(opened)
		if err != ErrInvalidCiphertext {
			t.Fatalf("tampered sealed byte %d returned %v", i, err)
		}
	}
}

func TestContextEnvelopeRejectsAuthenticatedEmptyPlaintext(t *testing.T) {
	v := newTestVault(t, 0x55)
	ctx := endpointContext(t, 8, 9, 10, testCanonicalOrigin)
	aad, ok := ctx.associatedData()
	if !ok {
		t.Fatal("context did not encode")
	}
	defer clear(aad)
	v.mu.RLock()
	gcm, err := v.gcmLocked()
	v.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{0x23}, gcmNonceBytes)
	sealed := gcm.Seal(nil, nonce, nil, aad)
	encoded := encodeEnvelope(contextEnvelopePrefix, nonce, sealed)
	opened, err := v.OpenForContext(encoded, ctx)
	clear(opened)
	if err != ErrInvalidCiphertext {
		t.Fatalf("authenticated empty plaintext returned %v", err)
	}
}

func TestContextEnvelopeWrongKeyBoundsClosedAndVersionIsolation(t *testing.T) {
	ctx := endpointContext(t, 2, 4, 6, testCanonicalOrigin)
	first := newTestVault(t, 0x65)
	second := newTestVault(t, 0x66)
	envelope, err := first.SealForContext([]byte("wrong-key-context"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if opened, err := second.OpenForContext(envelope, ctx); err != ErrInvalidCiphertext {
		clear(opened)
		t.Fatalf("wrong key returned %v", err)
	}
	if opened, err := first.Open(envelope); err != ErrInvalidCiphertext {
		clear(opened)
		t.Fatalf("legacy Open accepted v2: %v", err)
	}
	for _, plaintext := range [][]byte{nil, {}, make([]byte, MaxPlaintextBytes+1)} {
		if encoded, err := first.SealForContext(plaintext, ctx); encoded != "" || err != ErrInvalidPlaintext {
			t.Fatalf("out-of-bound contextual seal returned (%q,%v)", encoded, err)
		}
	}
	ceiling := bytes.Repeat([]byte{0xa7}, MaxPlaintextBytes)
	encoded, err := first.SealForContext(ceiling, ctx)
	if err != nil {
		t.Fatalf("ceiling contextual seal: %v", err)
	}
	opened, err := first.OpenForContext(encoded, ctx)
	if err != nil || !bytes.Equal(opened, ceiling) {
		clear(opened)
		t.Fatalf("ceiling contextual open: %v", err)
	}
	clear(opened)
	clear(ceiling)

	closedKey := bytes.Repeat([]byte{0x67}, MasterKeyBytes)
	closed, err := New(closedKey)
	clear(closedKey)
	if err != nil {
		t.Fatal(err)
	}
	closedEnvelope, err := closed.SealForContext([]byte("closed-context"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	if _, err := closed.SealForContext([]byte("x"), ctx); err != ErrClosed {
		t.Fatalf("SealForContext after Close: %v", err)
	}
	if opened, err := closed.OpenForContext(closedEnvelope, ctx); err != ErrClosed {
		clear(opened)
		t.Fatalf("OpenForContext after Close: %v", err)
	}
	if _, err := closed.SealForContext([]byte("x"), EndpointKeyContext{}); err != ErrInvalidContext {
		t.Fatalf("invalid context seal returned %v", err)
	}
	if opened, err := first.OpenForContext(envelope, EndpointKeyContext{}); err != ErrInvalidCiphertext {
		clear(opened)
		t.Fatalf("invalid context open returned %v", err)
	}
}

func TestContextSafeFormattingAndConcurrentUse(t *testing.T) {
	originMarker := "https://format-marker.example:443"
	ctx := endpointContext(t, 712345678901, 723456789012, 734567890123, originMarker)
	var logs bytes.Buffer
	slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
		if attr.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return attr
	}})).Info("context", "value", ctx)
	for _, rendered := range []string{fmt.Sprint(ctx), fmt.Sprintf("%#v", ctx), logs.String()} {
		if strings.Contains(rendered, originMarker) || strings.Contains(rendered, "712345678901") {
			t.Fatal("context formatting exposed authenticated values")
		}
	}

	v := newTestVault(t, 0x76)
	const workers = 16
	const iterations = 32
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := 1; worker <= workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			workerCtx, err := NewEndpointKeyContext(int64(worker), int64(worker+100), int64(worker+200), testCanonicalOrigin)
			if err != nil {
				errs <- errors.New("context construction failed")
				return
			}
			for iteration := 0; iteration < iterations; iteration++ {
				plaintext := []byte(fmt.Sprintf("worker-%d-iteration-%d", worker, iteration))
				envelope, err := v.SealForContext(plaintext, workerCtx)
				if err != nil {
					errs <- errors.New("concurrent contextual seal failed")
					return
				}
				opened, err := v.OpenForContext(envelope, workerCtx)
				if err != nil || !bytes.Equal(opened, plaintext) {
					clear(opened)
					errs <- errors.New("concurrent contextual open failed")
					return
				}
				clear(opened)
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

func TestParseEnvelopeVersionRejectsMalformedAndFuture(t *testing.T) {
	v := newTestVault(t, 0x77)
	legacy, err := v.Seal([]byte("legacy"))
	if err != nil {
		t.Fatal(err)
	}
	if version, err := ParseEnvelopeVersion(legacy); err != nil || version != EnvelopeVersionV1 {
		t.Fatalf("legacy version=(%d,%v)", version, err)
	}
	ctx := endpointContext(t, 1, 2, 3, testCanonicalOrigin)
	current, err := v.SealForContext([]byte("current"), ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{
		strings.Replace(current, ":v2:", ":v9:", 1),
		current + "=",
		"nbsec:v2:aes-256-gcm:AAAA:BBBB",
		"not-an-envelope",
	} {
		if version, err := ParseEnvelopeVersion(candidate); version != 0 || err != ErrInvalidCiphertext {
			t.Fatalf("invalid envelope classified as (%d,%v)", version, err)
		}
	}
}
