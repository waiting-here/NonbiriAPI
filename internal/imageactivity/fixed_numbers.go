package imageactivity

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

// Keep exact decimal validation bounded before constructing a rational number.
// The ordinary scalar parser still supplies the finite wire representation.
func fixedDecimal(raw json.RawMessage) (*big.Rat, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 256 || !json.Valid(raw) {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, ErrInvalid
	}
	number, ok := value.(json.Number)
	if !ok {
		return nil, ErrInvalid
	}
	text := number.String()
	if offset := strings.IndexAny(text, "eE"); offset >= 0 {
		exponent, err := strconv.ParseInt(text[offset+1:], 10, 32)
		if err != nil || exponent < -512 || exponent > 512 {
			return nil, ErrInvalid
		}
	}
	valueRat, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, ErrInvalid
	}
	return valueRat, nil
}

func fixedFloatDecimal(value float64) *big.Rat {
	rational, _ := new(big.Rat).SetString(strconv.FormatFloat(value, 'f', -1, 64))
	return rational
}

func fixedCanonicalNumber(raw json.RawMessage) (float64, error) {
	exact, err := fixedDecimal(raw)
	if err != nil {
		return 0, err
	}
	value, err := scalar(raw)
	number, ok := value.(float64)
	if err != nil || !ok || exact.Cmp(fixedFloatDecimal(number)) != 0 {
		return 0, ErrInvalid
	}
	return number, nil
}

func validateFixedNumbers(input SubmitInput, rules []ParameterRule) error {
	values := map[ParameterKey]json.RawMessage{Seed: input.Seed, Steps: input.Steps, Guidance: input.Guidance}
	for _, rule := range rules {
		raw := values[rule.Key]
		if len(raw) == 0 || rule.Type != "integer" && rule.Type != "number" {
			continue
		}
		value, err := fixedDecimal(raw)
		if err != nil || rule.Type == "integer" && !value.IsInt() {
			return ErrInvalid
		}
		if rule.Minimum != nil && value.Cmp(fixedFloatDecimal(*rule.Minimum)) < 0 || rule.Maximum != nil && value.Cmp(fixedFloatDecimal(*rule.Maximum)) > 0 {
			return ErrInvalid
		}
		if rule.Step != nil {
			base := new(big.Rat)
			if rule.Minimum != nil {
				base = fixedFloatDecimal(*rule.Minimum)
			}
			difference := new(big.Rat).Sub(value, base)
			if !difference.Quo(difference, fixedFloatDecimal(*rule.Step)).IsInt() {
				return ErrInvalid
			}
		}
		if len(rule.Enum) > 0 {
			found := false
			for _, raw := range rule.Enum {
				candidate, err := fixedDecimal(raw)
				if err == nil && candidate.Cmp(value) == 0 {
					found = true
					break
				}
			}
			if !found {
				return ErrInvalid
			}
		}
	}
	return nil
}
