// Package httpapi provides stateless input boundaries for HTTP handlers.
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

var (
	ErrInvalid  = errors.New("invalid request input")
	ErrTooLarge = errors.New("request body is too large")
)

// RequestField distinguishes omission from the zero value. Explicit null is
// rejected; use NullableField for fields that support clearing.
type RequestField[T any] struct {
	Value T
	Set   bool
}

func (field *RequestField[T]) UnmarshalJSON(data []byte) error {
	if field == nil || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return ErrInvalid
	}
	if err := DecodeJSON(data, &field.Value); err != nil {
		return err
	}
	field.Set = true
	return nil
}

// NullableField preserves missing, null, and non-null values independently.
type NullableField[T any] struct {
	Value *T
	Set   bool
}

func (field *NullableField[T]) UnmarshalJSON(data []byte) error {
	if field == nil {
		return ErrInvalid
	}
	field.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		field.Value = nil
		return nil
	}
	var value T
	if err := DecodeJSON(data, &value); err != nil {
		return err
	}
	field.Value = &value
	return nil
}

// BodyOptions keeps route-specific byte, syntax, and media-type policies
// explicit. A nil ContentType accepts any media type. ReadJSON requires Validate:
// existing routes have different syntax and field-budget contracts.
type BodyOptions struct {
	MaxBytes    int64
	Validate    func([]byte) error
	ContentType func(string) bool
}

// ReadJSON reads a bounded body and validates syntax before decoding. Raw bytes
// are returned only on success. Partial input is cleared on failure and errors
// carry no request contents.
func ReadJSON(writer http.ResponseWriter, request *http.Request, destination any, options BodyOptions) ([]byte, error) {
	if destination == nil || options.Validate == nil {
		return nil, ErrInvalid
	}
	body, err := ReadBody(writer, request, options)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || DecodeJSON(body, destination) != nil {
		clear(body)
		return nil, ErrInvalid
	}
	return body, nil
}

// ReadBody reads bounded raw input and applies the supplied syntax policy.
// Without Validate, raw/empty body semantics remain with callers; typed JSON
// consumers must also call DecodeJSON to enforce the complete-value boundary.
func ReadBody(writer http.ResponseWriter, request *http.Request, options BodyOptions) ([]byte, error) {
	if request == nil || request.Body == nil || options.MaxBytes < 1 ||
		(options.ContentType != nil && !options.ContentType(request.Header.Get("Content-Type"))) {
		return nil, ErrInvalid
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, options.MaxBytes))
	if err != nil {
		clear(body)
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			return nil, ErrTooLarge
		}
		return nil, ErrInvalid
	}
	if options.Validate != nil && options.Validate(body) != nil {
		clear(body)
		return nil, ErrInvalid
	}
	return body, nil
}

// DecodeJSON applies destination-specific unknown-field rules and requires
// exactly one value. Syntax budgets are enforced separately by the caller.
func DecodeJSON(data []byte, destination any) error { return decodeJSON(data, destination, false) }

// DecodeJSONWithNumbers preserves exact JSON numbers for domain parsers that
// intentionally retain numeric input rather than decoding it to float64.
func DecodeJSONWithNumbers(data []byte, destination any) error {
	return decodeJSON(data, destination, true)
}

func decodeJSON(data []byte, destination any, useNumber bool) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if useNumber {
		decoder.UseNumber()
	}
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func JSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

// ParseQuery leaves bare '?' acceptance to the route's existing policy.
func ParseQuery(request *http.Request, rejectForceQuery bool) (url.Values, error) {
	if request == nil || request.URL == nil || (rejectForceQuery && request.URL.ForceQuery) {
		return nil, ErrInvalid
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return nil, ErrInvalid
	}
	return values, nil
}

func ExactQuery(values url.Values, allowed ...string) bool {
	known := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		known[key] = struct{}{}
	}
	for key, entries := range values {
		if _, exists := known[key]; !exists || len(entries) != 1 {
			return false
		}
	}
	return true
}

// NoBody accepts an absent or actually empty body, without relying on headers.
func NoBody(request *http.Request) bool {
	if request == nil || request.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1))
	return err == nil && len(body) == 0
}

// EmptyQuery can require an empty raw query or accept syntactically empty
// query separators, matching the different existing ingress contracts.
func EmptyQuery(request *http.Request, rejectForceQuery, rawOnly bool) bool {
	values, err := ParseQuery(request, rejectForceQuery)
	return err == nil && len(values) == 0 && (!rawOnly || request.URL.RawQuery == "")
}
