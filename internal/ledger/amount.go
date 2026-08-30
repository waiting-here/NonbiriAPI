// Package ledger owns the Generation 2 wide-credit ledger and capacity
// primitives. All writes are transaction-local; callers retain ownership of
// the outer transaction and must commit domain state and ledger effects
// together.
package ledger

import (
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Amount is a canonical signed milli-credit value whose absolute value is
// strictly less than 2^127. Its representation is private so callers cannot
// construct negative zero or set the reserved high bit.
type Amount struct {
	value db.SM128
}

// AmountFromBig copies value into the fixed-width signed ledger domain.
func AmountFromBig(value *big.Int) (Amount, error) {
	encoded, err := db.SM128FromBig(value)
	if err != nil {
		return Amount{}, ErrInvalidAmount
	}
	return Amount{value: encoded}, nil
}

// AmountFromMilli promotes an external int64 primitive without narrowing any
// stored ledger value back to int64.
func AmountFromMilli(value int64) Amount {
	// Every int64 has at most 63 magnitude bits, strictly inside SM128's
	// 127-bit magnitude domain.
	encoded, _ := db.SM128FromBig(big.NewInt(value))
	return Amount{value: encoded}
}

// ParseAmount parses one canonical decimal string into the signed ledger
// domain. Leading zeros, plus signs, whitespace, exponents and negative zero
// are rejected by the shared Generation 2 codec.
func ParseAmount(value string) (Amount, error) {
	encoded, err := db.ParseSM128Decimal(value)
	if err != nil {
		return Amount{}, ErrInvalidAmount
	}
	return Amount{value: encoded}, nil
}

// Big returns an independent big.Int copy suitable for checked arithmetic.
func (a Amount) Big() *big.Int { return a.value.Big() }

// Decimal returns the canonical base-10 wire representation.
func (a Amount) Decimal() string { return a.value.Decimal() }

// Sign reports -1, 0 or +1.
func (a Amount) Sign() int { return int(a.value.Sign) }

// IsZero reports whether the amount is canonical zero.
func (a Amount) IsZero() bool { return a.value.Sign == 0 }

func amountFromScalar(value db.SM128) (Amount, error) {
	if _, err := db.NewSM128(int(value.Sign), value.Mag[:]); err != nil {
		return Amount{}, ErrInvariant
	}
	return Amount{value: value}, nil
}

func amountFromParts(sign int, magnitude []byte) (Amount, error) {
	value, err := db.NewSM128(sign, magnitude)
	if err != nil {
		return Amount{}, ErrInvariant
	}
	return Amount{value: value}, nil
}

func negate(value Amount) Amount {
	if value.value.Sign == 0 {
		return value
	}
	value.value.Sign = -value.value.Sign
	return value
}

func addAmounts(values ...Amount) (Amount, error) {
	total := new(big.Int)
	for _, value := range values {
		total.Add(total, value.Big())
	}
	return AmountFromBig(total)
}

func subtractAmounts(left, right Amount) (Amount, error) {
	return AmountFromBig(new(big.Int).Sub(left.Big(), right.Big()))
}

func nonnegative(value Amount) bool { return value.Sign() >= 0 }

func positive(value Amount) bool { return value.Sign() > 0 }
