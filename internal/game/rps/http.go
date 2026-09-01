package rps

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type httpAPI struct{ service *Service }

func RegisterRoutes(user resources.UserRouteRegistrar, continuation resources.ContinuationUserRouteRegistrar, service *Service) error {
	if user == nil || continuation == nil || service == nil {
		return errors.New("rps: user registrars and service are required")
	}
	api := &httpAPI{service: service}
	userRoutes := []struct {
		method, path string
		handler      resources.AuthorizedUserHandler
	}{
		{http.MethodPost, RouteTutorial, api.tutorial},
		{http.MethodPost, RouteQueue, api.enqueue},
		{http.MethodDelete, RouteQueueItem, api.cancel},
		{http.MethodGet, RouteLeaderboard, api.leaderboard},
	}
	for _, route := range userRoutes {
		if err := user.RegisterUserRoute(route.method, route.path, route.handler); err != nil {
			return fmt.Errorf("rps: register %s %s: %w", route.method, route.path, err)
		}
	}
	continuationRoutes := []struct {
		method, path string
		handler      resources.AuthenticatedContinuationHandler
	}{
		{http.MethodGet, RouteState, api.read},
		{http.MethodPost, RouteActions, api.action},
		{http.MethodPost, RouteLease, api.lease},
		{http.MethodPost, RoutePendingACK, api.ack},
	}
	for _, route := range continuationRoutes {
		if err := continuation.RegisterContinuationUserRoute(route.method, route.path, route.handler); err != nil {
			return fmt.Errorf("rps: register %s %s: %w", route.method, route.path, err)
		}
	}
	return nil
}

func (api *httpAPI) tutorial(w http.ResponseWriter, request *http.Request, principal resources.UserPrincipal) {
	if !rpsNoBody(w, request) || !rpsNoQuery(w, request) {
		return
	}
	if err := api.service.MarkTutorialSeen(request.Context(), principal.UserID); err != nil {
		writeRPSError(w, err)
		return
	}
	writeRPSNoContent(w)
}

func (api *httpAPI) enqueue(w http.ResponseWriter, request *http.Request, principal resources.UserPrincipal) {
	if !rpsNoQuery(w, request) {
		return
	}
	key, ok := rpsIdempotencyKey(w, request)
	if !ok {
		return
	}
	var body struct {
		Mode                string `json:"mode"`
		DeviceToken         string `json:"device_token"`
		DeathmatchConfirmed *bool  `json:"deathmatch_confirmed"`
	}
	if !rpsDecodeStrict(w, request, &body) {
		return
	}
	if body.DeathmatchConfirmed == nil {
		rpsInvalid(w)
		return
	}
	address, err := netip.ParseAddr(httpmw.ClientIP(request))
	if err != nil {
		rpsInvalid(w)
		return
	}
	result, err := api.service.Enqueue(request.Context(), EnqueueInput{
		UserID: principal.UserID, Mode: body.Mode, DeviceToken: body.DeviceToken,
		CanonicalSourceIP: address.As16(), DeathmatchConfirmed: *body.DeathmatchConfirmed, IdempotencyKey: key,
	})
	if err != nil {
		writeRPSError(w, err)
		return
	}
	if !result.valid() {
		writeRPSError(w, ErrInvariant)
		return
	}
	writeRPSJSON(w, result.HTTPStatus, result.Queue)
}

func (api *httpAPI) cancel(w http.ResponseWriter, request *http.Request, principal resources.UserPrincipal) {
	if !rpsNoQuery(w, request) {
		return
	}
	key, ok := rpsIdempotencyKey(w, request)
	if !ok {
		return
	}
	var body cancelBody
	if !rpsDecodeStrict(w, request, &body) {
		return
	}
	result, err := api.service.Cancel(request.Context(), CancelInput{UserID: principal.UserID,
		QueueID: request.PathValue("id"), ExpectedRevision: body.ExpectedRevision, IdempotencyKey: key})
	if err != nil {
		writeRPSError(w, err)
		return
	}
	if result.HTTPStatus != http.StatusNoContent {
		writeRPSError(w, ErrInvariant)
		return
	}
	writeRPSNoContent(w)
}

func (api *httpAPI) read(w http.ResponseWriter, request *http.Request, principal resources.ContinuationUserPrincipal) {
	if !rpsNoBody(w, request) || !rpsNoQuery(w, request) {
		return
	}
	result, err := api.service.Read(request.Context(), ReadInput{UserID: principal.UserID, SessionBinding: principal.SessionBinding})
	if err != nil {
		writeRPSError(w, err)
		return
	}
	writeRPSJSON(w, http.StatusOK, result)
}

