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
	MaxDepth  = 64
	MaxFields = 1024
)

var ErrInvalid = errors.New("invalid JSON object")

// ValidateObject accepts exactly one UTF-8 JSON object, rejecting duplicate
// decoded member names at every depth and bounding structural work.
func ValidateObject(data []byte) error {
	if !utf8.Valid(data) {
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
		for dec.More() {
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
