package host

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type httpAPI struct{ service *Service }

func (service *Service) RegisterRoutes(registrars Registrars) error {
	if service == nil {
		return ErrInvalidRequest
	}
	service.operationMu.Lock()
	defer service.operationMu.Unlock()
	if service.closed.Load() || service.routesRegistered || registrars.User == nil || registrars.Admin == nil || registrars.Continuation == nil || registrars.Maintenance == nil {
		return ErrInvalidRequest
	}
	for _, descriptor := range service.registry.Descriptors() {
		guard := newRouteGuard(descriptor, registrars)
		if err := service.modules[descriptor.ID].RegisterRoutes(guard.registrars()); err != nil {
			return err
		}
		if !guard.complete() {
			return game.ErrInvalidContract
		}
	}
	api := &httpAPI{service: service}
	for _, route := range []struct {
		path    string
		handler resources.AuthorizedUserHandler
	}{{RouteGames, api.games}, {RouteHomeSummary, api.summary}} {
		if err := registrars.User.RegisterUserRoute(http.MethodGet, route.path, route.handler); err != nil {
			return err
		}
	}
	for _, route := range []struct {
		method, path string
		handler      http.HandlerFunc
	}{{http.MethodGet, RouteAdminActiveCounts, api.activeCounts}, {http.MethodGet, RouteAdminGamesConfig, api.getConfig}, {http.MethodPatch, RouteAdminGamesConfig, api.patchConfig}} {
		if err := registrars.Admin.RegisterAdminRoute(route.method, route.path, route.handler); err != nil {
			return err
		}
	}
	service.routesRegistered = true
	return nil
}

func (api *httpAPI) summary(writer http.ResponseWriter, request *http.Request, principal resources.UserPrincipal) {
	if !noBody(writer, request) || !requireExactQuery(writer, request) {
		return
	}
	result, err := api.service.HomeSummary(request.Context(), principal.UserID)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (api *httpAPI) games(w http.ResponseWriter, r *http.Request, principal resources.UserPrincipal) {
	if !noBody(w, r) || !requireExactQuery(w, r) {
		return
	}
	snapshot, err := api.service.GamesSnapshot(r.Context(), principal.UserID, api.service.services.Now())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}
func (api *httpAPI) activeCounts(w http.ResponseWriter, r *http.Request) {
	if !noBody(w, r) || !requireExactQuery(w, r) {
		return
	}
	result, err := api.service.ActiveCounts(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (api *httpAPI) getConfig(w http.ResponseWriter, r *http.Request) {
	if !noBody(w, r) || !requireExactQuery(w, r) {
		return
	}
	result, err := api.service.ReadGamesConfig(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (api *httpAPI) patchConfig(w http.ResponseWriter, r *http.Request) {
	if !requireExactQuery(w, r) {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	result, err := api.service.PatchGamesConfig(r.Context(), body, key)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r == nil || r.Body == nil {
		returnInvalid(w)
		return nil, false
	}
	limited := http.MaxBytesReader(w, r.Body, idempotency.MaxControlBodyBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			returnInvalid(w)
		}
		return nil, false
	}
	if len(body) == 0 {
		returnInvalid(w)
		return nil, false
	}
	return body, true
}
func noBody(w http.ResponseWriter, r *http.Request) bool {
	if r == nil || r.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(body) != 0 {
		returnInvalid(w)
		return false
	}
	return true
}

func requireIdempotencyKey(w http.ResponseWriter, request *http.Request) (string, bool) {
	if request == nil {
		returnInvalid(w)
		return "", false
	}
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		returnInvalid(w)
		return "", false
	}
	if _, err := idempotency.KeyHash(values[0]); err != nil {
		returnInvalid(w)
		return "", false
	}
	return values[0], true
}

func exactQuery(values url.Values, allowed ...string) bool {
	set := map[string]bool{}
	for _, key := range allowed {
		set[key] = true
	}
	for key, items := range values {
		if !set[key] || len(items) != 1 {
			return false
		}
	}
	return true
}

func requireExactQuery(w http.ResponseWriter, request *http.Request, allowed ...string) bool {
	if request == nil || request.URL == nil || request.URL.ForceQuery && request.URL.RawQuery == "" {
		returnInvalid(w)
		return false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err == nil && exactQuery(values, allowed...) {
		return true
	}
	returnInvalid(w)
	return false
}
func returnInvalid(w http.ResponseWriter) {
	httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		writeError(w, ErrInvariant)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeError(writer http.ResponseWriter, err error) {
	code, message := httperr.CodeInternal, "request failed"
	switch {
	case errors.Is(err, ErrInvalidRequest):
		code, message = httperr.CodeInvalidRequest, "invalid request"
	case errors.Is(err, ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "authentication required"
	case errors.Is(err, ErrForbidden):
		code, message = httperr.CodeForbidden, "forbidden"
	case errors.Is(err, ErrNotFound):
		code, message = httperr.CodeNotFound, "not found"
	case errors.Is(err, ErrConflict):
		code, message = httperr.CodeConflict, "conflict"
	case errors.Is(err, ErrMaintenance):
		code, message = httperr.CodeMaintenance, "maintenance mode"
	case errors.Is(err, ErrResourceLimit):
		code, message = httperr.CodeResourceLimitExceeded, "resource limit exceeded"
	case errors.Is(err, ErrServiceUnavailable), errors.Is(err, ErrClosed):
		code, message = httperr.CodeServiceUnavailable, "service unavailable"
	}
	httperr.WriteError(writer, httperr.New(code, message))
}
