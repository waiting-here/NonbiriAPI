package activities

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	milliPerPoint      int64 = 1000
	maxPointsWireBytes       = 32
)

// parsePointsMilli accepts the non-negative points wire grammar and converts
// it exactly to the bounded raw milli-credit primitive used by activity
// storage and ledger plans. Activity amount inputs are never signed.
func parsePointsMilli(raw string) (int64, bool) {
	if raw == "" || len(raw) > maxPointsWireBytes {
		return 0, false
	}
	wholeText, fractionText := raw, ""
	if dot := strings.IndexByte(raw, '.'); dot >= 0 {
		if strings.IndexByte(raw[dot+1:], '.') >= 0 {
			return 0, false
		}
		wholeText, fractionText = raw[:dot], raw[dot+1:]
		if len(fractionText) < 1 || len(fractionText) > 3 {
			return 0, false
		}
	}
	if wholeText == "" || len(wholeText) > 1 && wholeText[0] == '0' {
		return 0, false
	}
	for index := range wholeText {
		if wholeText[index] < '0' || wholeText[index] > '9' {
			return 0, false
		}
	}
	for index := range fractionText {
		if fractionText[index] < '0' || fractionText[index] > '9' {
			return 0, false
		}
	}
	whole, err := strconv.ParseInt(wholeText, 10, 64)
	if err != nil || whole < 0 {
		return 0, false
	}
	fraction := int64(0)
	for index := range fractionText {
		fraction = fraction*10 + int64(fractionText[index]-'0')
	}
	for digits := len(fractionText); digits < 3; digits++ {
		fraction *= 10
	}
	if whole > (db.MaxMoneyMilli-fraction)/milliPerPoint {
		return 0, false
	}
	return whole*milliPerPoint + fraction, true
}

// formatMilliPoints renders any signed aggregate milli-credit value without
// narrowing it to int64 or using floating point.
func formatMilliPoints(value *big.Int) string {
	if value == nil || value.Sign() == 0 {
		return "0"
	}
	negative := value.Sign() < 0
	abs := new(big.Int).Abs(new(big.Int).Set(value))
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(abs, big.NewInt(milliPerPoint), fraction)
	formatted := whole.String()
	if fraction.Sign() != 0 {
		fractionText := strconv.FormatInt(fraction.Int64()+milliPerPoint, 10)[1:]
		formatted += "." + strings.TrimRight(fractionText, "0")
	}
	if negative {
		return "-" + formatted
	}
	return formatted
}

func formatMilliPointsInt64(value int64) string {
	return formatMilliPoints(big.NewInt(value))
}
