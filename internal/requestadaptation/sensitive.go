package requestadaptation

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

func cloneSensitivity(paths map[string][]string) map[string][]string {
	if len(paths) == 0 {
		return nil
	}
	out := make(map[string][]string, len(paths))
	for key, value := range paths {
		out[key] = append([]string(nil), value...)
	}
	return out
}

func editSensitivity(previous map[string][]string, edits map[string]ValueEdit) map[string][]string {
	var out map[string][]string
	for key, edit := range edits {
		if edit.Action == "clear" {
			continue
		}
		paths := previous[key]
		if edit.SensitivePaths != nil {
			paths = *edit.SensitivePaths
		}
		if len(paths) > 0 {
			if out == nil {
				out = make(map[string][]string)
			}
			out[key] = append([]string(nil), paths...)
		}
	}
	return out
}

// A selector is relative to the configured value. The empty pointer selects
// the value itself; only nonempty string leaves can be protected.
func sensitiveString(value any, path string) (string, bool) {
	if path != "" {
		parts, err := decodePointer(path)
		if err != nil {
			return "", false
		}
		for _, part := range parts {
			switch node := value.(type) {
			case map[string]any:
				value = node[part]
			case []any:
				index, err := strconv.Atoi(part)
				if err != nil || index < 0 || index >= len(node) || strconv.Itoa(index) != part {
					return "", false
				}
				value = node[index]
			default:
				return "", false
			}
		}
	}
	text, ok := value.(string)
	return text, ok && text != ""
}

func validateSensitivity(d Document) error {
	count, size := 0, 0
	add := func(value string) bool {
		count++
		size += len(value)
		return value != "" && count <= MaxSensitiveValues && size <= MaxSensitiveBytes
	}
	for name, paths := range d.FixedHeaders.SensitivePaths {
		value, exists := d.FixedHeaders.Values[name]
		if !exists || len(paths) != 1 || paths[0] != "" || !add(strings.Trim(value, " \t")) {
			return ErrInvalid
		}
	}
	for _, section := range []BodySection{d.BodyDefaults, d.BodyForced} {
		for key, paths := range section.SensitivePaths {
			value, err := decodedValue(section.Values[key])
			if err != nil || len(paths) == 0 {
				return ErrInvalid
			}
			seen := make(map[string]bool, len(paths))
			for _, path := range paths {
				text, ok := sensitiveString(value, path)
				if !ok || seen[path] || !add(text) {
					return ErrInvalid
				}
				seen[path] = true
			}
		}
	}
	return nil
}

// HeaderSecrets uses the final added headers, including HTTP whitespace
// normalization. Its returned byte slices belong to the prepared attempt.
func HeaderSecrets(headers http.Header, d Document) [][]byte {
	names := make([]string, 0, len(d.FixedHeaders.SensitivePaths))
	for name := range d.FixedHeaders.SensitivePaths {
		names = append(names, name)
	}
	sort.Strings(names)
	var out [][]byte
	for _, name := range names {
		if value := strings.Trim(headers.Get(name), " \t"); value != "" {
			out = append(out, []byte(value))
		}
	}
	return out
}

// SentSecrets excludes values discarded by protocol compilation. Unmarked
// requests do no extra JSON work. Returned slices alias the attempt policy.
func SentSecrets(candidates [][]byte, headers http.Header, body []byte) ([][]byte, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	remaining := make(map[string][]byte, len(candidates))
	for _, value := range candidates {
		if len(value) > 0 {
			remaining[string(value)] = value
		}
	}
	var out [][]byte
	match := func(value string) {
		if material, exists := remaining[value]; exists {
			out = append(out, material)
			delete(remaining, value)
		}
	}
	for _, values := range headers {
		for _, value := range values {
			match(strings.Trim(value, " \t"))
		}
	}
	if len(remaining) == 0 {
		return out, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return ErrInvalid
		}
		switch value := token.(type) {
		case string:
			match(value)
		case json.Delim:
			if value != '{' && value != '[' {
				return ErrInvalid
			}
			for decoder.More() {
				if value == '{' {
					if _, err := decoder.Token(); err != nil {
						return ErrInvalid
					}
				}
				if err := walk(); err != nil {
					return err
				}
			}
			if _, err := decoder.Token(); err != nil {
				return ErrInvalid
			}
		}
		return nil
	}
	if err := walk(); err != nil {
		return nil, err
	}
	return out, nil
}
