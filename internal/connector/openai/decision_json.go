package openai

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

func validateDecisionJSON(raw []byte) bool {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	p := boundedJSON{data: raw, dataKeys: true}
	return p.value(1)
}

// The enclosing request has already checked JSON syntax and duplicate keys.
// Count decoded UTF-8 string bytes and keep numeric tokens lossless. The raw
// data remains untouched for native forwarding.
func validDecisionJSON(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' && raw[0] != '[' {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	values, size := 0, 0
	var value func(int, bool) bool
	value = func(depth int, objectOnly bool) bool {
		values++
		if values > 10000 || size > 256<<10 {
			return false
		}
		token, err := decoder.Token()
		if err != nil || objectOnly && token != json.Delim('{') {
			return false
		}
		switch token := token.(type) {
		case json.Delim:
			if depth > 32 || token != '{' && token != '[' {
				return false
			}
			size += 2
			first := true
			for decoder.More() {
				if !first {
					size++
				}
				first = false
				if token == '{' {
					key, err := decoder.Token()
					if err != nil {
						return false
					}
					text, ok := key.(string)
					if !ok {
						return false
					}
					size += decisionJSONStringSize(text) + 1
				}
				if !value(depth+1, depth == 1 && token == '[') {
					return false
				}
			}
			if _, err := decoder.Token(); err != nil {
				return false
			}
		case string:
			size += decisionJSONStringSize(token)
		case json.Number:
			size += len(token)
		case bool:
			size += 4
			if !token {
				size++
			}
		case nil:
			size += 4
		default:
			return false
		}
		return size <= 256<<10
	}
	return value(1, false)
}

func decisionJSONStringSize(text string) int {
	size := 2
	for _, char := range text {
		switch char {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			size += 2
		default:
			if char < 0x20 {
				size += 6
			} else {
				size += utf8.RuneLen(char)
			}
		}
	}
	return size
}
