package adminalerts

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
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const (
	routeAlerts         = "/admin/api/alerts"
	routeResolveAlert   = "/admin/api/alerts/{id}/resolve"
	maxRawQueryBytes    = 8192
	maxResolveBodyBytes = 4096
)

type httpAPI struct {
	repository *Repository
}

func RegisterRoutes(registrar AdminRouteRegistrar, repository *Repository) error {
	if isNilInterface(registrar) || repository == nil || repository.database == nil {
		return errors.New("administrator alerts: route registrar and repository are required")
	}
	api := &httpAPI{repository: repository}
	routes := []struct {
		method  string
		pattern string
		handler AuthorizedAdminHandler
	}{
		{method: http.MethodGet, pattern: routeAlerts, handler: api.list},
		{method: http.MethodPost, pattern: routeResolveAlert, handler: api.resolve},
	}
	for _, route := range routes {
		if err := registrar.RegisterAdminRoute(route.method, route.pattern, route.handler); err != nil {
			return fmt.Errorf("administrator alerts: register %s %s: %w", route.method, route.pattern, err)
		}
	}
	return nil
}

func (api *httpAPI) list(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	query, ok := parseListRequest(writer, request)
	if !ok {
		return
	}
	page, err := api.repository.List(request.Context(), principal.UserID, query)
	if err != nil {
		writeError(writer, err)
		return
	}
	httperr.WriteJSON(writer, http.StatusOK, page)
}

func (api *httpAPI) resolve(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	alertID, ok := parseAlertID(request)
	if !ok {
		writeError(writer, ErrInvalidRequest)
		return
	}
	resolved, ok := parseResolveRequest(writer, request)
	if !ok {
		return
	}
	value, err := api.repository.SetResolved(request.Context(), principal.UserID, alertID, resolved)
	if err != nil {
		writeError(writer, err)
		return
	}
	httperr.WriteJSON(writer, http.StatusOK, value)
}

func parseListRequest(writer http.ResponseWriter, request *http.Request) (ListQuery, bool) {
	if !requireNoBody(writer, request) {
		return ListQuery{}, false
	}
	values, ok := strictQuery(writer, request)
	if !ok {
		return ListQuery{}, false
	}
	if !exactQuery(values, "resolved", "cursor", "limit") {
		writeError(writer, ErrInvalidRequest)
		return ListQuery{}, false
	}
	query := ListQuery{}
	if entries, set := values["resolved"]; set {
		switch entries[0] {
		case "true":
			value := true
			query.Resolved = &value
		case "false":
			value := false
			query.Resolved = &value
		default:
			writeError(writer, ErrInvalidRequest)
			return ListQuery{}, false
		}
	}
	if entries, set := values["cursor"]; set {
		if entries[0] == "" || len(entries[0]) > maxCursorBytes {
			writeError(writer, ErrInvalidRequest)
			return ListQuery{}, false
		}
		query.Cursor = entries[0]
	}
	if entries, set := values["limit"]; set {
		limit, err := strconv.ParseInt(entries[0], 10, 32)
		if err != nil || limit < 1 || limit > maxPageLimit || strconv.FormatInt(limit, 10) != entries[0] {
			writeError(writer, ErrInvalidRequest)
			return ListQuery{}, false
		}
		query.Limit = int(limit)
	}
	return query, true
}

func strictQuery(writer http.ResponseWriter, request *http.Request) (url.Values, bool) {
	if request == nil || request.URL == nil || len(request.URL.RawQuery) > maxRawQueryBytes {
		writeError(writer, ErrInvalidRequest)
		return nil, false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeError(writer, ErrInvalidRequest)
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
		if _, ok := known[key]; !ok || len(entries) != 1 {
			return false
		}
	}
	return true
}

func requireNoBody(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil || len(body) != 0 {
		writeError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func requireEmptyQuery(writer http.ResponseWriter, request *http.Request) bool {
	values, ok := strictQuery(writer, request)
	if !ok {
		return false
	}
	if len(values) != 0 {
		writeError(writer, ErrInvalidRequest)
		return false
	}
	return true
}

func parseAlertID(request *http.Request) (int64, bool) {
	if request == nil {
		return 0, false
	}
	raw := request.PathValue("id")
	value, err := strconv.ParseInt(raw, 10, 64)
	return value, err == nil && value > 0 && strconv.FormatInt(value, 10) == raw
}

type optionalBool struct {
	value bool
	set   bool
}

func (field *optionalBool) UnmarshalJSON(data []byte) error {
	if field == nil {
		return errors.New("invalid boolean field")
	}
	switch string(bytes.TrimSpace(data)) {
	case "true":
		field.value, field.set = true, true
	case "false":
		field.value, field.set = false, true
	default:
		return errors.New("boolean field is required")
	}
	return nil
}

type resolveWire struct {
	Resolved optionalBool `json:"resolved"`
}

func parseResolveRequest(writer http.ResponseWriter, request *http.Request) (bool, bool) {
	if !requireEmptyQuery(writer, request) {
		return false, false
	}
	if request == nil || request.Body == nil {
		return true, true
	}
	limited := http.MaxBytesReader(writer, request.Body, maxResolveBodyBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httperr.WriteError(writer, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeError(writer, ErrInvalidRequest)
		}
		return false, false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return true, true
	}
	if strictjson.ValidateObject(body) != nil {
		writeError(writer, ErrInvalidRequest)
		return false, false
	}
	var wire resolveWire
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		writeError(writer, ErrInvalidRequest)
		return false, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(writer, ErrInvalidRequest)
		return false, false
	}
	if !wire.Resolved.set {
		return true, true
	}
	return wire.Resolved.value, true
}

func writeError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		httperr.WriteError(writer, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
	case errors.Is(err, ErrUnauthorized):
		httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
	case errors.Is(err, ErrForbidden):
		httperr.WriteError(writer, httperr.New(httperr.CodeForbidden, "access denied"))
	case errors.Is(err, ErrNotFound):
		httperr.WriteError(writer, httperr.New(httperr.CodeNotFound, "not found"))
	default:
		httperr.WriteError(writer, httperr.New(httperr.CodeInternal, "internal error"))
	}
}
