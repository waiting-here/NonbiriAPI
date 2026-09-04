package adminapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const (
	RouteAdminBootstrapConfig   = "/admin/api/config"
	RouteAdminSiteConfig        = "/admin/api/site-config"
	RouteAdminSiteConfigCatalog = "/admin/api/site-config/catalog"
	RouteAdminSiteConfigKey     = "/admin/api/site-config/{key}"
)

type SiteConfigAdminPrincipal struct {
	UserID int64
}

type SiteConfigAuthorizedAdminHandler func(http.ResponseWriter, *http.Request, SiteConfigAdminPrincipal)

// SiteConfigRouteRegistrar is the entry authorization and admin-station
// seam. Root composition adapts the central authentication runtime and passes
// only a live administrator identity to these handlers.
type SiteConfigRouteRegistrar interface {
	RegisterAdminRoute(method, pattern string, handler SiteConfigAuthorizedAdminHandler) error
}

// SiteConfigRuntime owns only the four frozen bootstrap/site-config routes.
// It has no route mux or process-global state and can be composed independently
// from other administrator route owners.
type SiteConfigRuntime struct {
	repository *SiteConfigRepository
}

func NewSiteConfigRuntime(repository *SiteConfigRepository) (*SiteConfigRuntime, error) {
	if repository == nil {
		return nil, errors.New("admin site configuration: repository is required")
	}
	return &SiteConfigRuntime{repository: repository}, nil
}

func RegisterSiteConfigRoutes(registrar SiteConfigRouteRegistrar, runtime *SiteConfigRuntime) error {
	if nilSiteConfigInterface(registrar) || runtime == nil || runtime.repository == nil {
		return errors.New("admin site configuration: registrar and runtime are required")
	}
	routes := []struct {
		method, pattern string
		handler         SiteConfigAuthorizedAdminHandler
	}{
		{http.MethodGet, RouteAdminBootstrapConfig, runtime.getBootstrap},
		{http.MethodGet, RouteAdminSiteConfig, runtime.getSiteConfig},
		{http.MethodGet, RouteAdminSiteConfigCatalog, runtime.getCatalog},
		{http.MethodPatch, RouteAdminSiteConfigKey, runtime.patchSiteConfig},
	}
	for _, route := range routes {
		if err := registrar.RegisterAdminRoute(route.method, route.pattern, route.handler); err != nil {
			return fmt.Errorf("admin site configuration: register %s %s: %w", route.method, route.pattern, err)
		}
	}
	return nil
}

func (runtime *SiteConfigRuntime) getBootstrap(writer http.ResponseWriter, request *http.Request, principal SiteConfigAdminPrincipal) {
	if !requireSiteConfigMethod(writer, request, http.MethodGet) || !requireSiteConfigReadRequest(writer, request) {
		return
	}
	value, err := runtime.repository.ReadAdminPublicConfig(request.Context(), principal.UserID)
	if err != nil {
		writeSiteConfigError(writer, err)
		return
	}
	writeSiteConfigJSON(writer, http.StatusOK, value)
}

func (runtime *SiteConfigRuntime) getSiteConfig(writer http.ResponseWriter, request *http.Request, principal SiteConfigAdminPrincipal) {
	if !requireSiteConfigMethod(writer, request, http.MethodGet) || !requireSiteConfigReadRequest(writer, request) {
		return
	}
	value, err := runtime.repository.ReadSiteConfig(request.Context(), principal.UserID)
	if err != nil {
		writeSiteConfigError(writer, err)
		return
	}
	writeSiteConfigJSON(writer, http.StatusOK, value)
}

func (runtime *SiteConfigRuntime) getCatalog(writer http.ResponseWriter, request *http.Request, principal SiteConfigAdminPrincipal) {
	if !requireSiteConfigMethod(writer, request, http.MethodGet) || !requireSiteConfigReadRequest(writer, request) {
		return
	}
	value, err := runtime.repository.ReadSiteConfigCatalog(request.Context(), principal.UserID)
	if err != nil {
		writeSiteConfigError(writer, err)
		return
	}
	writeSiteConfigJSON(writer, http.StatusOK, value)
}

func (runtime *SiteConfigRuntime) patchSiteConfig(writer http.ResponseWriter, request *http.Request, principal SiteConfigAdminPrincipal) {
	if !requireSiteConfigMethod(writer, request, http.MethodPatch) || !requireSiteConfigNoQuery(writer, request) {
		return
	}
	key, ok := requireSiteConfigIdempotencyKey(writer, request)
	if !ok {
		return
	}
	rawValue, ok := decodeSiteConfigPatch(writer, request)
	if !ok {
		return
	}
	result, err := runtime.repository.PatchSiteConfig(request.Context(), SiteConfigPatchInput{
		AdminID: principal.UserID, Key: request.PathValue("key"), RawValue: rawValue, IdempotencyKey: key,
	})
	if err != nil {
		writeSiteConfigError(writer, err)
		return
	}
	writeSiteConfigMutation(writer, result)
}

