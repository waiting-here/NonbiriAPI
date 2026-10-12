package gacha

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

func RegisterRoutes(users limitedactivities.UserRouteRegistrar, admins limitedactivities.AdminRouteRegistrar, s *Service) error {
	if users == nil || admins == nil || s == nil {
		return ErrInvalid
	}
	if err := users.RegisterUserRoute(http.MethodGet, "/api/gacha/entry", func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
		if !empty(r, false) {
			writeError(w, ErrInvalid)
			return
		}
		out, err := s.Entry(r.Context(), p.UserID)
		respond(w, out, err)
	}); err != nil {
		return err
	}
	if err := users.RegisterUserRoute(http.MethodGet, "/api/gacha/state", func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
		if !empty(r, false) {
			writeError(w, ErrInvalid)
			return
		}
		out, err := s.State(r.Context(), p.UserID)
		respond(w, out, err)
	}); err != nil {
		return err
	}
	if err := users.RegisterUserRoute(http.MethodGet, "/api/gacha/history", func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
		if !empty(r, true) || len(r.URL.Query()) > 1 || len(r.URL.Query()["cursor"]) > 1 || len(r.URL.Query().Get("cursor")) > 128 {
			writeError(w, ErrInvalid)
			return
		}
		for key := range r.URL.Query() {
			if key != "cursor" {
				writeError(w, ErrInvalid)
				return
			}
		}
		out, err := s.History(r.Context(), p.UserID, r.URL.Query().Get("cursor"))
		respond(w, out, err)
	}); err != nil {
		return err
	}
	if err := users.RegisterUserRoute(http.MethodPost, "/api/gacha/actions", func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
		var input Action
		if err := decode(w, r, &input); err != nil {
			writeError(w, err)
			return
		}
		key, err := requestKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		out, err := s.Act(r.Context(), p.UserID, key, input)
		var rejection Rejection
		if errors.As(err, &rejection) {
			writeJSON(w, map[string]string{"rejection": string(rejection)})
			return
		}
		respond(w, out, err)
	}); err != nil {
		return err
	}
	if err := admins.RegisterAdminRoute(http.MethodGet, "/admin/api/gacha", func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
		if !empty(r, false) {
			writeError(w, ErrInvalid)
			return
		}
		out, err := s.AdminConfig(r.Context(), p.UserID)
		respond(w, out, err)
	}); err != nil {
		return err
	}
	return admins.RegisterAdminRoute(http.MethodPost, "/admin/api/gacha", func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
		var input AdminInput
		if err := decode(w, r, &input); err != nil {
			writeError(w, err)
			return
		}
		key, err := requestKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		out, err := s.AdminSave(r.Context(), p.UserID, key, input)
		respond(w, out, err)
	})
}
func requestKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", ErrInvalid
	}
	if _, err := idempotency.KeyHash(values[0]); err != nil {
		return "", ErrInvalid
	}
	return values[0], nil
}
func empty(r *http.Request, query bool) bool {
	if r.URL.RawQuery != "" && !query {
		return false
	}
	return httpapi.NoBody(r)
}
func decode(w http.ResponseWriter, r *http.Request, out any) error {
	if r.URL.RawQuery != "" || r.Body == nil {
		return ErrInvalid
	}
	raw, err := httpapi.ReadJSON(w, r, out, httpapi.BodyOptions{MaxBytes: 32768, Validate: strictjson.ValidateObject})
	if err != nil {
		return ErrInvalid
	}
	// Null fields must not silently acquire a Go zero value.
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return ErrInvalid
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrInvalid
		}
	}
	return nil
}
func respond(w http.ResponseWriter, out any, err error) {
	if err != nil {
		writeError(w, err)
	} else {
		writeJSON(w, out)
	}
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Cache-Control", "no-store")
	httperr.WriteJSON(w, http.StatusOK, value)
}
func writeError(w http.ResponseWriter, err error) {
	code, message := httperr.CodeServiceUnavailable, "The card activity could not complete this request."
	switch {
	case errors.Is(err, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "Check the activity request and settings."
	case errors.Is(err, authz.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "Sign in to use this activity."
	case errors.Is(err, authz.ErrForbidden):
		code, message = httperr.CodeForbidden, "This account cannot perform this operation."
	case errors.Is(err, ErrConflict), errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		code, message = httperr.CodeConflict, "This request or configuration revision has changed."
	case errors.Is(err, ErrNotFound):
		code, message = httperr.CodeNotFound, "Open the activity before continuing."
	case errors.Is(err, ErrCapacity):
		code, message = httperr.CodeResourceLimitExceeded, "This operation exceeds the activity balance capacity."
	case errors.Is(err, ledger.ErrInsufficientBalance):
		code, message = httperr.CodeInsufficientCredits, "There are not enough credits for this exchange."
	case errors.Is(err, maintenance.ErrMaintenanceOn):
		code, message = httperr.CodeMaintenance, "The site is under maintenance."
	case errors.Is(err, db.ErrTimezoneUnavailable):
		code, message = httperr.CodeFeatureDisabled, "The activity is not ready yet."
	}
	w.Header().Set("Cache-Control", "no-store")
	httperr.WriteError(w, httperr.New(code, message))
}
