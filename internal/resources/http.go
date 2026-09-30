package resources

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type httpAPI struct {
	repository *Repository
}

// RegisterRoutes registers the complete owner-scoped resource API through
// the authentication and maintenance seam. Root mux wiring remains owned by
// the application composition layer.
func RegisterRoutes(registrar UserRouteRegistrar, repository *Repository) error {
	if isNilInterface(registrar) || repository == nil {
		return errors.New("resources: route registrar and repository are required")
	}
	api := &httpAPI{repository: repository}
	routes := []struct {
		method, pattern string
		handler         AuthorizedUserHandler
	}{
		{http.MethodGet, routeEndpoints, api.listEndpoints},
		{http.MethodPost, routeEndpoints, api.createEndpoint},
		{http.MethodGet, routeEndpointCreateOptions, api.endpointCreateOptions},
		{http.MethodGet, routeEndpoint, api.getEndpoint},
		{http.MethodPatch, routeEndpoint, api.patchEndpoint},
		{http.MethodDelete, routeEndpoint, api.deleteEndpoint},
		{http.MethodGet, routeEndpointAdaptation, api.getEndpointAdaptation},
		{http.MethodPut, routeEndpointAdaptation, api.putEndpointAdaptation},
		{http.MethodGet, routeEndpointKeys, api.listEndpointKeys},
		{http.MethodPost, routeEndpointKeys, api.createEndpointKey},
		{http.MethodGet, routeKeyBindings, api.listKeyBindings},
		{http.MethodPatch, routeEndpointKey, api.patchEndpointKey},
		{http.MethodDelete, routeEndpointKey, api.deleteEndpointKey},
		{http.MethodGet, routeCatalog, api.getCatalog},
		{http.MethodPost, routeDiscovery, api.refreshDiscovery},
		{http.MethodPost, routeManualCatalog, api.createManualEntries},
		{http.MethodPatch, routeManualEntry, api.updateManualEntry},
		{http.MethodDelete, routeManualEntry, api.deleteManualEntry},
		{http.MethodGet, routeModels, api.listModels},
		{http.MethodPost, routeModels, api.createModel},
		{http.MethodGet, routeModel, api.getModel},
		{http.MethodPatch, routeModel, api.patchModel},
		{http.MethodDelete, routeModel, api.deleteModel},
		{http.MethodGet, routeBindingCandidates, api.bindingCandidates},
		{http.MethodGet, routeBindings, api.listBindings},
		{http.MethodPost, routeBindingBatch, api.addBindings},
		{http.MethodPut, routeBindingOrder, api.orderBindings},
		{http.MethodDelete, routeBinding, api.deleteBinding},
		{http.MethodGet, "/api/caller-key", api.getCallerKey},
		{http.MethodPost, "/api/caller-key/regenerate", api.regenerateCallerKey},
	}
	for _, route := range routes {
		if err := registrar.RegisterUserRoute(route.method, route.pattern, route.handler); err != nil {
			return fmt.Errorf("resources: register %s %s: %w", route.method, route.pattern, err)
		}
	}
	return nil
}

type requestField[T any] = httpapi.RequestField[T]

