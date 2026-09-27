package requestadaptation

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

func decodeBody(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || strictjson.ValidateObjectWithFieldLimit(raw, 16384) != nil {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil || value == nil {
		return nil, ErrInvalid
	}
	return value, nil
}

func encodeBody(value map[string]any, maxBytes int64) ([]byte, error) {
	if maxBytes < 1 {
		return nil, ErrInvalid
	}
	encoded, err := json.Marshal(value)
	if err != nil || int64(len(encoded)) > maxBytes || strictjson.ValidateObjectWithFieldLimit(encoded, 16384) != nil {
		clear(encoded)
		return nil, ErrInvalid
	}
	return encoded, nil
}

func pointerValue(root map[string]any, parts []string) (any, bool, error) {
	current := root
	for index, part := range parts {
		value, present := current[part]
		if !present {
			return nil, false, nil
		}
		if index == len(parts)-1 {
			return value, true, nil
		}
		next, ok := value.(map[string]any)
		if !ok || next == nil {
			return nil, false, ErrConflict
		}
		current = next
	}
	return nil, false, ErrInvalid
}

func putPointer(root map[string]any, parts []string, value any, onlyMissing bool) error {
	current := root
	for index, part := range parts {
		if index == len(parts)-1 {
			if _, present := current[part]; !present || !onlyMissing {
				current[part] = value
			}
			return nil
		}
		child, present := current[part]
		if !present {
			next := make(map[string]any)
			current[part] = next
			current = next
			continue
		}
		next, ok := child.(map[string]any)
		if !ok || next == nil {
			return ErrConflict
		}
		current = next
	}
	return ErrInvalid
}

func decodedValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, ErrInvalid
	}
	return value, nil
}

// ApplyBody mutates a fresh logical JSON copy. Null is an existing value;
// absent parents may be created, but scalar, null, and array parents conflict.
func ApplyBody(source []byte, d Document, maxBytes int64) ([]byte, error) {
	if len(d.BodyDefaults.Values) == 0 && len(d.BodyForced.Values) == 0 {
		return append([]byte(nil), source...), nil
	}
	root, err := decodeBody(source)
	if err != nil {
		return nil, err
	}
	for _, section := range []struct {
		values      map[string]json.RawMessage
		onlyMissing bool
	}{{d.BodyDefaults.Values, true}, {d.BodyForced.Values, false}} {
		for path, raw := range section.values {
			parts, err := ParsePointer(path)
			if err != nil {
				return nil, err
			}
			value, err := decodedValue(raw)
			if err != nil {
				return nil, err
			}
			if err := putPointer(root, parts, value, section.onlyMissing); err != nil {
				return nil, err
			}
		}
	}
	encoded, err := encodeBody(root, maxBytes)
	if err != nil {
		return nil, err
	}
	// Only ordinary sampling fields are interpreted here. Connector-specific
	// capability checks and server authority still run on the final copy.
	var fields map[string]json.RawMessage
	if json.Unmarshal(encoded, &fields) != nil || ValidateSamplerFields(fields) != nil {
		clear(encoded)
		return nil, ErrInvalid
	}
	return encoded, nil
}

// ExcludedConflict rejects a configured body path whose top-level parent is
// excluded from this effective model+binding. It is also used at save time.
func ExcludedConflict(d Document, excluded []string) bool {
	paths := make([]string, 0, len(d.BodyDefaults.Values)+len(d.BodyForced.Values)+len(d.NativeExtensionPaths.Values))
	for path := range d.BodyDefaults.Values {
		paths = append(paths, path)
	}
	for path := range d.BodyForced.Values {
		paths = append(paths, path)
	}
	paths = append(paths, d.NativeExtensionPaths.Values...)
	for _, path := range paths {
		parts, err := ParsePointer(path)
		if err != nil {
			return true
		}
		for _, name := range excluded {
			if parts[0] == name {
				return true
			}
		}
	}
	return false
}

// ExtractNative lifts only explicitly declared client extensions out of the
// logical OpenAI request. Their top-level roots are omitted from converter
// input; undeclared siblings do not accidentally become native fields.
func ExtractNative(source []byte, paths []string, maxBytes int64) ([]byte, map[string]json.RawMessage, error) {
	if len(paths) == 0 {
		return append([]byte(nil), source...), map[string]json.RawMessage{}, nil
	}
	root, err := decodeBody(source)
	if err != nil {
		return nil, nil, err
	}
	out := make(map[string]json.RawMessage, len(paths))
	for _, path := range paths {
		parts, err := ParsePointer(path)
		if err != nil {
			return nil, nil, err
		}
		value, present, err := pointerValue(root, parts)
		if err != nil {
			return nil, nil, err
		}
		if !present {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, nil, ErrInvalid
		}
		out[path] = encoded
	}
	for _, path := range paths {
		parts, _ := ParsePointer(path)
		delete(root, parts[0])
	}
	filtered, err := encodeBody(root, maxBytes)
	if err != nil {
		for _, value := range out {
			clear(value)
		}
		return nil, nil, err
	}
	return filtered, out, nil
}

// MergeNative inserts declared values after protocol conversion. Protected
// server fields are rechecked by ValidateDocument before this function runs.
func MergeNative(compiled []byte, native map[string]json.RawMessage, maxBytes int64) ([]byte, error) {
	if len(native) == 0 {
		return append([]byte(nil), compiled...), nil
	}
	root, err := decodeBody(compiled)
	if err != nil {
		return nil, err
	}
	for path, raw := range native {
		parts, err := ParsePointer(path)
		if err != nil {
			return nil, err
		}
		value, err := decodedValue(raw)
		if err != nil {
			return nil, err
		}
		if err := putPointer(root, parts, value, false); err != nil {
			return nil, err
		}
	}
	return encodeBody(root, maxBytes)
}

func rootOf(path string) string {
	parts, err := ParsePointer(path)
	if err != nil || len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func nativeRootAllowed(path string) bool {
	root := rootOf(path)
	if root == "" {
		return false
	}
	switch root {
	case "max_tokens", "max_completion_tokens", "temperature", "top_p", "top_k", "presence_penalty", "frequency_penalty", "seed", "stop", "n", "logit_bias", "logprobs", "response_format":
		return false
	}
	return !strings.HasPrefix(root, "_")
}
