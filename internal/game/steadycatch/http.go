package steadycatch

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/game/ranking"
	"github.com/waiting-here/NonbiriAPI/internal/game/steadycatch/engine"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (s *Service) registerRoutes(r host.Registrars) error {
	if err := r.User.RegisterUserRoute("GET", "/api/games/steady-catch/catalog", func(w http.ResponseWriter, request *http.Request, _ resources.UserPrincipal) {
		respond(w, engine.Catalog(), nil)
	}); err != nil {
		return err
	}
	if err := r.User.RegisterUserRoute("POST", "/api/games/steady-catch/sessions", func(w http.ResponseWriter, request *http.Request, p resources.UserPrincipal) {
		var body struct{}
		if !decode(w, request, &body) {
			return
		}
		keys := request.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			respond(w, nil, ErrInvalid)
			return
		}
		v, err := s.Start(request.Context(), p.UserID, keys[0])
		respond(w, v, err)
	}); err != nil {
		return err
	}
	if err := r.User.RegisterUserRoute("GET", "/api/games/steady-catch/leaderboard", func(w http.ResponseWriter, request *http.Request, p resources.UserPrincipal) {
		v, err := s.Leaderboard(request.Context(), p.UserID, request.URL.Query().Get("window"))
		respond(w, v, err)
	}); err != nil {
		return err
	}
	if err := r.Continuation.RegisterContinuationUserRoute("GET", "/api/games/steady-catch/session", func(w http.ResponseWriter, request *http.Request, p resources.ContinuationUserPrincipal) {
		v, err := s.Read(request.Context(), Identity{p.UserID, p.SessionBinding})
		respond(w, v, err)
	}); err != nil {
		return err
	}
	return r.Continuation.RegisterContinuationUserRoute("POST", "/api/games/steady-catch/sessions/{id}/controls", func(w http.ResponseWriter, request *http.Request, p resources.ContinuationUserPrincipal) {
		var c Controls
		if !decode(w, request, &c) {
			return
		}
		v, err := s.Control(request.Context(), Identity{p.UserID, p.SessionBinding}, request.PathValue("id"), c)
		respond(w, v, err)
	})
}
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := httpapi.ReadBody(w, r, httpapi.BodyOptions{MaxBytes: 65536})
	if err == nil {
		err = httpapi.DecodeJSON(body, dst)
	}
	if err != nil {
		if errors.Is(err, httpapi.ErrTooLarge) {
			httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			respond(w, nil, ErrInvalid)
		}
		return false
	}
	return true
}
func respond(w http.ResponseWriter, value any, err error) {
	if err != nil {
		code, message := httperr.CodeInternal, "request failed"
		switch {
		case errors.Is(err, ErrInvalid):
			code, message = httperr.CodeInvalidRequest, "invalid request"
		case errors.Is(err, ErrConflict), errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
			code, message = httperr.CodeConflict, "refresh the current game"
		case errors.Is(err, ErrNotFound):
			code, message = httperr.CodeNotFound, "game not found"
		case errors.Is(err, ErrDisabled):
			code, message = httperr.CodeFeatureDisabled, "game is disabled"
		case errors.Is(err, resources.ErrUnauthorized):
			code, message = httperr.CodeUnauthorized, "authentication required"
		case errors.Is(err, resources.ErrForbidden), errors.Is(err, game.ErrUserDeleting):
			code, message = httperr.CodeForbidden, "forbidden"
		case errors.Is(err, resources.ErrMaintenance), errors.Is(err, maintenance.ErrContinuationDenied):
			code, message = httperr.CodeMaintenance, "maintenance mode"
		case errors.Is(err, ledger.ErrInsufficientBalance):
			code, message = httperr.CodeInsufficientCredits, "insufficient credits"
		case errors.Is(err, game.ErrStartRateLimited):
			code, message = httperr.CodeRateLimited, "game start rate limit exceeded"
		case errors.Is(err, ErrUnavailable), errors.Is(err, ranking.ErrCatchingUp), errors.Is(err, ledger.ErrCapacityExhausted), errors.Is(err, ledger.ErrRetryable):
			code, message = httperr.CodeServiceUnavailable, "temporarily unavailable"
		}
		httperr.WriteError(w, httperr.New(code, message))
		return
	}
	body, err := json.Marshal(value)
	if err != nil {
		httperr.WriteError(w, httperr.New(httperr.CodeInternal, "request failed"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
