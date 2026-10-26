package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
)

// adminPasswordVerifier compares the configured password without retaining an
// unkeyed password digest. Its random key and MAC live only in this runtime;
// neither is persisted or used for the stable session credential generation.
// This is not a password-storage format: persisted passwords require a KDF.
type adminPasswordVerifier struct {
	key [32]byte
	mac [sha256.Size]byte
}

func newAdminPasswordVerifier(password string) adminPasswordVerifier {
	var verifier adminPasswordVerifier
	_, _ = rand.Read(verifier.key[:])
	verifier.mac = verifier.authenticate(password)
	return verifier
}

func (v *adminPasswordVerifier) authenticate(value string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, v.key[:])
	_, _ = mac.Write([]byte(value))
	var result [sha256.Size]byte
	mac.Sum(result[:0])
	return result
}

func (v *adminPasswordVerifier) matches(value string) bool {
	if !validateBoundedText(value, maxPasswordBytes, false) {
		return false
	}
	mac := v.authenticate(value)
	return hmac.Equal(mac[:], v.mac[:])
}
