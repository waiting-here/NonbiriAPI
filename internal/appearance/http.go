package appearance

import (
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

func RegisterRoutes(routes limitedactivities.UserRouteRegistrar, s *Service) error {
	if routes == nil || s == nil {
		return ErrInvalid
	}
	if err := routes.RegisterUserRoute(http.MethodGet, "/api/appearance", func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
		if r.URL.RawQuery != "" {
			respond(w, State{}, ErrInvalid)
			return
		}
		if !httpapi.NoBody(r) {
			respond(w, State{}, ErrInvalid)
			return
		}
		out, err := s.State(r.Context(), p.UserID)
		respond(w, out, err)
	}); err != nil {
		return err
	}
	return routes.RegisterUserRoute(http.MethodPost, "/api/appearance", func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
		if r.URL.RawQuery != "" || r.Body == nil {
			respond(w, State{}, ErrInvalid)
			return
		}
		var input struct {
			Selected string `json:"selected"`
			Moment   string `json:"moment"`
		}
		_, err := httpapi.ReadJSON(w, r, &input, httpapi.BodyOptions{MaxBytes: 1024, Validate: strictjson.ValidateObject})
		if err != nil || input.Selected == "" {
			respond(w, State{}, ErrInvalid)
			return
		}
		out, err := s.Select(r.Context(), p.UserID, input.Selected, input.Moment)
		respond(w, out, err)
	})
}
func respond(w http.ResponseWriter, state State, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if err == nil {
		httperr.WriteJSON(w, http.StatusOK, state)
		return
	}
	code, message := httperr.CodeServiceUnavailable, "Your appearance could not be updated."
	switch {
	case errors.Is(err, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "Choose an available appearance."
	case errors.Is(err, authz.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "Sign in to choose an appearance."
	case errors.Is(err, authz.ErrForbidden):
		code, message = httperr.CodeForbidden, "This appearance is not available for this account."
	case errors.Is(err, maintenance.ErrMaintenanceOn):
		code, message = httperr.CodeMaintenance, "The site is under maintenance."
	}
	httperr.WriteError(w, httperr.New(code, message))
}
