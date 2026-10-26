package secret

import (
	"bytes"
	"testing"
)

func TestAgentCredentialsArePurposeIsolated(t *testing.T) {
	vault, err := New(bytes.Repeat([]byte{23}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	sealed, err := vault.SealAgentKey([]byte("dedicated-secret"), "model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = vault.OpenAgentKey(sealed, "search"); err == nil {
		t.Fatal("cross-purpose credential accepted")
	}
	if _, err = vault.OpenAccountMFA(sealed, bytes.Repeat([]byte{1}, 16)); err == nil {
		t.Fatal("agent key accepted as account secret")
	}
	plain, err := vault.OpenAgentKey(sealed, "model")
	if err != nil || string(plain) != "dedicated-secret" {
		t.Fatal(err)
	}
	clear(plain)
}
