package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const (
	accountExportRoute = "/api/account/export"
	accountDeleteRoute = "/api/account/delete"
)

type UserPrincipal struct {
	UserID int64
}

type AdminPrincipal struct {
	UserID int64
}

type AuthorizedUserHandler func(http.ResponseWriter, *http.Request, UserPrincipal)
type AuthorizedAdminHandler func(http.ResponseWriter, *http.Request, AdminPrincipal)

type UserRouteRegistrar interface {
	RegisterUserRoute(string, string, AuthorizedUserHandler) error
}

type AdminRouteRegistrar interface {
	RegisterAdminRoute(string, string, AuthorizedAdminHandler) error
}

type lifecycleHTTP struct {
	coordinator *Coordinator
}

func RegisterRoutes(users UserRouteRegistrar, admins AdminRouteRegistrar, coordinator *Coordinator) error {
	if users == nil || admins == nil || coordinator == nil {
		return ErrInvalid
	}
	api := &lifecycleHTTP{coordinator: coordinator}
	userRoutes := []struct {
		method, pattern string
		handler         AuthorizedUserHandler
	}{
		{http.MethodPost, accountExportRoute, api.exportAccount},
		{http.MethodPost, accountDeleteRoute, api.deleteAccount},
	}
	for _, route := range userRoutes {
		if err := users.RegisterUserRoute(route.method, route.pattern, route.handler); err != nil {
			return fmt.Errorf("lifecycle: register %s %s: %w", route.method, route.pattern, err)
		}
	}
	adminRoutes := []struct {
		method, pattern string
		handler         AuthorizedAdminHandler
	}{
		{http.MethodGet, legalHoldListRoute, api.listLegalHolds},
		{http.MethodGet, legalHoldDetailRoute, api.getLegalHold},
		{http.MethodPost, legalHoldListRoute, api.createLegalHold},
		{http.MethodPost, legalHoldReleaseRoute, api.releaseLegalHold},
	}
	for _, route := range adminRoutes {
		if err := admins.RegisterAdminRoute(route.method, route.pattern, route.handler); err != nil {
			return fmt.Errorf("lifecycle: register %s %s: %w", route.method, route.pattern, err)
		}
	}
	return nil
}