func decodeStrictObject[T any](writer http.ResponseWriter, request *http.Request, destination *T) ([]byte, bool) {
	body, err := httpapi.ReadJSON(writer, request, destination, httpapi.BodyOptions{MaxBytes: idempotency.MaxControlBodyBytes, Validate: validateResourceJSONObject})
	if err != nil {
		if errors.Is(err, httpapi.ErrTooLarge) {
			httperr.WriteError(writer, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeResourceError(writer, ErrInvalidRequest)
		}
		return nil, false
	}
	return body, true
}

func requireNoBody(writer http.ResponseWriter, request *http.Request) bool {
	if !httpapi.NoBody(request) {
		writeResourceError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func parsePathID(writer http.ResponseWriter, request *http.Request, name string) (int64, bool) {
	if request == nil {
		writeResourceError(writer, ErrInvalidRequest)
		return 0, false
	}
	id, err := parseDecimalID(request.PathValue(name))
	if err != nil {
		writeResourceError(writer, ErrNotFound)
		return 0, false
	}
	return id, true
}

func exactQuery(values url.Values, allowed ...string) bool {
	return httpapi.ExactQuery(values, allowed...)
}

func requestQuery(writer http.ResponseWriter, request *http.Request) (url.Values, bool) {
	values, err := httpapi.ParseQuery(request, false)
	if err != nil {
		writeResourceError(writer, ErrInvalidRequest)
		return nil, false
	}
	return values, true
}

func parsePageQuery(writer http.ResponseWriter, request *http.Request) (int, string, bool) {
	values, parsed := requestQuery(writer, request)
	if !parsed {
		return 0, "", false
	}
	if !exactQuery(values, "cursor", "limit") {
		writeResourceError(writer, ErrInvalidRequest)
		return 0, "", false
	}
	limit := 0
	if value, present := values["limit"]; present {
		parsed, err := strconv.Atoi(value[0])
		if err != nil || !validPageLimit(parsed) {
			writeResourceError(writer, ErrInvalidRequest)
			return 0, "", false
		}
		limit = parsed
	}
	cursor := ""
	if value, present := values["cursor"]; present {
		if value[0] == "" {
			writeResourceError(writer, ErrInvalidRequest)
			return 0, "", false
		}
		cursor = value[0]
	}
	return limit, cursor, true
}

func requireEmptyQuery(writer http.ResponseWriter, request *http.Request) bool {
	values, ok := requestQuery(writer, request)
	if !ok {
		return false
	}
	if len(values) != 0 {
		writeResourceError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func idempotencyKey(writer http.ResponseWriter, request *http.Request) (string, bool) {
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		writeResourceError(writer, ErrInvalidRequest)
		return "", false
	}
	key := values[0]
	if _, err := idempotency.KeyHash(key); err != nil {
		writeResourceError(writer, ErrInvalidRequest)
		return "", false
	}
	return key, true
}

func controlMutation(writer http.ResponseWriter, request *http.Request, route string, pathIDs []int64, canonical any) (ControlMutation, bool) {
	key, ok := idempotencyKey(writer, request)
	if !ok {
		return ControlMutation{}, false
	}
	body, err := idempotency.CanonicalJSON(canonical)
	if err != nil {
		writeResourceError(writer, ErrInvalidRequest)
		return ControlMutation{}, false
	}
	ids := make([]string, len(pathIDs))
	for index, id := range pathIDs {
		if id <= 0 {
			writeResourceError(writer, ErrInvalidRequest)
			return ControlMutation{}, false
		}
		ids[index] = strconv.FormatInt(id, 10)
	}
	return ControlMutation{
		IdempotencyKey: key, Method: request.Method, Route: route,
		PathIDs: ids, Query: "", CanonicalBody: body,
	}, true
}

func noBodyMutation(writer http.ResponseWriter, request *http.Request, route string, pathIDs ...int64) (ControlMutation, bool) {
	if !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return ControlMutation{}, false
	}
	key, ok := idempotencyKey(writer, request)
	if !ok {
		return ControlMutation{}, false
	}
	ids := make([]string, len(pathIDs))
	for index, id := range pathIDs {
		ids[index] = strconv.FormatInt(id, 10)
	}
	return ControlMutation{IdempotencyKey: key, Method: request.Method, Route: route, PathIDs: ids}, true
}

func writeMutation[T any](writer http.ResponseWriter, result MutationResult[T]) {
	writer.Header().Set("Cache-Control", "no-store")
	if result.Status == http.StatusNoContent {
		writer.WriteHeader(result.Status)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(result.Status)
	_, _ = writer.Write(result.Body)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	httperr.WriteJSON(writer, status, value)
}

func writeResourceError(writer http.ResponseWriter, err error) {
	httperr.WriteMapped(writer, err,
		httperr.Mapping{Err: ErrInvalidRequest, Code: httperr.CodeInvalidRequest, Message: "invalid request"},
		httperr.Mapping{Err: ErrUnauthorized, Code: httperr.CodeUnauthorized, Message: "authentication required"},
		httperr.Mapping{Err: ErrForbidden, Code: httperr.CodeForbidden, Message: "access denied"},
		httperr.Mapping{Err: ErrMaintenance, Code: httperr.CodeMaintenance, Message: "maintenance in progress"},
		httperr.Mapping{Err: ErrNotFound, Code: httperr.CodeNotFound, Message: "not found"},
		httperr.Mapping{Err: ErrConflict, Code: httperr.CodeConflict, Message: "request conflicts with current state"},
		httperr.Mapping{Err: ErrResourceLocked, Code: httperr.CodeResourceLocked, Message: "resource is locked"},
		httperr.Mapping{Err: ErrResourceLimit, Code: httperr.CodeResourceLimitExceeded, Message: "resource limit exceeded"},
		httperr.Mapping{Err: errDiscoveryWorkerUnavailable, Code: httperr.CodeServiceUnavailable, Message: "service unavailable"},
	)
}

func canonicalExpectedRevision(value requestField[string]) (int64, bool) {
	if !value.Set {
		return 0, false
	}
	revision, err := parseDecimalID(value.Value)
	return revision, err == nil
}

func canonicalExpectedGeneration(value requestField[string]) (int64, bool) {
	if !value.Set || value.Value == "" || (len(value.Value) > 1 && value.Value[0] == '0') {
		return 0, false
	}
	for index := range value.Value {
		if value.Value[index] < '0' || value.Value[index] > '9' {
			return 0, false
		}
	}
	generation, err := strconv.ParseInt(value.Value, 10, 64)
	return generation, err == nil && generation >= 0
}

func optionalPointer[T any](field requestField[T]) *T {
	if !field.Set {
		return nil
	}
	value := field.Value
	return &value
}
