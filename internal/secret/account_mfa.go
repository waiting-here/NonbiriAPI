package secret

// AccountMFACodec provides purpose-isolated encryption for authenticator seeds.
type AccountMFACodec interface {
	SealAccountMFA([]byte, []byte) (string, error)
	OpenAccountMFA(string, []byte) ([]byte, error)
}

func mfaAAD(contextID []byte) ([]byte, error) {
	if len(contextID) != 16 {
		return nil, ErrInvalidContext
	}
	return append([]byte("NonbiriAPI/generation/2/account-totp/v1:"), contextID...), nil
}
func (v *Vault) SealAccountMFA(seed, contextID []byte) (string, error) {
	if len(seed) != 20 {
		return "", ErrInvalidPlaintext
	}
	aad, err := mfaAAD(contextID)
	if err != nil {
		return "", err
	}
	defer clear(aad)
	return v.seal(seed, aad)
}
func (v *Vault) OpenAccountMFA(encoded string, contextID []byte) ([]byte, error) {
	aad, err := mfaAAD(contextID)
	if err != nil {
		return nil, ErrInvalidCiphertext
	}
	defer clear(aad)
	seed, err := v.open(encoded, aad)
	if err != nil || len(seed) != 20 {
		clear(seed)
		return nil, ErrInvalidCiphertext
	}
	return seed, nil
}
