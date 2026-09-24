package limitedactivities

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

const (
	userDirectoryRoute = "/api/limited-activities"
	userDetailRoute    = "/api/limited-activities/{key}"
	userWalletRoute    = "/api/limited-activities/picture-book/wallet"
	userExchangeRoute  = "/api/limited-activities/picture-book/exchange"
	adminConfigRoute   = "/admin/api/limited-activities/{key}"
)

// RegisterRoutes relies on root composition for session, CSRF, station and
// admission wrappers; every handler also repeats transaction-local authority.
func RegisterRoutes(users UserRouteRegistrar, admins AdminRouteRegistrar, s *Service) error {
	if nilInterface(users) || nilInterface(admins) || s == nil {
		return ErrInvalid
	}
	get := func(fn func(*http.Request, UserPrincipal) (any, error)) AuthorizedUserHandler {
		return func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
			if !emptyRequest(r) {
				writeError(w, ErrInvalid)
				return
			}
			result, err := fn(r, p)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, result)
		}
	}
	for _, route := range []struct {
		path    string
		handler AuthorizedUserHandler
	}{
		{userDirectoryRoute, get(func(r *http.Request, p UserPrincipal) (any, error) { return s.List(r.Context(), p.UserID) })},
		{userDetailRoute, get(func(r *http.Request, p UserPrincipal) (any, error) {
			return s.Detail(r.Context(), p.UserID, r.PathValue("key"))
		})},
		{userWalletRoute, get(func(r *http.Request, p UserPrincipal) (any, error) { return s.Wallet(r.Context(), p.UserID) })},
	} {
		if err := users.RegisterUserRoute(http.MethodGet, route.path, route.handler); err != nil {
			return err
		}
	}
	if err := users.RegisterUserRoute(http.MethodPost, userExchangeRoute, func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
		var input ExchangeInput
		if err := decodeRequest(r, &input, []string{"asset", "quantity"}); err != nil {
			writeError(w, err)
			return
		}
		key, err := requestKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := s.Exchange(r.Context(), p.UserID, key, input)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, result.Value)
	}); err != nil {
		return err
	}
	if err := admins.RegisterAdminRoute(http.MethodGet, adminConfigRoute, func(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
		if !emptyRequest(r) {
			writeError(w, ErrInvalid)
			return
		}
		result, err := s.AdminConfig(r.Context(), p.UserID, r.PathValue("key"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, result)
	}); err != nil {
		return err
	}
	return admins.RegisterAdminRoute(http.MethodPut, adminConfigRoute, func(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
		var input ConfigInput
		if err := decodeRequest(r, &input, []string{"expected_revision", "visible", "starts_at", "ends_at", "paused", "module_config"}); err != nil {
			writeError(w, err)
			return
		}
		key, err := requestKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := s.UpdateConfig(r.Context(), p.UserID, r.PathValue("key"), key, input)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, result.Value)
	})
}

func requestKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", ErrInvalid
	}
	return values[0], nil
}

func emptyRequest(r *http.Request) bool {
	if r == nil || r.URL == nil || r.URL.RawQuery != "" {
		return false
	}
	if r.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1))
	return err == nil && len(body) == 0
}

func decodeRequest(r *http.Request, out any, required []string) error {
	if r == nil || r.URL == nil || r.URL.RawQuery != "" || r.Body == nil {
		return ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return ErrInvalid
	}
	if err = strictJSON(raw, out); err != nil {
		return ErrInvalid
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return ErrInvalid
	}
	for _, key := range required {
		value, ok := fields[key]
		if !ok || key != "starts_at" && key != "ends_at" && string(value) == "null" {
			return ErrInvalid
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Cache-Control", "no-store")
	httperr.WriteJSON(w, http.StatusOK, value)
}
func writeError(w http.ResponseWriter, err error) {
	code, message := httperr.CodeServiceUnavailable, "The activity could not be loaded. Please try again."
	switch {
	case errors.Is(err, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "Check the activity settings or exchange quantity."
	case errors.Is(err, authz.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "Sign in to use this activity."
	case errors.Is(err, authz.ErrForbidden):
		code, message = httperr.CodeForbidden, "This account cannot perform that activity operation."
	case errors.Is(err, ErrNotFound):
		code, message = httperr.CodeNotFound, "Activity not found."
	case errors.Is(err, ErrConflict):
		code, message = httperr.CodeConflict, "The activity settings or request identity changed."
	case errors.Is(err, ErrClosed):
		code, message = httperr.CodeFeatureDisabled, "This activity is not accepting new exchanges or tasks."
	case errors.Is(err, ErrCapacity):
		code, message = httperr.CodeResourceLimitExceeded, "The requested activity currency is no longer available."
	case errors.Is(err, ledger.ErrInsufficientBalance):
		code, message = httperr.CodeInsufficientCredits, "There are not enough general credits for this exchange."
	case errors.Is(err, maintenance.ErrMaintenanceOn):
		code, message = httperr.CodeMaintenance, "The site is under maintenance."
	}
	httperr.WriteError(w, httperr.New(code, message))
}
