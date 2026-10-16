package secret

import (
	"bytes"
	"testing"
)

func TestAccountMFAEncryptionPurposeAndContext(t *testing.T) {
	vault, err := New(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	seed := bytes.Repeat([]byte{7}, 20)
	contextID := bytes.Repeat([]byte{9}, 16)
	encoded, err := vault.SealAccountMFA(seed, contextID)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := vault.OpenAccountMFA(encoded, contextID)
	if err != nil || !bytes.Equal(opened, seed) {
		t.Fatal("MFA roundtrip failed", err)
	}
	clear(opened)
	other := bytes.Repeat([]byte{8}, 16)
	if _, err = vault.OpenAccountMFA(encoded, other); err == nil {
		t.Fatal("context swap accepted")
	}
	endpointContext, _ := NewGenerationTwoEndpointKeyContext(contextID)
	if _, err = vault.OpenForGenerationTwoContext(encoded, endpointContext); err == nil {
		t.Fatal("MFA ciphertext used as endpoint secret")
	}
	endpointCipher, err := vault.SealForGenerationTwoContext(seed, endpointContext)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = vault.OpenAccountMFA(endpointCipher, contextID); err == nil {
		t.Fatal("endpoint ciphertext used as MFA")
	}
	if _, err = vault.SealAccountMFA(seed[:19], contextID); err == nil {
		t.Fatal("wrong seed length accepted")
	}
}
