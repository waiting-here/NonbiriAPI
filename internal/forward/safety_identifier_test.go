package forward

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

const testSafetyOrigin = "https://example.com:443"

type inspectingSubkeyDeriver struct {
	key      []byte
	wantInfo string
	err      error
	returned []byte
}

func (deriver *inspectingSubkeyDeriver) DeriveGenerationTwoSubkey(info []byte) ([]byte, error) {
	if string(info) != deriver.wantInfo {
		return nil, fmt.Errorf("unexpected subkey info %q", info)
	}
	if deriver.err != nil {
		return nil, deriver.err
	}
	deriver.returned = append([]byte(nil), deriver.key...)
	return deriver.returned, nil
}

func TestSafetyIdentifierKnownVectorAndScopeSeparation(t *testing.T) {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index)
	}
	deriver := &inspectingSubkeyDeriver{key: key, wantInfo: SafetyIdentifierSubkeyInfo}
	factory, err := NewSafetyIdentifierFactory(deriver)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = factory.Close() })
	if !bytes.Equal(deriver.returned, make([]byte, 32)) {
		t.Fatal("factory did not clear the temporary derived key")
	}

	const expected = "nbu_v3_NM6BXU63RFI3GVH33OZWLE46NBCXAHWEO44VIW7P7ED33KT5F2RA"
	first, err := factory.Generate(42, testSafetyOrigin)
	if err != nil || first != expected {
		t.Fatalf("known vector=%q err=%v", first, err)
	}
	second, err := factory.Generate(42, testSafetyOrigin)
	if err != nil || second != first {
		t.Fatalf("stable vector=%q err=%v", second, err)
	}
	if len(first) != safetyIdentifierLength || !strings.HasPrefix(first, safetyIdentifierPrefix) || strings.Contains(first, "=") {
		t.Fatalf("identifier is not canonical: %q", first)
	}

	for _, different := range []struct {
		userID int64
		origin string
	}{
		{userID: 43, origin: testSafetyOrigin},
		{userID: 42, origin: "http://example.com:80"},
		{userID: 42, origin: "https://other.example:443"},
		{userID: 42, origin: "https://example.com:8443"},
	} {
		value, generateErr := factory.Generate(different.userID, different.origin)
		if generateErr != nil || value == first {
			t.Fatalf("scope separation user=%d origin=%q value=%q err=%v", different.userID, different.origin, value, generateErr)
		}
	}
}

func TestSafetyIdentifierCanonicalOriginAndInvalidInputs(t *testing.T) {
	factory, err := NewSafetyIdentifierFactory(fixedSubkeyDeriver{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = factory.Close() })

	firstOrigin, err := canonicalOrigin("HTTPS://EXAMPLE.COM./api/v1")
	if err != nil {
		t.Fatal(err)
	}
	sameOrigin, err := canonicalOrigin("https://example.com:443/another/path")
	if err != nil {
		t.Fatal(err)
	}
	if firstOrigin != sameOrigin {
		t.Fatalf("path changed canonical origin: %q != %q", firstOrigin, sameOrigin)
	}
	first, _ := factory.Generate(7, firstOrigin)
	second, _ := factory.Generate(7, sameOrigin)
	if first == "" || first != second {
		t.Fatalf("same origin identifiers=%q/%q", first, second)
	}

	for _, invalid := range []string{
		"", "https://example.com", "https://EXAMPLE.com:443", "https://example.com:443/path", "not-an-origin",
	} {
		if value, generateErr := factory.Generate(7, invalid); !errors.Is(generateErr, errSafetyIdentifierInput) || value != "" {
			t.Fatalf("invalid origin %q value=%q err=%v", invalid, value, generateErr)
		}
	}
	for _, userID := range []int64{0, -1} {
		if value, generateErr := factory.Generate(userID, testSafetyOrigin); !errors.Is(generateErr, errSafetyIdentifierInput) || value != "" {
			t.Fatalf("invalid user %d value=%q err=%v", userID, value, generateErr)
		}
	}
}

func TestSafetyIdentifierConstructionCloseAndFormatting(t *testing.T) {
	deriveErr := errors.New("subkey unavailable")
	if factory, err := NewSafetyIdentifierFactory(&inspectingSubkeyDeriver{
		wantInfo: SafetyIdentifierSubkeyInfo, err: deriveErr,
	}); !errors.Is(err, deriveErr) || factory != nil {
		t.Fatalf("derivation failure factory=%v err=%v", factory, err)
	}
	for _, length := range []int{0, 31, 33} {
		factory, err := NewSafetyIdentifierFactory(&inspectingSubkeyDeriver{
			key: make([]byte, length), wantInfo: SafetyIdentifierSubkeyInfo,
		})
		if !errors.Is(err, errSafetyIdentifierInput) || factory != nil {
			t.Fatalf("derived length %d factory=%v err=%v", length, factory, err)
		}
	}

	key := []byte("0123456789abcdef0123456789abcdef")
	factory, err := NewSafetyIdentifierFactory(&inspectingSubkeyDeriver{key: key, wantInfo: SafetyIdentifierSubkeyInfo})
	if err != nil {
		t.Fatal(err)
	}
	formatted := fmt.Sprintf("%v %+v %#v", factory, factory, factory)
	for _, marker := range []string{"0123456789abcdef", "48 49 50 51", "0x30, 0x31, 0x32"} {
		if strings.Contains(formatted, marker) {
			t.Fatalf("routine formatting exposed derived key: %q", formatted)
		}
	}
	if err := factory.Close(); err != nil {
		t.Fatal(err)
	}
	if err := factory.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if !bytes.Equal(factory.key[:], make([]byte, 32)) {
		t.Fatal("close did not clear retained derived key")
	}
	if value, generateErr := factory.Generate(1, testSafetyOrigin); !errors.Is(generateErr, errSafetyIdentifierClosed) || value != "" {
		t.Fatalf("post-close value=%q err=%v", value, generateErr)
	}
}

func TestSafetyIdentifierConcurrentGenerateAndClose(t *testing.T) {
	factory, err := NewSafetyIdentifierFactory(fixedSubkeyDeriver{})
	if err != nil {
		t.Fatal(err)
	}
	want, err := factory.Generate(91, testSafetyOrigin)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 16
	start := make(chan struct{})
	ready := make(chan struct{}, workers)
	errorsFound := make(chan error, workers)
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			reported := false
			for {
				value, generateErr := factory.Generate(91, testSafetyOrigin)
				if !reported {
					ready <- struct{}{}
					reported = true
				}
				switch {
				case generateErr == nil && value == want:
					continue
				case errors.Is(generateErr, errSafetyIdentifierClosed) && value == "":
					return
				default:
					errorsFound <- fmt.Errorf("value=%q err=%v", value, generateErr)
					return
				}
			}
		}()
	}
	close(start)
	for worker := 0; worker < workers; worker++ {
		<-ready
	}
	if err := factory.Close(); err != nil {
		t.Fatal(err)
	}
	group.Wait()
	close(errorsFound)
	for generateErr := range errorsFound {
		t.Error(generateErr)
	}
}
