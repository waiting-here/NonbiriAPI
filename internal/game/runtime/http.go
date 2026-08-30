package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type httpAPI struct{ service *Service }

func RegisterUserRoutes(registrar resources.UserRouteRegistrar, service *Service) error {
	if registrar == nil || service == nil {
		return errors.New("game runtime: user registrar and service are required")
	}
	api := &httpAPI{service: service}
	routes := []struct {
		method, path string
		handler      resources.AuthorizedUserHandler
	}{
		{http.MethodGet, RouteGames, api.games}, {http.MethodPost, RouteFishingBatches, api.start}, {http.MethodGet, RouteFishingState, api.state}, {http.MethodPost, RouteFishingACK, api.ack}, {http.MethodPost, RouteFishingRecover, api.recover}, {http.MethodGet, RouteFishingLeaderboard, api.leaderboard},
	}
	for _, route := range routes {
		if err := registrar.RegisterUserRoute(route.method, route.path, route.handler); err != nil {
			return fmt.Errorf("game runtime: register %s %s: %w", route.method, route.path, err)
		}
	}
	return nil
}

func RegisterAdminRoutes(registrar AdminRouteRegistrar, service *Service) error {
	if registrar == nil || service == nil {
		return errors.New("game runtime: admin registrar and service are required")
	}
	api := &httpAPI{service: service}
	routes := []struct {
		method, path string
		handler      http.HandlerFunc
	}{{http.MethodGet, RouteAdminActiveCounts, api.activeCounts}, {http.MethodGet, RouteAdminGamesConfig, api.getConfig}, {http.MethodPatch, RouteAdminGamesConfig, api.patchConfig}}
	for _, route := range routes {
		if err := registrar.RegisterAdminRoute(route.method, route.path, route.handler); err != nil {
			return fmt.Errorf("game runtime: register %s %s: %w", route.method, route.path, err)
		}
	}
	return nil
}

func (api *httpAPI) games(w http.ResponseWriter, r *http.Request, principal resources.UserPrincipal) {
	if !noBody(w, r) || !requireExactQuery(w, r) {
		return
	}
	snapshot, err := api.service.GamesSnapshot(r.Context(), principal.UserID, api.service.now())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}
func (api *httpAPI) start(w http.ResponseWriter, r *http.Request, principal resources.UserPrincipal) {
	if !requireExactQuery(w, r) {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body startBody
	if !decodeStrict(w, r, &body) {
		return
	}
	result, pending, err := api.service.StartFishing(r.Context(), StartInput{UserID: principal.UserID, Bait: body.Bait, Count: body.Count, IdempotencyKey: key})
	writeFishingUnion(w, result, pending, err)
}
func (api *httpAPI) state(w http.ResponseWriter, r *http.Request, principal resources.UserPrincipal) {
	if !noBody(w, r) || !requireExactQuery(w, r) {
		return
	}
	state, err := api.service.FishingState(r.Context(), principal.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}
func (api *httpAPI) ack(w http.ResponseWriter, r *http.Request, principal resources.UserPrincipal) {
	if !noBody(w, r) || !requireExactQuery(w, r) {
		return
	}
	if err := api.service.AcknowledgeFishing(r.Context(), principal.UserID, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
func (api *httpAPI) recover(w http.ResponseWriter, r *http.Request, principal resources.UserPrincipal) {
	if !noBody(w, r) || !requireExactQuery(w, r) {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	result, pending, err := api.service.RecoverFishing(r.Context(), RecoverInput{UserID: principal.UserID, BatchID: r.PathValue("id"), IdempotencyKey: key})
	writeFishingUnion(w, result, pending, err)
}
func (api *httpAPI) leaderboard(w http.ResponseWriter, r *http.Request, principal resources.UserPrincipal) {
	if !noBody(w, r) || !requireExactQuery(w, r, "board") {
		return
	}
	values, ok := r.URL.Query()["board"]
	if !ok || len(values) != 1 {
		returnInvalid(w)
	}
	result, err := api.service.FishingLeaderboard(r.Context(), principal.UserID, values[0])
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
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

func writeFishingUnion(w http.ResponseWriter, result *FishingBatchResult, pending *FishingSettlementPending, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	if (result == nil) == (pending == nil) {
		writeError(w, ErrInvariant)
		return
	}
	if result != nil {
		writeJSON(w, http.StatusOK, result)
	} else {
		writeJSON(w, http.StatusAccepted, pending)
	}
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
func decodeStrict[T any](w http.ResponseWriter, r *http.Request, destination *T) bool {
	body, ok := readBody(w, r)
	if !ok {
		return false
	}
	if err := validateStrictJSON(body); err != nil {
		returnInvalid(w)
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
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	return walkHTTPJSON(decoder, true)
}
func walkHTTPJSON(decoder *json.Decoder, root bool) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		if token == nil {
			return errors.New("null")
		}
		if root {
			return errors.New("object required")
		}
		return nil
	}
	if delimiter == '{' {
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, tokenErr := decoder.Token()
			if tokenErr != nil {
				return tokenErr
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return errors.New("duplicate key")
			}
			seen[key] = true
			if err := walkHTTPJSON(decoder, false); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	}
	if delimiter == '[' {
		for decoder.More() {
			if err := walkHTTPJSON(decoder, false); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	}
	return errors.New("invalid delimiter")
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
	case errors.Is(err, ErrCapacity):
		code, message = httperr.CodeResourceLimitExceeded, "resource limit exceeded"
	}
	httperr.WriteError(w, httperr.New(code, message))
}
