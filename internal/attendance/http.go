package attendance

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/rewards"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const userRoute = "/api/attendance"
const signInRoute = "/api/attendance/{id}/sign-in"
const adminRoute = "/admin/api/attendance"
const adminPeriodRoute = "/admin/api/attendance/{id}"

func RegisterRoutes(users limitedactivities.UserRouteRegistrar, admins limitedactivities.AdminRouteRegistrar, s *Service) error {
	if users == nil || admins == nil || s == nil {
		return ErrInvalid
	}
	list := func(w http.ResponseWriter, r *http.Request, user int64, admin bool) {
		page, size, err := pagination(r)
		if err != nil {
			writeError(w, err)
			return
		}
		out, err := s.List(r.Context(), user, admin, page, size)
		respond(w, out, err)
	}
	if err := users.RegisterUserRoute(http.MethodGet, userRoute, func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
		list(w, r, p.UserID, false)
	}); err != nil {
		return err
	}
	if err := admins.RegisterAdminRoute(http.MethodGet, adminRoute, func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
		list(w, r, p.UserID, true)
	}); err != nil {
		return err
	}
	if err := users.RegisterUserRoute(http.MethodPost, signInRoute, func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
		var in SignInInput
		key, err := decode(r, &in, []string{"site_date"})
		if err != nil {
			writeError(w, err)
			return
		}
		out, err := s.SignIn(r.Context(), p.UserID, r.PathValue("id"), key, in)
		respond(w, out, err)
	}); err != nil {
		return err
	}
	fields := []string{"title", "description", "start_date", "end_date", "mode", "published", "tiers"}
	if err := admins.RegisterAdminRoute(http.MethodPost, adminRoute, func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
		var in Input
		key, err := decode(r, &in, fields)
		if err != nil {
			writeError(w, err)
			return
		}
		out, err := s.Create(r.Context(), p.UserID, key, in)
		respond(w, out, err)
	}); err != nil {
		return err
	}
	return admins.RegisterAdminRoute(http.MethodPut, adminPeriodRoute, func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
		var in Edit
		key, err := decode(r, &in, append([]string{"expected_revision"}, fields...))
		if err != nil {
			writeError(w, err)
			return
		}
		out, err := s.Update(r.Context(), p.UserID, r.PathValue("id"), key, in)
		respond(w, out, err)
	})
}
func pagination(r *http.Request) (int, int, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, 0, ErrInvalid
	}
	for k, v := range q {
		if (k != "page" && k != "page_size") || len(v) != 1 {
			return 0, 0, ErrInvalid
		}
	}
	values := []int{1, 20}
	for i, k := range []string{"page", "page_size"} {
		if q.Has(k) {
			n, e := strconv.Atoi(q.Get(k))
			if e != nil || strconv.Itoa(n) != q.Get(k) {
				return 0, 0, ErrInvalid
			}
			values[i] = n
		}
	}
	if r.Body != nil {
		raw, e := io.ReadAll(io.LimitReader(r.Body, 1))
		if e != nil || len(raw) > 0 {
			return 0, 0, ErrInvalid
		}
	}
	return values[0], values[1], nil
}
func decode(r *http.Request, out any, required []string) (string, error) {
	if r.URL.RawQuery != "" || r.Body == nil || len(r.Header.Values("Idempotency-Key")) != 1 {
		return "", ErrInvalid
	}
	key := r.Header.Get("Idempotency-Key")
	if _, err := idempotency.KeyHash(key); err != nil {
		return "", ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, idempotency.MaxControlBodyBytes+1))
	if err != nil || len(raw) > idempotency.MaxControlBodyBytes {
		return "", ErrInvalid
	}
	if err = strictjson.ValidateObjectWithFieldLimit(raw, 4096); err != nil {
		return "", ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(out); err != nil {
		return "", ErrInvalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return "", ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return "", ErrInvalid
	}
	for _, key := range required {
		v, ok := fields[key]
		if !ok || bytes.Equal(v, []byte("null")) {
			return "", ErrInvalid
		}
	}
	return key, nil
}
func respond(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httperr.WriteJSON(w, http.StatusOK, value)
}
func writeError(w http.ResponseWriter, err error) {
	code, message := httperr.CodeServiceUnavailable, "The attendance activity could not be updated. Please try again."
	switch {
	case errors.Is(err, authz.ErrMFARequired):
		code, message = httperr.CodeMFARequired, "MFA verification is required."
	case errors.Is(err, authz.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "Sign in to use this activity."
	case errors.Is(err, authz.ErrForbidden):
		code, message = httperr.CodeForbidden, "This account cannot perform that activity operation."
	case errors.Is(err, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "Check the attendance dates and rewards."
		if strings.HasPrefix(err.Error(), ErrInvalid.Error()+": ") {
			message = strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")
		}
	case errors.Is(err, ErrConflict):
		code, message = httperr.CodeConflict, "The activity or site date changed. Refresh and try again."
		if strings.HasPrefix(err.Error(), ErrConflict.Error()+": ") {
			message = strings.TrimPrefix(err.Error(), ErrConflict.Error()+": ")
		}
	case errors.Is(err, ErrNotFound):
		code, message = httperr.CodeNotFound, "Attendance period not found."
	case errors.Is(err, ErrClosed), errors.Is(err, db.ErrTimezoneUnavailable):
		code, message = httperr.CodeFeatureDisabled, "This period is not accepting sign-ins."
	case errors.Is(err, rewards.ErrCapacity):
		code, message = httperr.CodeResourceLimitExceeded, "The reward could not fit in your account. Your sign-in was not saved; free capacity and try again."
	case errors.Is(err, maintenance.ErrMaintenanceOn):
		code, message = httperr.CodeMaintenance, "The site is under maintenance."
	}
	httperr.WriteError(w, httperr.New(code, message))
}
