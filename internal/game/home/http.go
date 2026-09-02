package home

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const RouteSummary = "/api/home/game-summary"

type httpAPI struct{ service *Service }

func RegisterRoutes(registrar resources.UserRouteRegistrar, service *Service) error {
	if registrar == nil || service == nil {
		return ErrInvalid
	}
	api := &httpAPI{service: service}
	if err := registrar.RegisterUserRoute(http.MethodGet, RouteSummary, api.summary); err != nil {
		return fmt.Errorf("game home: register GET %s: %w", RouteSummary, err)
	}
	return nil
}

func (api *httpAPI) summary(writer http.ResponseWriter, request *http.Request, principal resources.UserPrincipal) {
	if !noBody(writer, request) || !noQuery(writer, request) {
		return
	}
	result, err := api.service.Read(request.Context(), principal.UserID)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, result)
}

func noBody(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil || len(body) != 0 {
		writeError(writer, ErrInvalid)
		return false
	}
	return true
}

func noQuery(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.URL == nil || request.URL.ForceQuery && request.URL.RawQuery == "" {
		writeError(writer, ErrInvalid)
		return false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(values) != 0 {
		writeError(writer, ErrInvalid)
		return false
	}
	return true
}

func writeJSON(writer http.ResponseWriter, value Summary) {
	body, err := json.Marshal(value)
	if err != nil {
		writeError(writer, ErrInvariant)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body)
}

func writeError(writer http.ResponseWriter, err error) {
	code, message := httperr.CodeInternal, "request failed"
	switch {
	case errors.Is(err, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "invalid request"
	case errors.Is(err, ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "authentication required"
	case errors.Is(err, ErrForbidden):
		code, message = httperr.CodeForbidden, "forbidden"
	case errors.Is(err, ErrMaintenance):
		code, message = httperr.CodeMaintenance, "maintenance mode"
	case errors.Is(err, ErrResourceLimit):
		code, message = httperr.CodeResourceLimitExceeded, "resource limit exceeded"
	case errors.Is(err, ErrUnavailable):
		code, message = httperr.CodeServiceUnavailable, "service unavailable"
	}
	httperr.WriteError(writer, httperr.New(code, message))
}
