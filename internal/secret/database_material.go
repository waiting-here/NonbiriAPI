package secret

import "crypto/sha256"

// CreateDatabaseMaterial creates the authenticated singleton used both for
// credential review identity and the constant-cost startup key check.
func (v *Vault) CreateDatabaseMaterial() ([]byte, error) {
	key, err := v.DeriveGenerationTwoSubkey([]byte(reviewPurpose))
	if err != nil {
		return nil, ErrReviewMaterial
	}
	defer clear(key)
	encoded, err := v.seal(key, []byte(reviewAAD))
	if err != nil {
		return nil, ErrReviewMaterial
	}
	return []byte(encoded), nil
}

func (v *Vault) ValidateDatabaseMaterial(envelope []byte) error {
	key, err := v.open(string(envelope), []byte(reviewAAD))
	defer clear(key)
	if err != nil || len(key) != sha256.Size {
		return ErrReviewMaterial
	}
	return nil
}