type actionHTTPBody struct {
	PhaseSeq         string          `json:"phase_seq"`
	ExpectedRevision string          `json:"expected_revision"`
	Action           string          `json:"action"`
	Payload          json.RawMessage `json:"payload"`
}

func decodeActionInput(w http.ResponseWriter, request *http.Request, principal resources.ContinuationUserPrincipal, key string) (ActionInput, bool) {
	var body actionHTTPBody
	if !rpsDecodeStrict(w, request, &body) {
		return ActionInput{}, false
	}
	if len(body.Payload) == 0 || bytes.Equal(bytes.TrimSpace(body.Payload), []byte("null")) {
		rpsInvalid(w)
		return ActionInput{}, false
	}
	input := ActionInput{UserID: principal.UserID, SessionBinding: principal.SessionBinding, SessionID: request.PathValue("id"),
		PhaseSeq: body.PhaseSeq, ExpectedRevision: body.ExpectedRevision, Action: body.Action, IdempotencyKey: key}
	switch body.Action {
	case "gesture":
		var payload gestureActionPayload
		if !rpsDecodeStrictBytes(body.Payload, &payload) {
			rpsInvalid(w)
			return ActionInput{}, false
		}
		input.Gesture = payload.Gesture
	case "dealer_decision":
		var payload struct {
			Decision string          `json:"decision"`
			Amount   json.RawMessage `json:"amount"`
		}
		if !rpsDecodeStrictBytes(body.Payload, &payload) || payload.Decision == "raise" && len(payload.Amount) == 0 ||
			payload.Decision == "no_raise" && len(payload.Amount) != 0 {
			rpsInvalid(w)
			return ActionInput{}, false
		}
		input.DealerDecision = payload.Decision
		if len(payload.Amount) != 0 {
			if !rpsDecodeJSONString(payload.Amount, &input.RaiseAmount) {
				rpsInvalid(w)
				return ActionInput{}, false
			}
		}
	case "follower_decision":
		var payload followerActionPayload
		if !rpsDecodeStrictBytes(body.Payload, &payload) {
			rpsInvalid(w)
			return ActionInput{}, false
		}
		input.FollowerDecision = payload.Decision
	default:
		rpsInvalid(w)
		return ActionInput{}, false
	}
	return input, true
}

func (api *httpAPI) action(w http.ResponseWriter, request *http.Request, principal resources.ContinuationUserPrincipal) {
	if !rpsNoQuery(w, request) {
		return
	}
	key, ok := rpsIdempotencyKey(w, request)
	if !ok {
		return
	}
	input, ok := decodeActionInput(w, request, principal, key)
	if !ok {
		return
	}
	result, err := api.service.Action(request.Context(), input)
	if err != nil {
		writeRPSError(w, err)
		return
	}
	if !result.valid() {
		writeRPSError(w, ErrInvariant)
		return
	}
	writeRPSJSON(w, result.HTTPStatus, result.State)
}

func (api *httpAPI) lease(w http.ResponseWriter, request *http.Request, principal resources.ContinuationUserPrincipal) {
	if !rpsNoQuery(w, request) {
		return
	}
	var body struct {
		LeaseID string `json:"lease_id"`
	}
	if !rpsDecodeStrict(w, request, &body) {
		return
	}
	result, err := api.service.RenewLease(request.Context(), LeaseInput{UserID: principal.UserID,
		SessionBinding: principal.SessionBinding, SessionID: request.PathValue("id"), LeaseID: body.LeaseID})
	if err != nil {
		writeRPSError(w, err)
		return
	}
	writeRPSJSON(w, http.StatusOK, result)
}

func (api *httpAPI) ack(w http.ResponseWriter, request *http.Request, principal resources.ContinuationUserPrincipal) {
	if !rpsNoQuery(w, request) {
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
	}
	if !rpsDecodeStrict(w, request, &body) {
		return
	}
	result, err := api.service.ACK(request.Context(), ACKInput{UserID: principal.UserID,
		SessionBinding: principal.SessionBinding, SessionID: body.SessionID})
	if err != nil {
		writeRPSError(w, err)
		return
	}
	if result.HTTPStatus != http.StatusNoContent {
		writeRPSError(w, ErrInvariant)
		return
	}
	writeRPSNoContent(w)
}

