package donation

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

type requiredField[T any] = httpapi.RequestField[T]

type nullableField[T any] = httpapi.NullableField[T]

func decodeStrictObject[T any](writer http.ResponseWriter, request *http.Request, destination *T) bool {
	body, err := httpapi.ReadJSON(writer, request, destination, httpapi.BodyOptions{MaxBytes: idempotency.MaxControlBodyBytes, Validate: strictjson.ValidateObject})
	if err != nil {
		if errors.Is(err, httpapi.ErrTooLarge) {
			httperr.WriteError(writer, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeDonationError(writer, ErrInvalidRequest)
		}
		return false
	}
	clear(body)
	return true
}

func requireNoBody(writer http.ResponseWriter, request *http.Request) bool {
	if !httpapi.NoBody(request) {
		writeDonationError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func requestQuery(writer http.ResponseWriter, request *http.Request) (url.Values, bool) {
	values, err := httpapi.ParseQuery(request, false)
	if err != nil {
		writeDonationError(writer, ErrInvalidRequest)
		return nil, false
	}
	return values, true
}

func exactQuery(values url.Values, allowed ...string) bool {
	return httpapi.ExactQuery(values, allowed...)
}

func requireEmptyQuery(writer http.ResponseWriter, request *http.Request) bool {
	values, ok := requestQuery(writer, request)
	if !ok {
		return false
	}
	if len(values) != 0 {
		writeDonationError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func parsePathID(writer http.ResponseWriter, request *http.Request, name string) (int64, bool) {
	if request == nil {
		writeDonationError(writer, ErrInvalidRequest)
		return 0, false
	}
	value, err := parseCanonicalID(request.PathValue(name))
	if err != nil {
		writeDonationError(writer, ErrNotFound)
		return 0, false
	}
	return value, true
}

func parseCanonicalID(value string) (int64, error) {
	if value == "" || len(value) > 19 || len(value) > 1 && value[0] == '0' {
		return 0, ErrInvalidRequest
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return 0, ErrInvalidRequest
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, ErrInvalidRequest
	}
	return parsed, nil
}

func parsePage(values url.Values, allowed ...string) (int, string, bool) {
	if !exactQuery(values, allowed...) {
		return 0, "", false
	}
	limit := defaultHTTPPageLimit
	if raw, exists := values["limit"]; exists {
		parsed, err := strconv.Atoi(raw[0])
		if err != nil || parsed < 1 || parsed > 100 {
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
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		writeDonationError(writer, ErrInvalidRequest)
		return resources.ControlMutation{}, false
	}
	if _, err := idempotency.KeyHash(values[0]); err != nil {
		writeDonationError(writer, ErrInvalidRequest)
		return resources.ControlMutation{}, false
	}
	body, err := idempotency.CanonicalJSON(canonical)
	if err != nil {
		writeDonationError(writer, ErrInvalidRequest)
		return resources.ControlMutation{}, false
	}
	ids := make([]string, len(pathIDs))
	for index, id := range pathIDs {
		if id <= 0 {
			writeDonationError(writer, ErrInvalidRequest)
			return resources.ControlMutation{}, false
		}
		ids[index] = strconv.FormatInt(id, 10)
	}
	return resources.ControlMutation{IdempotencyKey: values[0], Method: request.Method, Route: route,
		PathIDs: ids, Query: charityscope.Query(request.Context()), CanonicalBody: body}, true
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

func writeDonationError(writer http.ResponseWriter, err error) {
	httperr.WriteMapped(writer, err,
		httperr.Mapping{Err: ErrInvalidRequest, Code: httperr.CodeInvalidRequest, Message: "invalid request"},
		httperr.Mapping{Err: ErrUnauthorized, Code: httperr.CodeUnauthorized, Message: "authentication required"},
		httperr.Mapping{Err: ErrForbidden, Code: httperr.CodeForbidden, Message: "access denied"},
		httperr.Mapping{Err: ErrFeatureDisabled, Code: httperr.CodeFeatureDisabled, Message: "feature disabled"},
		httperr.Mapping{Err: ErrNotFound, Code: httperr.CodeNotFound, Message: "not found"},
		httperr.Mapping{Err: ErrConflict, Code: httperr.CodeConflict, Message: "request conflicts with current state"},
		httperr.Mapping{Err: ErrResourceLocked, Code: httperr.CodeResourceLocked, Message: "resource is locked"},
		httperr.Mapping{Err: ErrResourceLimit, Code: httperr.CodeResourceLimitExceeded, Message: "resource limit exceeded"},
		httperr.Mapping{Err: ErrUnavailable, Code: httperr.CodeServiceUnavailable, Message: "service unavailable"},
	)
}

func registerError(domain, method, pattern string, err error) error {
	return fmt.Errorf("%s: register %s %s: %w", domain, method, pattern, err)
}

const defaultHTTPPageLimit = 50