func (api *lifecycleHTTP) exportAccount(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireLifecycleEmptyQuery(writer, request) || !requireLifecycleNoBody(writer, request) {
		return
	}
	payload, err := api.coordinator.Export(request.Context(), principal.UserID, api.decisionNow())
	if err != nil {
		writeLifecycleError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Content-Disposition", `attachment; filename="nonbiriapi-account-export-v4.json"`)
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(payload)
}

type accountDeleteWire struct {
	Confirm string `json:"confirm"`
}

func (api *lifecycleHTTP) deleteAccount(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireLifecycleEmptyQuery(writer, request) {
		return
	}
	var body accountDeleteWire
	if !decodeLifecycleObject(writer, request, &body) {
		return
	}
	if body.Confirm != "DELETE" {
		writeLifecycleError(writer, ErrInvalid)
		return
	}
	if err := api.coordinator.DeleteAccount(request.Context(), principal.UserID, api.decisionNow()); err != nil {
		writeLifecycleError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

func (api *lifecycleHTTP) listLegalHolds(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireLifecycleNoBody(writer, request) {
		return
	}
	values, ok := lifecycleQuery(writer, request, "state", "object_kind", "cursor", "limit")
	if !ok {
		return
	}
	filter := LegalHoldListFilter{AdminID: principal.UserID, DecisionNow: api.decisionNow()}
	if entries, present := values["state"]; present {
		if entries[0] == "" {
			writeLifecycleError(writer, ErrInvalid)
			return
		}
		filter.State = entries[0]
	}
	if entries, present := values["object_kind"]; present {
		if entries[0] == "" {
			writeLifecycleError(writer, ErrInvalid)
			return
		}
		filter.Kind = HeldObjectKind(entries[0])
	}
	if entries, present := values["cursor"]; present {
		if entries[0] == "" || len(entries[0]) > 4096 {
			writeLifecycleError(writer, ErrInvalid)
			return
		}
		filter.Cursor = entries[0]
	}
	if entries, present := values["limit"]; present {
		limit, err := strconv.Atoi(entries[0])
		if err != nil || limit < 1 || limit > legalHoldMaximumLimit || strconv.Itoa(limit) != entries[0] {
			writeLifecycleError(writer, ErrInvalid)
			return
		}
		filter.Limit = limit
	}
	page, err := api.coordinator.ListLegalHolds(request.Context(), filter)
	if err != nil {
		writeLifecycleError(writer, err)
		return
	}
	writeLifecycleJSON(writer, http.StatusOK, page)
}

func (api *lifecycleHTTP) getLegalHold(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireLifecycleEmptyQuery(writer, request) || !requireLifecycleNoBody(writer, request) {
		return
	}
	detail, err := api.coordinator.GetLegalHold(
		request.Context(), principal.UserID, request.PathValue("id"), api.decisionNow(),
	)
	if err != nil {
		writeLifecycleError(writer, err)
		return
	}
	writeLifecycleJSON(writer, http.StatusOK, detail)
}

type legalHoldCreateWire struct {
	ObjectKind   HeldObjectKind `json:"object_kind"`
	ObjectRef    string         `json:"object_ref"`
	Basis        string         `json:"basis"`
	ExpiresAt    int64          `json:"expires_at"`
	Confirmation bool           `json:"confirmation"`
}

func (api *lifecycleHTTP) createLegalHold(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireLifecycleEmptyQuery(writer, request) {
		return
	}
	var body legalHoldCreateWire
	if !decodeLifecycleObject(writer, request, &body) {
		return
	}
	key, ok := lifecycleIdempotencyKey(writer, request)
	if !ok {
		return
	}
	result, err := api.coordinator.CreateLegalHold(request.Context(), LegalHoldCreate{
		AdminID: principal.UserID, ObjectKind: body.ObjectKind, ObjectRef: body.ObjectRef,
		Basis: body.Basis, ExpiresAt: body.ExpiresAt, Confirmation: body.Confirmation,
		IdempotencyKey: key, DecisionNow: api.decisionNow(),
	})
	writeLifecycleMutation(writer, result.Status, result.Body, err)
}

type legalHoldReleaseWire struct {
	ExpectedRevision string `json:"expected_revision"`
	Reason           string `json:"reason"`
	Confirmation     bool   `json:"confirmation"`
}

func (api *lifecycleHTTP) releaseLegalHold(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireLifecycleEmptyQuery(writer, request) {
		return
	}
	var body legalHoldReleaseWire
	if !decodeLifecycleObject(writer, request, &body) {
		return
	}
	key, ok := lifecycleIdempotencyKey(writer, request)
	if !ok {
		return
	}
	result, err := api.coordinator.ReleaseLegalHold(request.Context(), LegalHoldRelease{
		AdminID: principal.UserID, HoldID: request.PathValue("id"),
		ExpectedRevision: body.ExpectedRevision, Reason: body.Reason,
		Confirmation: body.Confirmation, IdempotencyKey: key, DecisionNow: api.decisionNow(),
	})
	writeLifecycleMutation(writer, result.Status, result.Body, err)
}

func (api *lifecycleHTTP) decisionNow() int64 {
	return api.coordinator.now().Unix()
}

func decodeLifecycleObject(writer http.ResponseWriter, request *http.Request, destination any) bool {
	if request == nil || request.Body == nil || destination == nil || !lifecycleJSONContentType(request.Header.Get("Content-Type")) {
		writeLifecycleError(writer, ErrInvalid)
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, idempotency.MaxControlBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeLifecycleError(writer, ErrTooLarge)
		} else {
			writeLifecycleError(writer, ErrInvalid)
		}
		return false
	}
	if len(body) == 0 || strictjson.ValidateObject(body) != nil {
		writeLifecycleError(writer, ErrInvalid)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeLifecycleError(writer, ErrInvalid)
		return false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		writeLifecycleError(writer, ErrInvalid)
		return false
	}
	return true
}

func lifecycleJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

func requireLifecycleNoBody(writer http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil || len(body) != 0 {
		writeLifecycleError(writer, ErrInvalid)
		return false
	}
	return true
}

func requireLifecycleEmptyQuery(writer http.ResponseWriter, request *http.Request) bool {
	values, ok := lifecycleQuery(writer, request)
	return ok && len(values) == 0
}

func lifecycleQuery(writer http.ResponseWriter, request *http.Request, allowed ...string) (url.Values, bool) {
	if request == nil || request.URL == nil || request.URL.ForceQuery {
		writeLifecycleError(writer, ErrInvalid)
		return nil, false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeLifecycleError(writer, ErrInvalid)
		return nil, false
	}
	allow := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allow[key] = struct{}{}
	}
	for key, entries := range values {
		if _, present := allow[key]; !present || len(entries) != 1 {
			writeLifecycleError(writer, ErrInvalid)
			return nil, false
		}
	}
	return values, true
}

func lifecycleIdempotencyKey(writer http.ResponseWriter, request *http.Request) (string, bool) {
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		writeLifecycleError(writer, ErrInvalid)
		return "", false
	}
	if _, err := idempotency.KeyHash(values[0]); err != nil {
		writeLifecycleError(writer, ErrInvalid)
		return "", false
	}
	return values[0], true
}

func writeLifecycleMutation(writer http.ResponseWriter, status int, body []byte, err error) {
	if err != nil {
		writeLifecycleError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

func writeLifecycleJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	httperr.WriteJSON(writer, status, value)
}

func writeLifecycleError(writer http.ResponseWriter, err error) {
	if code := authz.StableCode(err); code != "" {
		httperr.WriteError(writer, httperr.New(code, lifecycleAuthMessage(code)))
		return
	}
	switch {
	case errors.Is(err, ErrInvalid):
		httperr.WriteError(writer, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
	case errors.Is(err, ErrUnauthorized):
		httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
	case errors.Is(err, ErrForbidden):
		httperr.WriteError(writer, httperr.New(httperr.CodeForbidden, "access denied"))
	case errors.Is(err, ErrNotFound):
		httperr.WriteError(writer, httperr.New(httperr.CodeNotFound, "not found"))
	case errors.Is(err, ErrConflict):
		httperr.WriteError(writer, httperr.New(httperr.CodeConflict, "request conflicts with current state"))
	case errors.Is(err, ErrTooLarge):
		httperr.WriteError(writer, httperr.New(httperr.CodePayloadTooLarge, "payload is too large"))
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrClosed):
		httperr.WriteError(writer, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
	default:
		httperr.WriteError(writer, httperr.New(httperr.CodeInternal, "internal error"))
	}
}

func lifecycleAuthMessage(code string) string {
	switch code {
	case httperr.CodeUnauthorized:
		return "authentication required"
	case httperr.CodeForbidden:
		return "access denied"
	case httperr.CodeElevationRequired:
		return "fresh authorization required"
	case httperr.CodeNotFound:
		return "not found"
	default:
		return "authorization failed"
	}
}
