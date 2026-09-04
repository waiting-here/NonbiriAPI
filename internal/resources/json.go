package resources

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

var errInvalidResourceJSON = errors.New("resources: invalid JSON object")

// validateResourceJSONObject applies the generation-two JSON limits to one
// object document. The field bound is per object, so arrays at a frozen batch
// limit may contain multiple small objects without consuming one shared field
// counter.
func validateResourceJSONObject(data []byte) error {
	if !utf8.Valid(data) || !validResourceStringEscapes(data) {
		return errInvalidResourceJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return errInvalidResourceJSON
	}
	delim, ok := first.(json.Delim)
	if !ok || delim != '{' || walkResourceJSON(decoder, delim, 1) != nil {
		return errInvalidResourceJSON
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errInvalidResourceJSON
	}
	return nil
}

func walkResourceJSON(decoder *json.Decoder, opening json.Delim, depth int) error {
	if depth > strictjson.MaxDepth {
		return errInvalidResourceJSON
	}
	switch opening {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return errInvalidResourceJSON
			}
			name, ok := key.(string)
			if !ok {
				return errInvalidResourceJSON
			}
			if _, duplicate := seen[name]; duplicate || len(seen) >= strictjson.MaxFields {
				return errInvalidResourceJSON
			}
			seen[name] = struct{}{}
			if walkResourceJSONToken(decoder, depth+1) != nil {
				return errInvalidResourceJSON
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errInvalidResourceJSON
		}
	case '[':
		elements := 0
		for decoder.More() {
			elements++
			if elements > strictjson.MaxArrayElements || walkResourceJSONToken(decoder, depth+1) != nil {
				return errInvalidResourceJSON
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errInvalidResourceJSON
		}
	default:
		return errInvalidResourceJSON
	}
	return nil
}

func walkResourceJSONToken(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return errInvalidResourceJSON
	}
	if delim, ok := token.(json.Delim); ok {
		return walkResourceJSON(decoder, delim, depth)
	}
	return nil
}

// encoding/json replaces isolated UTF-16 surrogates with U+FFFD, so reject
// malformed escapes before decoding instead of accepting a changed value.
func validResourceStringEscapes(data []byte) bool {
	inString := false
	for index := 0; index < len(data); index++ {
		if !inString {
			if data[index] == '"' {
				inString = true
			}
			continue
		}
		switch data[index] {
		case '"':
			inString = false
		case '\\':
			index++
			if index >= len(data) {
				return false
			}
			switch data[index] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				continue
			case 'u':
				value, next, ok := decodeResourceHexQuad(data, index+1)
				if !ok {
					return false
				}
				index = next - 1
				if value >= 0xdc00 && value <= 0xdfff {
					return false
				}
				if value < 0xd800 || value > 0xdbff {
					continue
				}
				if index+2 >= len(data) || data[index+1] != '\\' || data[index+2] != 'u' {
					return false
				}
				low, afterLow, ok := decodeResourceHexQuad(data, index+3)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return false
				}
				index = afterLow - 1
			default:
				return false
			}
		default:
			if data[index] < 0x20 {
				return false
			}
		}
	}
	return !inString
}

func decodeResourceHexQuad(data []byte, start int) (uint16, int, bool) {
	if start < 0 || len(data)-start < 4 {
		return 0, start, false
	}
	var value uint16
	for index := start; index < start+4; index++ {
		value <<= 4
		switch character := data[index]; {
		case character >= '0' && character <= '9':
			value |= uint16(character - '0')
		case character >= 'a' && character <= 'f':
			value |= uint16(character-'a') + 10
		case character >= 'A' && character <= 'F':
			value |= uint16(character-'A') + 10
		default:
			return 0, start, false
		}
	}
	return value, start + 4, true
}
