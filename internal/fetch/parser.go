package fetch

import (
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

// Protocol bounds for an OpenAI-compatible GET /v1/models response. Upstream
// model identifiers are untrusted opaque strings: every bound below exists so
// a hostile or malformed upstream cannot pollute logs, the database, or the
// user DOM with over-long or malformed text. The whole response body is
// additionally capped by readModelsBody before parsing (truncation is a
// protocol failure, never a silent partial success).
const (
	// MaxModels is the largest accepted model list.
	MaxModels = openai.MaxDiscoveredModels
	// MaxModelIDRunes bounds one upstream model identifier after trimming.
	MaxModelIDRunes = openai.MaxDiscoveredModelIDRunes
	// MaxProviderRunes bounds the owned_by/provider value after trimming.
	MaxProviderRunes = openai.MaxDiscoveredProviderRunes
	// MaxModelsBodyBytes caps the response body read for parsing. It is
	// deliberately far below the egress response ceiling: a model list never
	// legitimately approaches 32 MiB, and parsing is bounded by this limit.
	MaxModelsBodyBytes = openai.MaxModelsBodyBytes
)

// Model is one parsed, validated upstream model entry. Both fields are
// trimmed opaque strings with no control characters.
type Model = connectorcontract.DiscoveredModel

var (
	// errDataMissing: the response has no usable data array.
	errDataMissing = openai.ErrModelsDataMissing
	// errTooManyModels: the list exceeds MaxModels.
	errTooManyModels = openai.ErrTooManyModels
	// errEmptyModelID: an entry has an empty id.
	errEmptyModelID = openai.ErrEmptyModelID
	// errModelIDTooLong: an entry id exceeds MaxModelIDRunes.
	errModelIDTooLong = openai.ErrModelIDTooLong
	// errInvalidModelID: an id contains control characters, DEL, or the
	// U+FFFD replacement rune (evidence of coerced invalid UTF-8 upstream).
	errInvalidModelID = openai.ErrInvalidModelID
	// errProviderTooLong: an owned_by/provider exceeds MaxProviderRunes.
	errProviderTooLong = openai.ErrProviderTooLong
	// errInvalidProvider: a provider contains control characters or U+FFFD.
	errInvalidProvider = openai.ErrInvalidProvider
	// errDuplicateModelID: the list contains a duplicate id.
	errDuplicateModelID = openai.ErrDuplicateModelID
	// errTruncatedBody: the response exceeded MaxModelsBodyBytes.
	errTruncatedBody = openai.ErrModelsResponseTruncated
	// errMalformedJSON: the body is not valid JSON.
	errMalformedJSON = openai.ErrMalformedModelsJSON
)

// parseModels is the compatibility test seam for the parser now owned by the
// OpenAI registry discoverer. There is no second fetch/parser implementation:
// production discovery and these legacy package tests execute the same
// ParseModels function.
//
// Entries are validated strictly: id must be non-empty after ASCII trimming,
// within MaxModelIDRunes, free of C0 controls/DEL/U+FFFD, and unique across
// the list; provider must be within MaxProviderRunes and control-free
// (emptiness is allowed, matching upstreams that omit owned_by). Any
// violation rejects the whole list: a partial list is never cached.
func parseModels(body []byte) ([]Model, error) {
	return openai.ParseModels(body)
}
