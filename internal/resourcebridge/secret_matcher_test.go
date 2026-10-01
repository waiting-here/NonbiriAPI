package resourcebridge

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type matcherVault struct {
	Vault
	opened []byte
}

func (v *matcherVault) OpenForGenerationTwoContext(ciphertext string, credentialContext secret.GenerationTwoEndpointKeyContext) ([]byte, error) {
	plaintext, err := v.Vault.OpenForGenerationTwoContext(ciphertext, credentialContext)
	v.opened = plaintext
	return plaintext, err
}
func TestMatchEndpointSecretKeepsPlaintextInsideVaultAndClearsIt(t *testing.T) {
	f := newBridgeFixture(t)
	stored := f.writeUnreferencedSecret(t, "body-only-match", true)
	spy := &matcherVault{Vault: f.vault}
	f.runtime.vault = spy
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, value := range []struct {
		body string
		want bool
	}{{"body-only-match", true}, {"Body-only-match", false}, {"body-only-match ", false}} {
		candidate := []byte(value.body)
		same, err := f.runtime.MatchEndpointSecret(context.Background(), tx, stored.RefID, candidate)
		if err != nil || same != value.want {
			t.Fatalf("body match=%t err=%v", same, err)
		}
		if string(candidate) != value.body || len(spy.opened) == 0 || !allZero(spy.opened) {
			t.Fatal("matching changed input or retained decrypted plaintext")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = f.runtime.MatchEndpointSecret(ctx, tx, stored.RefID, []byte("body-only-match")); err == nil {
		t.Fatal("canceled comparison succeeded")
	}
	if !allZero(spy.opened) {
		t.Fatal("canceled comparison retained plaintext")
	}
	if _, err = f.runtime.MatchEndpointSecret(context.Background(), tx, stored.RefID+1000, []byte("body-only-match")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing reference=%v", err)
	}
}
