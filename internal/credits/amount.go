// Package credits holds request-local economy primitives: canonical int64
// milli-credit inputs, checked pricing arithmetic, and the reservation state
// model. Persistent wallet, pool and aggregate accounting uses the fixed-width
// internal/ledger package and must never narrow those values back to int64.
//
// Money rules (implementation contract §1.2/§5.1):
//
//   - each external pricing primitive is an integer number of milli-credits
//     (milli = 1/1000 of a display credit) bounded to int64; there is no float
//     anywhere in accounting, while account/ledger aggregates are SM128;
//   - wire values are canonical decimal strings: optional "-" for the values
//     that may be negative, digits only, no exponent, no "+", no leading
//     zeros, no whitespace, no "-0";
//   - intermediate computation uses math/big.Int (or provably equivalent
//     checked arithmetic); a result that does not fit int64 fails closed;
//   - token pricing sums the four buckets against their unit prices first and
//     divides by one million exactly once, rounding up to whole milli-credits;
//     buckets are never rounded individually.
package credits

import (
	"errors"
	"math"
	"math/big"
	"strconv"
)

var (
	// ErrInvalidAmount rejects a non-canonical or out-of-domain input
	// (malformed string, negative value where only non-negative is legal,
	// percent outside 0..100).
	ErrInvalidAmount = errors.New("credits: invalid amount")
	// ErrOverflow reports that an arithmetic result cannot be represented as
	// int64. Accounting always fails closed on overflow instead of wrapping.
	ErrOverflow = errors.New("credits: integer overflow")
)

// maxAmountDigits is the maximum character count of any canonical int64
// decimal string ("-9223372036854775808" is 20 characters). Longer inputs are
// rejected before parsing so a hostile string can never force an expensive
// scan.
const maxAmountDigits = 20

// ParseAmount parses a canonical decimal string into an int64 amount of
// milli-credits. The grammar is deliberately strict: optional single leading
// '-' followed by one or more ASCII digits; no '+', exponent, whitespace,
// fractional part, leading zeros, or "-0". Values beyond the int64 range are
// rejected (fail closed) rather than clamped.
func ParseAmount(s string) (int64, error) {
	if s == "" || len(s) > maxAmountDigits {
		return 0, ErrInvalidAmount
	}
	negative := false
	body := s
	if s[0] == '-' {
		negative = true
		body = s[1:]
		if body == "" {
			return 0, ErrInvalidAmount
		}
	}
	if len(body) > 1 && body[0] == '0' {
		return 0, ErrInvalidAmount // leading zeros ("01", "00") are not canonical
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return 0, ErrInvalidAmount
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, ErrInvalidAmount // out of int64 range
	}
	if negative && v == 0 {
		return 0, ErrInvalidAmount // "-0" is not canonical
	}
	return v, nil
}

// FormatAmount renders an int64 milli-credit amount as its canonical decimal
// string ("0", "7", "-7"). It never emits "+", exponents, or "-0".
func FormatAmount(v int64) string {
	return strconv.FormatInt(v, 10)
}

// Add returns a+b, failing closed when the result leaves the int64 range.
func Add(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, ErrOverflow
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// Sub returns a-b, failing closed when the result leaves the int64 range.
func Sub(a, b int64) (int64, error) {
	if b < 0 && a > math.MaxInt64+b {
		return 0, ErrOverflow
	}
	if b > 0 && a < math.MinInt64+b {
		return 0, ErrOverflow
	}
	return a - b, nil
}

// Mul returns a*b using exact big-integer multiplication and failing closed
// unless the product fits int64.
func Mul(a, b int64) (int64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}
	p := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	if !p.IsInt64() {
		return 0, ErrOverflow
	}
	return p.Int64(), nil
}

// millionMilli is the pricing divisor: unit prices are denominated per
// million tokens.
var millionMilli = big.NewInt(1_000_000)

// hundred is the discount-percentage divisor.
var hundred = big.NewInt(100)

// ceilDivBig returns ceil(a/b) for b>0 without float involvement.
func ceilDivBig(a, b *big.Int) (*big.Int, error) {
	q, r := new(big.Int).QuoRem(a, b, new(big.Int))
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q, nil
}

// bigToInt64 converts an exact big-integer result to int64, failing closed
// when it does not fit.
func bigToInt64(v *big.Int) (int64, error) {
	if !v.IsInt64() {
		return 0, ErrOverflow
	}
	return v.Int64(), nil
}
