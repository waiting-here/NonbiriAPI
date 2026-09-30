package reports

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
)

const (
	maxJSONDepth  = 32
	maxJSONFields = 256
)

func jsonMediaType(value string) bool { return httpapi.JSONContentType(value) }

func readStrictJSON(writer http.ResponseWriter, request *http.Request, destination any, maxBytes int64) ([]byte, error) {
	if writer == nil {
		return nil, ErrInvalidRequest
	}
	validate := func(body []byte) error {
		if !utf8.Valid(body) || validateJSONStringLiterals(body) != nil || scanStrictJSONObject(body) != nil {
			return ErrInvalidRequest
		}
		return nil
	}
	body, err := httpapi.ReadJSON(writer, request, destination, httpapi.BodyOptions{MaxBytes: maxBytes, Validate: validate, ContentType: jsonMediaType})
	if errors.Is(err, httpapi.ErrTooLarge) {
		return nil, errPayloadTooLarge
	}
	if err != nil {
		return nil, ErrInvalidRequest
	}
	return body, nil
}

var errPayloadTooLarge = errors.New("reports: request body too large")

type jsonScanState struct{ fields int }

func scanStrictJSONObject(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	state := jsonScanState{}
	if err := scanJSONValue(decoder, &state, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalidRequest
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, state *jsonScanState, depth int) error {
	if depth >= maxJSONDepth {
		return ErrInvalidRequest
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrInvalidRequest
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return ErrInvalidRequest
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrInvalidRequest
			}
			if _, exists := seen[key]; exists {
				return ErrInvalidRequest
			}
			seen[key] = struct{}{}
			state.fields++
			if state.fields > maxJSONFields {
				return ErrInvalidRequest
			}
			if err := scanJSONValue(decoder, state, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalidRequest
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder, state, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}

func validateJSONStringLiterals(body []byte) error {
	for offset := 0; offset < len(body); {
		if body[offset] != '"' {
			offset++
			continue
		}
		decoded, next, err := decodeJSONStringAt(body, offset)
		clear(decoded)
		if err != nil {
			return err
		}
		offset = next
	}
	return nil
}

func decodeJSONString(raw []byte) ([]byte, error) {
	decoded, next, err := decodeJSONStringAt(raw, 0)
	if err != nil || next != len(raw) {
		clear(decoded)
		return nil, ErrInvalidRequest
	}
	return decoded, nil
}

func decodeJSONStringAt(raw []byte, start int) ([]byte, int, error) {
	if start < 0 || start >= len(raw) || raw[start] != '"' {
		return nil, start, ErrInvalidRequest
	}
	out := make([]byte, 0, len(raw)-start)
	for index := start + 1; index < len(raw); {
		value := raw[index]
		if value == '"' {
			if !utf8.Valid(out) {
				clear(out)
				return nil, index, ErrInvalidRequest
			}
			return out, index + 1, nil
		}
		if value < 0x20 {
			clear(out)
			return nil, index, ErrInvalidRequest
		}
		if value != '\\' {
			out = append(out, value)
			index++
			continue
		}
		index++
		if index >= len(raw) {
			clear(out)
			return nil, index, ErrInvalidRequest
		}
		switch raw[index] {
		case '"', '\\', '/':
			out = append(out, raw[index])
			index++
		case 'b':
			out = append(out, '\b')
			index++
		case 'f':
			out = append(out, '\f')
			index++
		case 'n':
			out = append(out, '\n')
			index++
		case 'r':
			out = append(out, '\r')
			index++
		case 't':
			out = append(out, '\t')
			index++
		case 'u':
			first, next, ok := readHexRune(raw, index+1)
			if !ok {
				clear(out)
				return nil, index, ErrInvalidRequest
			}
			index = next
			decodedRune := rune(first)
			if utf16.IsSurrogate(decodedRune) {
				if decodedRune < 0xD800 || decodedRune > 0xDBFF || index+2 > len(raw) ||
					raw[index] != '\\' || raw[index+1] != 'u' {
					clear(out)
					return nil, index, ErrInvalidRequest
				}
				second, after, ok := readHexRune(raw, index+2)
				if !ok || second < 0xDC00 || second > 0xDFFF {
					clear(out)
					return nil, index, ErrInvalidRequest
				}
				decodedRune = utf16.DecodeRune(decodedRune, rune(second))
				index = after
			}
			var encoded [utf8.UTFMax]byte
			size := utf8.EncodeRune(encoded[:], decodedRune)
			out = append(out, encoded[:size]...)
		default:
			clear(out)
			return nil, index, ErrInvalidRequest
		}
	}
	clear(out)
	return nil, len(raw), ErrInvalidRequest
}

func readHexRune(raw []byte, start int) (uint16, int, bool) {
	if start < 0 || len(raw)-start < 4 {
		return 0, start, false
	}
	var value uint16
	for index := start; index < start+4; index++ {
		value <<= 4
		switch current := raw[index]; {
		case current >= '0' && current <= '9':
			value |= uint16(current - '0')
		case current >= 'a' && current <= 'f':
			value |= uint16(current-'a') + 10
		case current >= 'A' && current <= 'F':
			value |= uint16(current-'A') + 10
		default:
			return 0, index, false
		}
	}
	return value, start + 4, true
}
