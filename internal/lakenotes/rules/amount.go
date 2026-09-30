package rules

import (
	"encoding/json"
	"errors"
	"math/big"
)

var ErrInvalid = errors.New("invalid lake notes state or action")
var ErrFunds = errors.New("insufficient lake notes coins")
var ErrOverflow = errors.New("lake notes integer overflow")
var ErrBusy = errors.New("a fishing cast is in progress")

// Amount is a canonical decimal unsigned 128-bit integer. Its zero value is invalid.
type Amount string

func NewAmount(n uint64) Amount { return Amount(new(big.Int).SetUint64(n).String()) }
func ParseAmount(s string) (Amount, error) {
	if s == "" || len(s) > 39 || (len(s) > 1 && s[0] == '0') {
		return "", ErrInvalid
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return "", ErrInvalid
		}
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.BitLen() > 128 {
		return "", ErrOverflow
	}
	return Amount(s), nil
}
func (a Amount) value() *big.Int {
	n, _ := new(big.Int).SetString(string(a), 10)
	if n == nil {
		panic("invalid amount")
	}
	return n
}
func (a Amount) Cmp(b Amount) int { return a.value().Cmp(b.value()) }
func (a Amount) Add(b Amount) (Amount, error) {
	n := new(big.Int).Add(a.value(), b.value())
	if n.BitLen() > 128 {
		return "", ErrOverflow
	}
	return Amount(n.String()), nil
}
func (a Amount) Sub(b Amount) (Amount, error) {
	n := new(big.Int).Sub(a.value(), b.value())
	if n.Sign() < 0 {
		return "", ErrFunds
	}
	return Amount(n.String()), nil
}
func (a Amount) MarshalJSON() ([]byte, error) {
	if _, e := ParseAmount(string(a)); e != nil {
		return nil, e
	}
	return json.Marshal(string(a))
}
func (a *Amount) UnmarshalJSON(b []byte) error {
	var s string
	if e := json.Unmarshal(b, &s); e != nil {
		return e
	}
	v, e := ParseAmount(s)
	if e == nil {
		*a = v
	}
	return e
}
func addSmall(a *Amount, n int) error {
	if n < 0 {
		return ErrInvalid
	}
	v, e := a.Add(NewAmount(uint64(n)))
	if e == nil {
		*a = v
	}
	return e
}
func increment(a *Amount) error { return addSmall(a, 1) }
