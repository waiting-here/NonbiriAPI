package issues

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const routeIssues = "/api/issues"

type httpAPI struct {
	service *Service
}

func RegisterRoutes(registrar UserRouteRegistrar, service *Service) error {
	if isNilInterface(registrar) || service == nil || service.repository == nil {
		return errors.New("issues: route registrar and service are required")
	}
	api := &httpAPI{service: service}
	if err := registrar.RegisterUserRoute(http.MethodGet, routeIssues, api.list); err != nil {
		return fmt.Errorf("issues: register GET %s: %w", routeIssues, err)
	}
	return nil
}

func (api *httpAPI) list(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	if request == nil || request.URL == nil || request.URL.ForceQuery {
		writeError(writer, ErrInvalidRequest)
		return
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || !exactQuery(values, "state", "cursor", "limit") {
		writeError(writer, ErrInvalidRequest)
		return
	}
	states, present := values["state"]
	if !present || len(states) != 1 || (states[0] != "current" && states[0] != "closed") {
		writeError(writer, ErrInvalidRequest)
		return
	}
	query := ListQuery{State: states[0]}
	if cursor, present := values["cursor"]; present {
		if cursor[0] == "" || len(cursor[0]) > maxCursorBytes {
			writeError(writer, ErrInvalidRequest)
			return
		}
		query.Cursor = cursor[0]
	}
	if limit, present := values["limit"]; present {
		parsed, err := strconv.Atoi(limit[0])
		if err != nil || parsed < 1 || parsed > maxPageLimit {
			writeError(writer, ErrInvalidRequest)
			return
		}
		query.Limit = parsed
	}
	response, err := api.service.List(request.Context(), principal.UserID, query)
	if err != nil {
		writeError(writer, err)
		return
	}
	httperr.WriteJSON(writer, http.StatusOK, response)
}

func exactQuery(values url.Values, allowed ...string) bool {
	allow := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allow[key] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := allow[key]; !ok || len(entries) != 1 {
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

func writeError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		httperr.WriteError(writer, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
	case errors.Is(err, ErrUnauthorized):
		httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
	case errors.Is(err, ErrForbidden):
		httperr.WriteError(writer, httperr.New(httperr.CodeForbidden, "access forbidden"))
	case errors.Is(err, ErrNotFound):
		httperr.WriteError(writer, httperr.New(httperr.CodeNotFound, "resource not found"))
	case errors.Is(err, ErrConflict):
		httperr.WriteError(writer, httperr.New(httperr.CodeConflict, "resource state conflict"))
	case errors.Is(err, ErrUnavailable):
		httperr.WriteError(writer, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
	default:
		httperr.WriteError(writer, httperr.New(httperr.CodeInternal, "internal error"))
	}
}
