package debug

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const maxControlBody = 64 * 1024

type UserPrincipal struct {
	UserID         int64
	SessionBinding string
}

type AuthorizedUserHandler func(http.ResponseWriter, *http.Request, UserPrincipal)

// UserRouteRegistrar is intentionally narrower than auth.Runtime. The root
// adapter must supply irreversible session binding material on every request;
// Debug never accepts the raw browser cookie.
type UserRouteRegistrar interface {
	RegisterDebugUserRoute(method, pattern string, handler AuthorizedUserHandler) error
}

type HTTPAPI struct {
	hub       *Hub
	mutations *MutationRepository
}

func NewHTTPAPI(hub *Hub, mutations *MutationRepository) (*HTTPAPI, error) {
	if hub == nil || mutations == nil {
		return nil, ErrInvalid
	}
	return &HTTPAPI{hub: hub, mutations: mutations}, nil
}

func RegisterRoutes(registrar UserRouteRegistrar, hub *Hub, mutations *MutationRepository) error {
	if registrar == nil {
		return ErrInvalid
	}
	api, err := NewHTTPAPI(hub, mutations)
	if err != nil {
		return err
	}
	routes := []struct {
		method  string
		pattern string
		handler AuthorizedUserHandler
	}{
		{http.MethodGet, "/api/debug/session", api.getSession},
		{http.MethodPost, "/api/debug/session", api.createSession},
		{http.MethodPut, "/api/debug/session/mode", api.changeMode},
		{http.MethodPost, "/api/debug/session/stop", api.stopSession},
		{http.MethodPost, "/api/debug/session/replace", api.replaceSession},
		{http.MethodGet, "/api/debug/events", api.events},
	}
	for _, route := range routes {
		if err := registrar.RegisterDebugUserRoute(route.method, route.pattern, route.handler); err != nil {
			return err
		}
	}
	return nil
}

func (api *HTTPAPI) getSession(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !api.authorizePrincipal(writer, request, principal) || !requireNoQuery(writer, request) ||
		!requireNoRequestBody(writer, request) {
		return
	}
	api.reconcileBinding(principal)
	metadata, err := api.hub.Metadata(principal.UserID)
	if err != nil {
		api.writeDomainError(writer, err)
		return
	}
	if err := writeJSONValue(writer, http.StatusOK, metadata); err != nil {
		api.writeDomainError(writer, err)
	}
}

