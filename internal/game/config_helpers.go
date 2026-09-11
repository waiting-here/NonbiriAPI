package game

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func RawBool(raw map[string]string, key string, fallback bool) (bool, error) {
	return rawBool(raw, key, fallback)
}
func RawInt(raw map[string]string, key string, fallback, minimum, maximum int) (int, error) {
	return rawInt(raw, key, fallback, minimum, maximum)
}
func RawAmount(raw map[string]string, key string, fallback, minimum int64) (int64, error) {
	return rawAmount(raw, key, fallback, minimum)
}

// DecodeConfigPatch preserves duplicate-key, null, empty-object and unknown
// field rejection before a module merges any leaf into a fresh projection.
func DecodeConfigPatch(data []byte, destination any) error {
	if len(data) == 0 || len(data) > 65536 {
		return ErrInvalidConfig
	}
	if err := validateJSONObject(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrInvalidConfig
	}
	return nil
}

func BoolRaw(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func ConfigJSON(value any) json.RawMessage {
	body, err := json.Marshal(value)
	if err != nil {
		panic("game: invalid compiled configuration projection")
	}
	return body
}

// MergeConfigFields copies only existing leaves. The caller first decodes
// the patch into its writable DTO, so read-only fields cannot enter here.
func MergeConfigFields(current, patch json.RawMessage, destination any) error {
	var base, delta map[string]json.RawMessage
	if json.Unmarshal(current, &base) != nil || json.Unmarshal(patch, &delta) != nil || len(delta) == 0 {
		return ErrInvalidConfig
	}
	for key, next := range delta {
		next = bytes.TrimSpace(next)
		previous, exists := base[key]
		if !exists {
			return ErrInvalidConfig
		}
		if len(next) > 0 && next[0] == '{' {
			var merged map[string]json.RawMessage
			if err := MergeConfigFields(previous, next, &merged); err != nil {
				return err
			}
			base[key] = ConfigJSON(merged)
		} else {
			base[key] = append(json.RawMessage(nil), next...)
		}
	}
	return json.Unmarshal(ConfigJSON(base), destination)
}
