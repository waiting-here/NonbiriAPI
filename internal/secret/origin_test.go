package secret_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestContextUsesEgressCanonicalOriginAcrossPathChanges(t *testing.T) {
	_, firstOrigin, err := egress.CanonicalEndpointTarget("HTTPS://EXAMPLE.COM./api//v1")
	if err != nil {
		t.Fatal(err)
	}
	_, nextOrigin, err := egress.CanonicalEndpointTarget("https://example.com:443/another/path")
	if err != nil {
		t.Fatal(err)
	}
	if firstOrigin != nextOrigin {
		t.Fatalf("same effective origin did not canonicalize equally")
	}
	_, otherOrigin, err := egress.CanonicalEndpointTarget("https://other.example/api/v1")
	if err != nil {
		t.Fatal(err)
	}

	key := bytes.Repeat([]byte{0x48}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	firstContext, err := secret.NewEndpointKeyContext(1, 2, 3, firstOrigin)
	if err != nil {
		t.Fatal(err)
	}
	nextContext, err := secret.NewEndpointKeyContext(1, 2, 3, nextOrigin)
	if err != nil {
		t.Fatal(err)
	}
	otherContext, err := secret.NewEndpointKeyContext(1, 2, 3, otherOrigin)
	if err != nil {
		t.Fatal(err)
	}

	envelope, err := vault.SealForContext([]byte("path-independent-origin"), firstContext)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := vault.OpenForContext(envelope, nextContext)
	if err != nil {
		t.Fatalf("same-origin path change failed to open: %v", err)
	}
	clear(opened)
	opened, err = vault.OpenForContext(envelope, otherContext)
	clear(opened)
	if !errors.Is(err, secret.ErrInvalidCiphertext) {
		t.Fatalf("cross-origin context returned %v", err)
	}
}
