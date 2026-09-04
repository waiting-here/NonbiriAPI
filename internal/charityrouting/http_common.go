package charityrouting

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

type requiredField[T any] struct {
	Value T
	Set   bool
}

func (field *requiredField[T]) UnmarshalJSON(data []byte) error {
	if field == nil || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("null is not allowed")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&field.Value); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	field.Set = true
	return nil
}

type nullableField[T any] struct {
	Value *T
	Set   bool
}

func (field *nullableField[T]) UnmarshalJSON(data []byte) error {
	if field == nil {
		return errors.New("invalid nullable field")
	}
	field.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		field.Value = nil
		return nil
	}
	var value T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	field.Value = &value
	return nil
}

func decodeStrictObject[T any](writer http.ResponseWriter, request *http.Request, destination *T) bool {
	if request == nil || request.Body == nil || destination == nil {
		writeRoutingError(writer, ErrInvalidRequest)
		return false
	}
	limited := http.MaxBytesReader(writer, request.Body, idempotency.MaxControlBodyBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			httperr.WriteError(writer, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeRoutingError(writer, ErrInvalidRequest)
		}
		return false
	}
	if len(body) == 0 || strictjson.ValidateObject(body) != nil {
		writeRoutingError(writer, ErrInvalidRequest)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeRoutingError(writer, ErrInvalidRequest)
		return false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		writeRoutingError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func requireNoBody(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil || len(body) != 0 {
		writeRoutingError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func requestQuery(writer http.ResponseWriter, request *http.Request) (url.Values, bool) {
	if request == nil || request.URL == nil {
		writeRoutingError(writer, ErrInvalidRequest)
		return nil, false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeRoutingError(writer, ErrInvalidRequest)
		return nil, false
	}
	return values, true
}

func exactQuery(values url.Values, allowed ...string) bool {
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

func requireEmptyQuery(writer http.ResponseWriter, request *http.Request) bool {
	values, ok := requestQuery(writer, request)
	if !ok {
		return false
	}
	if len(values) != 0 {
		writeRoutingError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func parsePathID(writer http.ResponseWriter, request *http.Request, name string) (int64, bool) {
	if request == nil {
		writeRoutingError(writer, ErrInvalidRequest)
		return 0, false
	}
	id, err := parsePositiveID(request.PathValue(name))
	if err != nil {
		writeRoutingError(writer, ErrNotFound)
		return 0, false
	}
	return id, true
}

func parsePage(values url.Values, allowed ...string) (int, string, bool) {
	if !exactQuery(values, allowed...) {
		return 0, "", false
	}
	limit := defaultPageLimit
	if raw, exists := values["limit"]; exists {
		parsed, err := strconv.Atoi(raw[0])
		if err != nil || parsed < 1 || parsed > maxPageLimit {
			return 0, "", false
		}
		limit = parsed
	}
	cursor := ""
	if raw, exists := values["cursor"]; exists {
		if raw[0] == "" {
			return 0, "", false
		}
		cursor = raw[0]
	}
	return limit, cursor, true
}

func mutationFor(writer http.ResponseWriter, request *http.Request, route string, pathIDs []int64, canonical any) (resources.ControlMutation, bool) {
	keys := request.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		writeRoutingError(writer, ErrInvalidRequest)
		return resources.ControlMutation{}, false
	}
	if _, err := idempotency.KeyHash(keys[0]); err != nil {
		writeRoutingError(writer, ErrInvalidRequest)
		return resources.ControlMutation{}, false
	}
	body, err := idempotency.CanonicalJSON(canonical)
	if err != nil {
		writeRoutingError(writer, ErrInvalidRequest)
		return resources.ControlMutation{}, false
	}
	ids := make([]string, len(pathIDs))
	for index, id := range pathIDs {
		if id <= 0 {
			writeRoutingError(writer, ErrInvalidRequest)
			return resources.ControlMutation{}, false
		}
		ids[index] = strconv.FormatInt(id, 10)
	}
	return resources.ControlMutation{IdempotencyKey: keys[0], Method: request.Method, Route: route,
		PathIDs: ids, CanonicalBody: body}, true
}

func writeMutation[T any](writer http.ResponseWriter, result resources.MutationResult[T]) {
	writer.Header().Set("Cache-Control", "no-store")
	if result.Status == http.StatusNoContent {
		writer.WriteHeader(result.Status)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(result.Status)
	_, _ = writer.Write(result.Body)
}

func writeJSON(writer http.ResponseWriter, value any) {
	httperr.WriteJSON(writer, http.StatusOK, value)
}

func writeRoutingError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		httperr.WriteError(writer, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
	case errors.Is(err, ErrUnauthorized):
		httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
	case errors.Is(err, ErrForbidden):
		httperr.WriteError(writer, httperr.New(httperr.CodeForbidden, "access denied"))
	case errors.Is(err, ErrNotFound):
		httperr.WriteError(writer, httperr.New(httperr.CodeNotFound, "not found"))
	case errors.Is(err, ErrConflict):
		httperr.WriteError(writer, httperr.New(httperr.CodeConflict, "request conflicts with current state"))
	case errors.Is(err, ErrResourceLimit):
		httperr.WriteError(writer, httperr.New(httperr.CodeResourceLimitExceeded, "resource limit exceeded"))
	case errors.Is(err, ErrUnavailable):
		httperr.WriteError(writer, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
	default:
		httperr.WriteError(writer, httperr.New(httperr.CodeInternal, "internal error"))
	}
}

func registerError(method, pattern string, err error) error {
	return fmt.Errorf("charity routing: register %s %s: %w", method, pattern, err)
}
