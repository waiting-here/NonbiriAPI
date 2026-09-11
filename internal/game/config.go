package game

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

const GamesEnabledKey = "games_enabled"
const MaxMoneyMilli int64 = 9_000_000_000_000_000

func rawBool(raw map[string]string, key string, fallback bool) (bool, error) {
	value, ok := raw[key]
	if !ok {
		return fallback, nil
	}
	switch value {
	case "0":
		return false, nil
	case "1":
		return true, nil
	}
	return false, fmt.Errorf("%w: %s", ErrInvalidConfig, key)
}

func rawInt(raw map[string]string, key string, fallback, minimum, maximum int) (int, error) {
	value, ok := raw[key]
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || strconv.FormatInt(parsed, 10) != value || parsed < int64(minimum) || parsed > int64(maximum) {
		return 0, fmt.Errorf("%w: %s", ErrInvalidConfig, key)
	}
	return int(parsed), nil
}

func rawAmount(raw map[string]string, key string, fallback, minimum int64) (int64, error) {
	value, ok := raw[key]
	if !ok {
		return fallback, nil
	}
	parsed, err := credits.ParseAmount(value)
	if err != nil || parsed < minimum || parsed > MaxMoneyMilli {
		return 0, fmt.Errorf("%w: %s", ErrInvalidConfig, key)
	}
	return parsed, nil
}

func formatWireAmount(milli int64) string {
	whole, fraction := milli/1000, milli%1000
	if fraction == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return strings.TrimRight(strconv.FormatInt(whole, 10)+"."+strconv.FormatInt(fraction+1000, 10)[1:], "0")
}

// FormatAmount projects a bounded milli-credit primitive to the canonical
// display-credit wire grammar.
func FormatAmount(milli int64) string { return formatWireAmount(milli) }

func parseWireAmount(value string) (int64, error) {
	if value == "" || len(value) > 32 || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return 0, ErrInvalidConfig
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts[0]) > 1 && parts[0][0] == '0') {
		return 0, ErrInvalidConfig
	}
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return 0, ErrInvalidConfig
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) < 1 || len(fraction) > 3 {
			return 0, ErrInvalidConfig
		}
	}
	for _, c := range fraction {
		if c < '0' || c > '9' {
			return 0, ErrInvalidConfig
		}
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, ErrInvalidConfig
	}
	for len(fraction) < 3 {
		fraction += "0"
	}
	frac := int64(0)
	if fraction != "" {
		frac, _ = strconv.ParseInt(fraction, 10, 64)
	}
	if whole > (MaxMoneyMilli-frac)/1000 {
		return 0, ErrInvalidConfig
	}
	milli := whole*1000 + frac
	if formatWireAmount(milli) != value {
		return 0, ErrInvalidConfig
	}
	return milli, nil
}

// ParseAmount converts the canonical display-credit wire grammar to a
// bounded milli-credit primitive.
func ParseAmount(value string) (int64, error) { return parseWireAmount(value) }

func canonicalPositiveU128(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, ok := new(big.Int).SetString(value, 10)
	return ok && n.Sign() > 0 && n.BitLen() <= 128
}

func validateJSONObject(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSON(decoder, true); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalidConfig
	}
	return nil
}

func walkJSON(decoder *json.Decoder, requireObject bool) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok || (requireObject && delim != '{') {
		if token == nil {
			return fmt.Errorf("null")
		}
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		count := 0
		for decoder.More() {
			keyToken, _ := decoder.Token()
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return fmt.Errorf("duplicate key")
			}
			seen[key] = true
			count++
			if err := walkJSON(decoder, false); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("empty object")
		}
	case '[':
		for decoder.More() {
			if err := walkJSON(decoder, false); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("invalid JSON")
	}
	return nil
}

func ValidConfigRevision(value string) bool { return canonicalPositiveU128(value) }
