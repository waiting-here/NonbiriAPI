package secret

import (
	"bytes"
	"testing"
)

func TestChatGPTSecretsArePurposeIsolated(t *testing.T) {
	vault, err := New(bytes.Repeat([]byte{42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	sealed, err := vault.SealChatGPT([]byte("session-material"), "credentials")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = vault.OpenChatGPT(sealed, "transfer-key"); err == nil {
		t.Fatal("session accepted as transfer key")
	}
	if _, err = vault.OpenAgentKey(sealed, "model"); err == nil {
		t.Fatal("session accepted as API credential")
	}
	plain, err := vault.OpenChatGPT(sealed, "credentials")
	if err != nil || string(plain) != "session-material" {
		t.Fatal(err)
	}
	clear(plain)
	if _, err = vault.SealChatGPT([]byte("x"), "unknown"); err == nil {
		t.Fatal("unknown purpose accepted")
	}
}