func (api *httpAPI) leaderboard(w http.ResponseWriter, request *http.Request, principal resources.UserPrincipal) {
	if !rpsNoBody(w, request) {
		return
	}
	if request == nil || request.URL == nil || request.URL.ForceQuery && request.URL.RawQuery == "" {
		rpsInvalid(w)
		return
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(query) != 2 || len(query["mode"]) != 1 || len(query["board"]) != 1 {
		rpsInvalid(w)
		return
	}
	result, err := api.service.Leaderboard(request.Context(), principal.UserID, query["mode"][0], query["board"][0])
	if err != nil {
		writeRPSError(w, err)
		return
	}
	writeRPSJSON(w, http.StatusOK, result)
}

func rpsReadBody(w http.ResponseWriter, request *http.Request) ([]byte, bool) {
	if request == nil || request.Body == nil {
		rpsInvalid(w)
		return nil, false
	}
	limited := http.MaxBytesReader(w, request.Body, idempotency.MaxControlBodyBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			rpsInvalid(w)
		}
		return nil, false
	}
	if len(body) == 0 || rpsValidateStrictJSON(body) != nil {
		rpsInvalid(w)
		return nil, false
	}
	return body, true
}

func rpsDecodeStrict[T any](w http.ResponseWriter, request *http.Request, destination *T) bool {
	body, ok := rpsReadBody(w, request)
	if !ok {
		return false
	}
	if !rpsDecodeStrictBytes(body, destination) {
		rpsInvalid(w)
		return false
	}
	return true
}

func rpsDecodeStrictBytes[T any](body []byte, destination *T) bool {
	if rpsValidateStrictJSON(body) != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return false
	}
	return decoder.Decode(new(any)) == io.EOF
}

func rpsDecodeStrictResponseBytes[T any](body []byte, destination *T) bool {
	if rpsValidateStrictResponseJSON(body) != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return false
	}
	return decoder.Decode(new(any)) == io.EOF
}

func rpsDecodeJSONString(body []byte, destination *string) bool {
	if destination == nil || !utf8.Valid(body) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(destination); err != nil {
		return false
	}
	return decoder.Decode(new(any)) == io.EOF
}

func rpsValidateStrictJSON(body []byte) error {
	return rpsValidateJSON(body, false)
}

func rpsValidateStrictResponseJSON(body []byte) error {
	return rpsValidateJSON(body, true)
}

func rpsValidateJSON(body []byte, allowNull bool) error {
	if !utf8.Valid(body) {
		return errors.New("invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	return rpsWalkJSON(decoder, true, 0, allowNull)
}

func rpsWalkJSON(decoder *json.Decoder, root bool, depth int, allowNull bool) error {
	if depth > 32 {
		return errors.New("JSON too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		if root || token == nil && !allowNull {
			return errors.New("object required")
		}
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid object key")
			}
			if _, duplicate := seen[key]; duplicate || len(seen) >= 256 {
				return errors.New("duplicate or excessive object key")
			}
			seen[key] = struct{}{}
			if err := rpsWalkJSON(decoder, false, depth+1, allowNull); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		count := 0
		for decoder.More() {
			if count >= 10000 {
				return errors.New("excessive array")
			}
			count++
			if err := rpsWalkJSON(decoder, false, depth+1, allowNull); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return errors.New("invalid delimiter")
	}
}

func rpsNoBody(w http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil || len(body) != 0 {
		rpsInvalid(w)
		return false
	}
	return true
}

func rpsNoQuery(w http.ResponseWriter, request *http.Request) bool {
	if request == nil || request.URL == nil || request.URL.RawQuery != "" || request.URL.ForceQuery {
		rpsInvalid(w)
		return false
	}
	return true
}

func rpsIdempotencyKey(w http.ResponseWriter, request *http.Request) (string, bool) {
	if request == nil {
		rpsInvalid(w)
		return "", false
	}
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		rpsInvalid(w)
		return "", false
	}
	if _, err := idempotency.KeyHash(values[0]); err != nil {
		rpsInvalid(w)
		return "", false
	}
	return values[0], true
}

func rpsInvalid(w http.ResponseWriter) {
	httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
}

func writeRPSJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > maxProjectedStateBytes && status == http.StatusOK {
		writeRPSError(w, ErrInvariant)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeRPSNoContent(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func writeRPSError(w http.ResponseWriter, err error) {
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
		code, message = httperr.CodeRateLimited, "game action rate limit exceeded"
	case errors.Is(err, ErrFeatureDisabled):
		code, message = httperr.CodeFeatureDisabled, "game is disabled"
	case errors.Is(err, ErrInsufficientCredits):
		code, message = httperr.CodeInsufficientCredits, "insufficient credits"
	case errors.Is(err, ErrMaintenance):
		code, message = httperr.CodeMaintenance, "maintenance mode"
	case errors.Is(err, ErrServiceUnavailable), errors.Is(err, ErrClosed):
		code, message = httperr.CodeServiceUnavailable, "service unavailable"
	case errors.Is(err, ErrResourceLimit):
		code, message = httperr.CodeResourceLimitExceeded, "resource limit exceeded"
	}
	httperr.WriteError(w, httperr.New(code, message))
}
