package fishing

import (
	"crypto/rand"
	"errors"
	"io"
	"math/big"
)

var (
	// ErrInvalidBound reports an empty random sample space.
	ErrInvalidBound = errors.New("fishing: random bound must be positive")
	// ErrInvalidRandomValue reports a broken custom source that returned a
	// value outside the requested half-open interval.
	ErrInvalidRandomValue = errors.New("fishing: random source returned an out-of-range value")
)

// IntSource supplies an unbiased value in [0, upperExclusive). Production
// callers use CryptoSource; the small interface lets tests replay an exact
// sequence without weakening the production source.
type IntSource interface {
	Uint64n(upperExclusive uint64) (uint64, error)
}

// CryptoSource draws with crypto/rand.Int. Reader is injectable for
// deterministic tests; a nil Reader uses crypto/rand.Reader.
type CryptoSource struct {
	Reader io.Reader
}

// Uint64n returns an unbiased integer in [0, upperExclusive). It deliberately
// does not reduce random words modulo the bound.
func (source CryptoSource) Uint64n(upperExclusive uint64) (uint64, error) {
	if upperExclusive == 0 {
		return 0, ErrInvalidBound
	}
	reader := source.Reader
	if reader == nil {
		reader = rand.Reader
	}
	value, err := rand.Int(reader, new(big.Int).SetUint64(upperExclusive))
	if err != nil {
		return 0, err
	}
	return value.Uint64(), nil
}

func draw(source IntSource, upperExclusive uint64) (uint64, error) {
	if source == nil {
		return 0, errors.New("fishing: nil random source")
	}
	if upperExclusive == 0 {
		return 0, ErrInvalidBound
	}
	value, err := source.Uint64n(upperExclusive)
	if err != nil {
		return 0, err
	}
	if value >= upperExclusive {
		return 0, ErrInvalidRandomValue
	}
	return value, nil
}