func (api *HTTPAPI) createSession(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !api.authorizePrincipal(writer, request, principal) || !requireNoQuery(writer, request) {
		return
	}
	_, err := readNoBody(request)
	if err != nil {
		writeInvalid(writer, err)
		return
	}
	api.runIdempotent(writer, request, principal, "/api/debug/session", nil, func() (int, []byte, error) {
		api.reconcileBinding(principal)
		metadata, created, err := api.hub.Start(principal.UserID, principal.SessionBinding)
		if err != nil {
			return 0, nil, err
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		encoded, err := marshalResponse(metadata)
		return status, encoded, err
	})
}

type modeMutation struct {
	Mode             Mode   `json:"mode"`
	ExpectedRevision string `json:"expected_revision"`
	LiveConfirmation bool   `json:"live_confirmation"`
}

func (api *HTTPAPI) changeMode(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !api.authorizePrincipal(writer, request, principal) || !requireNoQuery(writer, request) {
		return
	}
	body, err := readStrictBody(request, maxControlBody)
	if err != nil {
		writeInvalid(writer, err)
		return
	}
	var mutation modeMutation
	if err := requireObjectFields(body, "mode", "expected_revision", "live_confirmation"); err != nil {
		writeInvalid(writer, err)
		return
	}
	if err := decodeExact(body, &mutation); err != nil {
		writeInvalid(writer, err)
		return
	}
	canonical, err := idempotency.CanonicalJSON(mutation)
	if err != nil {
		writeInvalid(writer, err)
		return
	}
	api.runIdempotent(writer, request, principal, "/api/debug/session/mode", canonical, func() (int, []byte, error) {
		if err := api.requireBinding(principal); err != nil {
			return 0, nil, err
		}
		metadata, err := api.hub.ChangeMode(principal.UserID, mutation.ExpectedRevision, mutation.Mode, mutation.LiveConfirmation)
		if err != nil {
			return 0, nil, err
		}
		encoded, err := marshalResponse(metadata)
		return http.StatusOK, encoded, err
	})
}

type stopMutation struct {
	ExpectedRevision string `json:"expected_revision"`
	ConfirmInflight  bool   `json:"confirm_inflight"`
}

func (api *HTTPAPI) stopSession(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !api.authorizePrincipal(writer, request, principal) || !requireNoQuery(writer, request) {
		return
	}
	body, err := readStrictBody(request, maxControlBody)
	if err != nil {
		writeInvalid(writer, err)
		return
	}
	var mutation stopMutation
	if err := requireObjectFields(body, "expected_revision", "confirm_inflight"); err != nil {
		writeInvalid(writer, err)
		return
	}
	if err := decodeExact(body, &mutation); err != nil {
		writeInvalid(writer, err)
		return
	}
	canonical, err := idempotency.CanonicalJSON(mutation)
	if err != nil {
		writeInvalid(writer, err)
		return
	}
	api.runIdempotent(writer, request, principal, "/api/debug/session/stop", canonical, func() (int, []byte, error) {
		if err := api.requireBinding(principal); err != nil {
			return 0, nil, err
		}
		if err := api.hub.Stop(principal.UserID, mutation.ExpectedRevision, mutation.ConfirmInflight); err != nil {
			return 0, nil, err
		}
		return http.StatusNoContent, nil, nil
	})
}

func (api *HTTPAPI) replaceSession(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !api.authorizePrincipal(writer, request, principal) || !requireNoQuery(writer, request) {
		return
	}
	body, err := readStrictBody(request, maxControlBody)
	if err != nil {
		writeInvalid(writer, err)
		return
	}
	var mutation stopMutation
	if err := requireObjectFields(body, "expected_revision", "confirm_inflight"); err != nil {
		writeInvalid(writer, err)
		return
	}
	if err := decodeExact(body, &mutation); err != nil {
		writeInvalid(writer, err)
		return
	}
	canonical, err := idempotency.CanonicalJSON(mutation)
	if err != nil {
		writeInvalid(writer, err)
		return
	}
	api.runIdempotent(writer, request, principal, "/api/debug/session/replace", canonical, func() (int, []byte, error) {
		if err := api.requireBinding(principal); err != nil {
			return 0, nil, err
		}
		metadata, err := api.hub.Replace(principal.UserID, principal.SessionBinding, mutation.ExpectedRevision, mutation.ConfirmInflight)
		if err != nil {
			return 0, nil, err
		}
		encoded, err := marshalResponse(metadata)
		return http.StatusCreated, encoded, err
	})
}

func (api *HTTPAPI) events(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !api.authorizePrincipal(writer, request, principal) || !requireNoQuery(writer, request) ||
		!requireNoRequestBody(writer, request) {
		return
	}
	if !acceptsEventStream(request.Header.Values("Accept")) {
		writeInvalid(writer, errors.New("Accept must include text/event-stream"))
		return
	}
	values := request.Header.Values("Last-Event-ID")
	if len(values) > 1 {
		writeInvalid(writer, errors.New("Last-Event-ID must be singular"))
		return
	}
	lastEventID := ""
	if len(values) == 1 {
		lastEventID = values[0]
		if strings.TrimSpace(lastEventID) != lastEventID {
			writeInvalid(writer, errors.New("Last-Event-ID is not canonical"))
			return
		}
	}
	if err := api.requireBinding(principal); err != nil {
		api.writeDomainError(writer, err)
		return
	}
	subscription, err := api.hub.Subscribe(request.Context(), principal.UserID, principal.SessionBinding, lastEventID)
	if err != nil {
		api.writeDomainError(writer, err)
		return
	}
	_ = subscription.Stream(request.Context(), writer)
}

func acceptsEventStream(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			media := strings.TrimSpace(strings.SplitN(token, ";", 2)[0])
			if strings.EqualFold(media, "text/event-stream") {
				return true
			}
		}
	}
	return false
}

func (api *HTTPAPI) authorizePrincipal(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) bool {
	if principal.UserID <= 0 || principal.SessionBinding == "" || request == nil {
		httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return false
	}
	if api.hub.config.verifier == nil {
		return true
	}
	state, err := api.hub.config.verifier.VerifyDebugIdentity(request.Context(), principal.UserID, principal.SessionBinding)
	if err != nil || state == IdentityUncertain {
		httperr.WriteError(writer, httperr.New(httperr.CodeServiceUnavailable, "debug identity could not be verified"))
		return false
	}
	switch state {
	case IdentityActive:
		return true
	case IdentityBanned:
		_ = api.hub.TerminateUser(principal.UserID, EndAccountBanned)
		httperr.WriteError(writer, httperr.New(httperr.CodeForbidden, "account is unavailable"))
	case IdentityDeleted:
		_ = api.hub.TerminateUser(principal.UserID, EndAccountDeleted)
		httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
	case IdentityRevoked:
		_ = api.hub.TerminateUser(principal.UserID, EndAuthRevoked)
		httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
	default:
		httperr.WriteError(writer, httperr.New(httperr.CodeUnauthorized, "authentication required"))
	}
	return false
}

func (api *HTTPAPI) reconcileBinding(principal UserPrincipal) {
	api.hub.mu.Lock()
	defer api.hub.mu.Unlock()
	current := api.hub.activeByUser[principal.UserID]
	if current != nil && !current.ended && current.identityBinding != principal.SessionBinding {
		api.hub.endSessionLocked(current, EndAuthRevoked)
	}
}