func requireSiteConfigMethod(writer http.ResponseWriter, request *http.Request, method string) bool {
	if request == nil {
		writeSiteConfigError(writer, ErrSiteConfigInvalid)
		return false
	}
	if request.Method != method {
		writer.Header().Set("Allow", method)
		httperr.WriteError(writer, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
		return false
	}
	return true
}

func requireSiteConfigReadRequest(writer http.ResponseWriter, request *http.Request) bool {
	return requireSiteConfigNoQuery(writer, request) && requireSiteConfigNoBody(writer, request)
}

func requireSiteConfigNoQuery(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.URL == nil || request.URL.RawQuery != "" || request.URL.ForceQuery {
		writeSiteConfigError(writer, ErrSiteConfigInvalid)
		return false
	}
	return true
}

func requireSiteConfigNoBody(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		writeSiteConfigError(writer, ErrSiteConfigInvalid)
		return false
	}
	if request.Body == nil || request.Body == http.NoBody {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil || len(body) != 0 {
		writeSiteConfigError(writer, ErrSiteConfigInvalid)
		return false
	}
	return true
}

func requireSiteConfigIdempotencyKey(writer http.ResponseWriter, request *http.Request) (string, bool) {
	if request == nil {
		writeSiteConfigError(writer, ErrSiteConfigInvalid)
		return "", false
	}
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		writeSiteConfigError(writer, ErrSiteConfigInvalid)
		return "", false
	}
	if _, err := idempotency.KeyHash(values[0]); err != nil {
		writeSiteConfigError(writer, ErrSiteConfigInvalid)
		return "", false
	}
	return values[0], true
}

func decodeSiteConfigPatch(writer http.ResponseWriter, request *http.Request) (json.RawMessage, bool) {
	if request == nil || request.Body == nil {
		writeSiteConfigError(writer, ErrSiteConfigInvalid)
		return nil, false
	}
	reader := http.MaxBytesReader(writer, request.Body, idempotency.MaxControlBodyBytes)
	body, err := io.ReadAll(reader)
	if err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			httperr.WriteError(writer, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeSiteConfigError(writer, ErrSiteConfigInvalid)
		}
		return nil, false
	}
	if len(body) == 0 || strictjson.ValidateObject(body) != nil {
		writeSiteConfigError(writer, ErrSiteConfigInvalid)
		return nil, false
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || len(object) != 1 {
		writeSiteConfigError(writer, ErrSiteConfigInvalid)
		return nil, false
	}
	value, ok := object["value"]
	if !ok || len(value) == 0 {
		writeSiteConfigError(writer, ErrSiteConfigInvalid)
		return nil, false
	}
	return append(json.RawMessage(nil), value...), true
}

func writeSiteConfigMutation(writer http.ResponseWriter, result SiteConfigMutationResult) {
	if result.Status != http.StatusOK || len(result.Body) == 0 {
		writeSiteConfigError(writer, ErrSiteConfigInvariant)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(result.Status)
	_, _ = writer.Write(result.Body)
}

func writeSiteConfigJSON(writer http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		writeSiteConfigError(writer, ErrSiteConfigInvariant)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

func writeSiteConfigError(writer http.ResponseWriter, err error) {
	code, message := httperr.CodeInternal, "internal error"
	switch {
	case errors.Is(err, ErrSiteConfigInvalid):
		code, message = httperr.CodeInvalidRequest, "invalid request"
	case errors.Is(err, ErrSiteConfigUnauthorized):
		code, message = httperr.CodeUnauthorized, "authentication required"
	case errors.Is(err, ErrSiteConfigForbidden):
		code, message = httperr.CodeForbidden, "access forbidden"
	case errors.Is(err, ErrSiteConfigNotFound):
		code, message = httperr.CodeNotFound, "configuration key not found"
	case errors.Is(err, ErrSiteConfigConflict):
		code, message = httperr.CodeConflict, "configuration conflict"
	case errors.Is(err, ErrSiteConfigUnavailable):
		code, message = httperr.CodeServiceUnavailable, "service unavailable"
	}
	httperr.WriteError(writer, httperr.New(code, message))
}

func nilSiteConfigInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
