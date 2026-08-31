package activities

import (
	"math/big"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func validReason(value string) bool {
	return validBoundedText(value, 1, 1024, 4096)
}

func validLiterature(value string) bool {
	return validBoundedText(value, 0, 1024, 4096)
}

func validBoundedText(value string, minimumRunes, maximumRunes, maximumBytes int) bool {
	if !utf8.ValidString(value) || len(value) > maximumBytes {
		return false
	}
	runes := utf8.RuneCountInString(value)
	if runes < minimumRunes || runes > maximumRunes {
		return false
	}
	for _, character := range value {
		if character <= 0x1f || character >= 0x7f && character <= 0x9f {
			return false
		}
	}
	return true
}

func decodeU128(raw []byte) (db.U128, error) {
	value, err := db.DecodeU128(raw)
	if err != nil {
		return db.U128{}, ErrInvariant
	}
	return value, nil
}

func u128FromBig(value *big.Int) (db.U128, error) {
	result, err := db.U128FromBig(value)
	if err != nil {
		return db.U128{}, ErrInvariant
	}
	return result, nil
}

func addU128(left, right db.U128) (db.U128, error) {
	return u128FromBig(new(big.Int).Add(left.Big(), right.Big()))
}

func oneU128() db.U128 {
	value, _ := db.U128FromBig(big.NewInt(1))
	return value
}
