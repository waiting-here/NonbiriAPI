package contract

import (
	"fmt"
	"testing"
)

func TestShortLivedSecretTransfersExactlyOnceAndRedactsFormatting(t *testing.T) {
	plaintext := []byte("plain-secret")
	ciphertext := []byte("cipher-secret")
	secret := NewShortLivedSecret(plaintext, ciphertext)
	if got := fmt.Sprintf("%v %#v", secret, secret); got != "[redacted upstream credential] [redacted upstream credential]" {
		t.Fatalf("secret formatting was not redacted: %q", got)
	}
	gotPlain, gotCipher, ok := secret.Take()
	if !ok || string(gotPlain) != "plain-secret" || string(gotCipher) != "cipher-secret" {
		t.Fatalf("first Take = %q %q %v", gotPlain, gotCipher, ok)
	}
	if secondPlain, secondCipher, secondOK := secret.Take(); secondOK || secondPlain != nil || secondCipher != nil {
		t.Fatalf("second Take unexpectedly succeeded: %q %q %v", secondPlain, secondCipher, secondOK)
	}
	clear(gotPlain)
	clear(gotCipher)
	secret.Clear()
}

func TestCapabilitySetRequiresAllBits(t *testing.T) {
	set := CapabilitySet(CapabilityText | CapabilityStream)
	if !set.Has(CapabilityText) || !set.HasAll(CapabilitySet(CapabilityText|CapabilityStream)) {
		t.Fatal("declared capability was not found")
	}
	if set.Has(CapabilityTools) || set.HasAll(CapabilitySet(CapabilityText|CapabilityTools)) {
		t.Fatal("undeclared capability was admitted")
	}
}
