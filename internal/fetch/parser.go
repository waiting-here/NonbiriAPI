package fetch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Protocol bounds for an OpenAI-compatible GET /v1/models response. Upstream
// model identifiers are untrusted opaque strings: every bound below exists so
// a hostile or malformed upstream cannot pollute logs, the database, or the
// user DOM with over-long or malformed text. The whole response body is
// additionally capped by readModelsBody before parsing (truncation is a
// protocol failure, never a silent partial success).
const (
	// MaxModels is the largest accepted model list.
	MaxModels = 1000
	// MaxModelIDRunes bounds one upstream model identifier after trimming.
	MaxModelIDRunes = 256
	// MaxProviderRunes bounds the owned_by/provider value after trimming.
	MaxProviderRunes = 128
	// MaxModelsBodyBytes caps the response body read for parsing. It is
	// deliberately far below the egress response ceiling: a model list never
	// legitimately approaches 32 MiB, and parsing is bounded by this limit.
	MaxModelsBodyBytes = 1 << 20
)

// Model is one parsed, validated upstream model entry. Both fields are
// trimmed opaque strings with no control characters.
type Model struct {
	ID       string
	Provider string
}

var (
	// errDataMissing: the response has no usable data array.
	errDataMissing = errors.New("models data is missing or not an array")
	// errTooManyModels: the list exceeds MaxModels.
	errTooManyModels = errors.New("model list is too large")
	// errEmptyModelID: an entry has an empty id.
	errEmptyModelID = errors.New("model id is empty")
	// errModelIDTooLong: an entry id exceeds MaxModelIDRunes.
	errModelIDTooLong = errors.New("model id is too long")
	// errInvalidModelID: an id contains control characters, DEL, or the
	// U+FFFD replacement rune (evidence of coerced invalid UTF-8 upstream).
	errInvalidModelID = errors.New("model id contains invalid characters")
	// errProviderTooLong: an owned_by/provider exceeds MaxProviderRunes.
	errProviderTooLong = errors.New("model provider is too long")
	// errInvalidProvider: a provider contains control characters or U+FFFD.
	errInvalidProvider = errors.New("model provider contains invalid characters")
	// errDuplicateModelID: the list contains a duplicate id.
	errDuplicateModelID = errors.New("duplicate model id in model list")
	// errTruncatedBody: the response exceeded MaxModelsBodyBytes.
	errTruncatedBody = errors.New("models response is truncated")
	// errMalformedJSON: the body is not valid JSON.
	errMalformedJSON = errors.New("malformed models JSON")
)

// parseModels validates and extracts upstream model entries from a fully read
// response body. The caller must pass a body already capped at
// MaxModelsBodyBytes+1; parseModels rejects a body that reaches the cap, so a
// truncated response can never be mistaken for a complete one. The entire
// body must be exactly one JSON value (trailing tokens are rejected).
//
// Entries are validated strictly: id must be non-empty after ASCII trimming,
// within MaxModelIDRunes, free of C0 controls/DEL/U+FFFD, and unique across
// the list; provider must be within MaxProviderRunes and control-free
// (emptiness is allowed, matching upstreams that omit owned_by). Any
// violation rejects the whole list: a partial list is never cached.
func parseModels(body []byte) ([]Model, error) {
	if len(body) > MaxModelsBodyBytes {
		return nil, errTruncatedBody
	}
	var root struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("%w: %v", errMalformedJSON, err)
	}
	if len(root.Data) == 0 {
		return nil, errDataMissing
	}
	trimmed := bytes.TrimSpace(root.Data)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errDataMissing
	}
	var entries []struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by"`
	}
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return nil, fmt.Errorf("%w: %v", errMalformedJSON, err)
	}
	if len(entries) > MaxModels {
		return nil, errTooManyModels
	}

	seen := make(map[string]struct{}, len(entries))
	models := make([]Model, 0, len(entries))
	for _, e := range entries {
		id := strings.TrimSpace(e.ID)
		if id == "" {
			return nil, errEmptyModelID
		}
		if utf8.RuneCountInString(id) > MaxModelIDRunes {
			return nil, errModelIDTooLong
		}
		if err := validateOpaqueText(id); err != nil {
			return nil, errInvalidModelID
		}
		provider := strings.TrimSpace(e.OwnedBy)
		if utf8.RuneCountInString(provider) > MaxProviderRunes {
			return nil, errProviderTooLong
		}
		if provider != "" {
			if err := validateOpaqueText(provider); err != nil {
				return nil, errInvalidProvider
			}
		}
		if _, dup := seen[id]; dup {
			return nil, errDuplicateModelID
		}
		seen[id] = struct{}{}
		models = append(models, Model{ID: id, Provider: provider})
	}
	return models, nil
}

// validateOpaqueText rejects C0 controls, DEL, and the U+FFFD replacement
// rune. U+FFFD marks invalid UTF-8 that encoding/json coerced silently; an
// opaque identifier containing it is evidence of a malformed upstream and is
// refused rather than persisted. Other Unicode (incl. non-ASCII scripts) is
// accepted: upstream identifiers are opaque, not ASCII-only.
func validateOpaqueText(value string) error {
	for _, r := range value {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return errors.New("invalid character")
		}
	}
	return nil
}
