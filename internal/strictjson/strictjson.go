// Package strictjson provides the bounded syntax pass used by mutation
// request decoders before encoding/json applies destination-specific rules.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const (
	MaxDepth         = 32
	MaxFields        = 256
	MaxArrayElements = 10000
)

var ErrInvalid = errors.New("invalid JSON object")

// ValidateObject accepts exactly one UTF-8 JSON object, rejecting duplicate
// decoded member names at every depth and bounding structural work.
func ValidateObject(data []byte) error {
	if !utf8.Valid(data) || !validStringEscapes(data) {
		return ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	first, err := dec.Token()
	if err != nil {
		return ErrInvalid
	}
	delim, ok := first.(json.Delim)
	if !ok || delim != '{' {
		return ErrInvalid
	}
	fields := 0
	if err := walk(dec, delim, 1, &fields); err != nil {
		return ErrInvalid
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

// validStringEscapes rejects malformed JSON string escapes before
// encoding/json can replace an isolated UTF-16 surrogate with U+FFFD. Raw
// UTF-8 was validated by the caller; destination-specific control-character
// policy remains the responsibility of the semantic decoder.
func validStringEscapes(data []byte) bool {
	inString := false
	for i := 0; i < len(data); i++ {
		if !inString {
			if data[i] == '"' {
				inString = true
			}
			continue
		}
		switch data[i] {
		case '"':
			inString = false
		case '\\':
			i++
			if i >= len(data) {
				return false
			}
			switch data[i] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				continue
			case 'u':
				value, next, ok := decodeHexQuad(data, i+1)
				if !ok {
					return false
				}
				i = next - 1
				if value >= 0xdc00 && value <= 0xdfff {
					return false
				}
				if value < 0xd800 || value > 0xdbff {
					continue
				}
				if i+2 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
					return false
				}
				low, afterLow, ok := decodeHexQuad(data, i+3)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return false
				}
				i = afterLow - 1
			default:
				return false
			}
		default:
			if data[i] < 0x20 {
				return false
			}
		}
	}
	return !inString
}

func decodeHexQuad(data []byte, start int) (uint16, int, bool) {
	if start < 0 || len(data)-start < 4 {
		return 0, start, false
	}
	var value uint16
	for i := start; i < start+4; i++ {
		value <<= 4
		switch c := data[i]; {
		case c >= '0' && c <= '9':
			value |= uint16(c - '0')
		case c >= 'a' && c <= 'f':
			value |= uint16(c-'a') + 10
		case c >= 'A' && c <= 'F':
			value |= uint16(c-'A') + 10
		default:
			return 0, start, false
		}
	}
	return value, start + 4, true
}

func walk(dec *json.Decoder, opening json.Delim, depth int, fields *int) error {
	if depth > MaxDepth {
		return ErrInvalid
	}
	switch opening {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return ErrInvalid
			}
			name, ok := key.(string)
			if !ok {
				return ErrInvalid
			}
			if _, duplicate := seen[name]; duplicate {
				return ErrInvalid
			}
			seen[name] = struct{}{}
			(*fields)++
			if *fields > MaxFields || walkToken(dec, depth+1, fields) != nil {
				return ErrInvalid
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		arrayElements := 0
		for dec.More() {
			arrayElements++
			if arrayElements > MaxArrayElements {
				return ErrInvalid
			}
			if walkToken(dec, depth+1, fields) != nil {
				return ErrInvalid
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func walkToken(dec *json.Decoder, depth int, fields *int) error {
	tok, err := dec.Token()
	if err != nil {
		return ErrInvalid
	}
	if delim, ok := tok.(json.Delim); ok {
		return walk(dec, delim, depth, fields)
	}
	return nil
}