func (api *HTTPAPI) requireBinding(principal UserPrincipal) error {
	api.hub.mu.Lock()
	defer api.hub.mu.Unlock()
	if api.hub.closed {
		return ErrClosed
	}
	current := api.hub.activeByUser[principal.UserID]
	if current == nil || current.ended {
		return ErrNoActiveSession
	}
	if current.identityBinding != principal.SessionBinding {
		api.hub.endSessionLocked(current, EndAuthRevoked)
		return ErrNoActiveSession
	}
	return nil
}

func (api *HTTPAPI) runIdempotent(
	writer http.ResponseWriter,
	request *http.Request,
	principal UserPrincipal,
	route string,
	canonicalBody []byte,
	operation func() (int, []byte, error),
) {
	key, err := singularIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		writeInvalid(writer, err)
		return
	}
	status, response, _, err := api.mutations.Execute(request.Context(), principal.UserID, resources.ControlMutation{
		IdempotencyKey: key, Method: request.Method, Route: route,
		CanonicalBody: append([]byte(nil), canonicalBody...),
	}, operation)
	if err != nil {
		api.writeDomainError(writer, err)
		return
	}
	writeJSONBytes(writer, status, response)
}

func singularIdempotencyKey(values []string) (string, error) {
	if len(values) != 1 {
		return "", errors.New("Idempotency-Key is required exactly once")
	}
	value := values[0]
	if len(value) < 22 || len(value) > 128 {
		return "", errors.New("Idempotency-Key length is invalid")
	}
	for i := range value {
		c := value[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return "", errors.New("Idempotency-Key is not URL-safe ASCII")
		}
	}
	return value, nil
}

func readNoBody(request *http.Request) ([]byte, error) {
	if request == nil || request.Body == nil {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, 2))
	if err != nil {
		return nil, err
	}
	if len(data) != 0 {
		return nil, errors.New("request body must be empty")
	}
	return nil, nil
}

func noRequestBody(request *http.Request) bool {
	if request == nil {
		return false
	}
	if request.Body == nil || request.Body == http.NoBody {
		return true
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, 1))
	return err == nil && len(data) == 0
}

func readStrictBody(request *http.Request, limit int64) ([]byte, error) {
	if request == nil || request.Body == nil {
		return nil, errors.New("request body is required")
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("request body exceeds %d bytes", limit)
	}
	if err := strictjson.ValidateObject(data); err != nil {
		return nil, err
	}
	return data, nil
}

func decodeExact(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}

func requireObjectFields(data []byte, names ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range names {
		value, exists := fields[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("field %q is required", name)
		}
	}
	return nil
}

func noQuery(request *http.Request) bool {
	if request == nil || request.URL == nil || request.URL.RawQuery != "" {
		return false
	}
	return true
}

func requireNoQuery(writer http.ResponseWriter, request *http.Request) bool {
	if noQuery(request) {
		return true
	}
	writeInvalid(writer, errors.New("query is not allowed"))
	return false
}

func requireNoRequestBody(writer http.ResponseWriter, request *http.Request) bool {
	if noRequestBody(request) {
		return true
	}
	writeInvalid(writer, errors.New("request body must be empty"))
	return false
}

func marshalResponse(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalid
	}
	return append(encoded, '\n'), nil
}

func writeJSONValue(writer http.ResponseWriter, status int, value any) error {
	body, err := marshalResponse(value)
	if err != nil {
		return err
	}
	writeJSONBytes(writer, status, body)
	return nil
}

func writeJSONBytes(writer http.ResponseWriter, status int, body []byte) {
	writer.Header().Set("Cache-Control", "no-store")
	if len(body) > 0 {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	writer.WriteHeader(status)
	if len(body) > 0 {
		_, _ = writer.Write(body)
	}
}

func writeInvalid(writer http.ResponseWriter, err error) {
	message := "invalid request"
	if err != nil && strings.Contains(err.Error(), "exceeds") {
		httperr.WriteError(writer, httperr.New(httperr.CodePayloadTooLarge, message))
		return
	}
	httperr.WriteError(writer, httperr.New(httperr.CodeInvalidRequest, message))
}

func (api *HTTPAPI) writeDomainError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalid):
		writeInvalid(writer, err)
	case errors.Is(err, ErrNoActiveSession):
		httperr.WriteError(writer, httperr.New(httperr.CodeNotFound, "debug session was not found"))
	case errors.Is(err, ErrConflict), errors.Is(err, ErrTraceTerminal):
		httperr.WriteError(writer, httperr.New(httperr.CodeConflict, "debug state changed"))
	case errors.Is(err, ErrCapacity):
		httperr.WriteError(writer, httperr.New(httperr.CodeResourceLimitExceeded, "debug capacity is exhausted"))
	case errors.Is(err, ErrClosed):
		httperr.WriteError(writer, httperr.New(httperr.CodeServiceUnavailable, "debug is unavailable"))
	default:
		httperr.WriteError(writer, httperr.New(httperr.CodeInternal, "debug operation failed"))
	}
}
