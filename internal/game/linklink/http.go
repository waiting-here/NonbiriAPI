package linklink

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type httpAPI struct{ service *Service }

func RegisterRoutes(user resources.UserRouteRegistrar, continuation resources.ContinuationUserRouteRegistrar, service *Service) error {
	if user == nil || continuation == nil || service == nil {
		return errors.New("linklink: user registrars and service are required")
	}
	api := &httpAPI{service: service}
	if err := user.RegisterUserRoute(http.MethodPost, RouteSessions, api.start); err != nil {
		return fmt.Errorf("linklink: register start: %w", err)
	}
	routes := []struct {
		method, path string
		handler      resources.AuthenticatedContinuationHandler
	}{
		{http.MethodGet, RouteSession, api.read},
		{http.MethodPost, RouteMatches, api.match},
		{http.MethodPost, RouteAbandon, api.abandon},
		{http.MethodPost, RouteLease, api.lease},
	}
	for _, route := range routes {
		if err := continuation.RegisterContinuationUserRoute(route.method, route.path, route.handler); err != nil {
			return fmt.Errorf("linklink: register %s %s: %w", route.method, route.path, err)
		}
	}
	return nil
}

func (api *httpAPI) start(w http.ResponseWriter, request *http.Request, principal resources.UserPrincipal) {
	if !requireExactQuery(w, request) {
		return
	}
	key, ok := requireIdempotencyKey(w, request)
	if !ok {
		return
	}
	var body startBody
	if !decodeStrict(w, request, &body) {
		return
	}
	result, err := api.service.Start(request.Context(), StartInput{UserID: principal.UserID, Spec: body.Spec, IdempotencyKey: key})
	writeResult(w, result, err)
}

func (api *httpAPI) read(w http.ResponseWriter, request *http.Request, principal resources.ContinuationUserPrincipal) {
	if !noBody(w, request) || !requireExactQuery(w, request) {
		return
	}
	current, err := api.service.Read(request.Context(), ReadInput{UserID: principal.UserID, SessionBinding: principal.SessionBinding})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, current)
}

func (api *httpAPI) match(w http.ResponseWriter, request *http.Request, principal resources.ContinuationUserPrincipal) {
	if !requireExactQuery(w, request) {
		return
	}
	key, ok := requireIdempotencyKey(w, request)
	if !ok {
		return
	}
	var body matchBody
	if !decodeStrict(w, request, &body) {
		return
	}
	result, err := api.service.Match(request.Context(), MatchInput{
		UserID: principal.UserID, SessionBinding: principal.SessionBinding, SessionID: request.PathValue("id"),
		ExpectedRevision: body.ExpectedRevision, First: body.First, Second: body.Second, IdempotencyKey: key,
	})
	writeResult(w, result, err)
}

func (api *httpAPI) abandon(w http.ResponseWriter, request *http.Request, principal resources.ContinuationUserPrincipal) {
	if !requireExactQuery(w, request) {
		return
	}
	key, ok := requireIdempotencyKey(w, request)
	if !ok {
		return
	}
	var body abandonBody
	if !decodeStrict(w, request, &body) {
		return
	}
	summary, err := api.service.Abandon(request.Context(), AbandonInput{
		UserID: principal.UserID, SessionBinding: principal.SessionBinding, SessionID: request.PathValue("id"),
		ExpectedRevision: body.ExpectedRevision, Confirmation: body.Confirmation, IdempotencyKey: key,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (api *httpAPI) lease(w http.ResponseWriter, request *http.Request, principal resources.ContinuationUserPrincipal) {
	if !requireExactQuery(w, request) {
		return
	}
	var body struct {
		LeaseID string `json:"lease_id"`
	}
	if !decodeStrict(w, request, &body) {
		return
	}
	result, err := api.service.RenewLease(request.Context(), LeaseInput{
		UserID: principal.UserID, SessionBinding: principal.SessionBinding, SessionID: request.PathValue("id"), LeaseID: body.LeaseID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeResult(w http.ResponseWriter, result Result, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	if !result.valid() {
		writeError(w, ErrInvariant)
		return
	}
	if result.State != nil {
		writeJSON(w, result.HTTPStatus, result.State)
	} else {
		writeJSON(w, result.HTTPStatus, result.Summary)
	}
}

func readBody(w http.ResponseWriter, request *http.Request) ([]byte, bool) {
	if request == nil || request.Body == nil {
		returnInvalid(w)
		return nil, false
	}
	limited := http.MaxBytesReader(w, request.Body, idempotency.MaxControlBodyBytes)
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
	if len(body) == 0 || validateStrictJSON(body) != nil {
		returnInvalid(w)
		return nil, false
	}
	return body, true
}

func decodeStrict[T any](w http.ResponseWriter, request *http.Request, destination *T) bool {
	body, ok := readBody(w, request)
	if !ok {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		returnInvalid(w)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		returnInvalid(w)
		return false
	}
	return true
}

func validateStrictJSON(body []byte) error {
	if !utf8.Valid(body) {
		return errors.New("invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	return walkJSON(decoder, true)
}

func walkJSON(decoder *json.Decoder, root bool) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		if token == nil || root {
			return errors.New("object required")
		}
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return errors.New("duplicate key")
			}
			seen[key] = true
			if err := walkJSON(decoder, false); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := walkJSON(decoder, false); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return errors.New("invalid delimiter")
	}
}

func noBody(w http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1))
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

func requireExactQuery(w http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.URL == nil || request.URL.ForceQuery && request.URL.RawQuery == "" {
		returnInvalid(w)
		return false
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(values) != 0 {
		returnInvalid(w)
		return false
	}
	return true
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

func writeError(w http.ResponseWriter, err error) {
	code, message := httperr.CodeInternal, "request failed"
	switch {
	case errors.Is(err, ErrInvalidRequest):
		code, message = httperr.CodeInvalidRequest, "invalid request"
	case errors.Is(err, ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "authentication required"
	case errors.Is(err, ErrForbidden):
		code, message = httperr.CodeForbidden, "forbidden"
	case errors.Is(err, ErrNotFound):
		code, message = httperr.CodeNotFound, "resource not found"
	case errors.Is(err, ErrConflict):
		code, message = httperr.CodeConflict, "state conflict"
	case errors.Is(err, ErrRateLimited):
		code, message = httperr.CodeRateLimited, "game start rate limit exceeded"
	case errors.Is(err, ErrFeatureDisabled):
		code, message = httperr.CodeFeatureDisabled, "game is disabled"
	case errors.Is(err, ErrInsufficientCredits):
		code, message = httperr.CodeInsufficientCredits, "insufficient credits"
	case errors.Is(err, ErrMaintenance):
		code, message = httperr.CodeMaintenance, "maintenance mode"
	case errors.Is(err, ErrServiceUnavailable):
		code, message = httperr.CodeServiceUnavailable, "service unavailable"
	}
	httperr.WriteError(w, httperr.New(code, message))
}
