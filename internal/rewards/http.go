package rewards

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
	"io"
	"net/http"
	"net/url"
)

func RegisterRoutes(users limitedactivities.UserRouteRegistrar, admins limitedactivities.AdminRouteRegistrar, s *Service) error {
	if users == nil || admins == nil || s == nil {
		return ErrInvalid
	}
	for _, path := range []string{"/api/rewards/assets", "/api/rewards/history"} {
		if err := users.RegisterUserRoute(http.MethodGet, path, func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
			cursor, err := readCursor(r)
			if err != nil {
				respond(w, nil, err)
				return
			}
			if r.URL.Path == "/api/rewards/assets" {
				if cursor != "" {
					respond(w, nil, ErrInvalid)
					return
				}
				tx, e := s.config.ReadDatabase.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
				if e != nil {
					respond(w, nil, e)
					return
				}
				defer tx.Rollback()
				if e = s.config.Users.AuthorizeUserMutation(r.Context(), tx, p.UserID); e != nil {
					_ = tx.Rollback()
					respond(w, nil, e)
					return
				}
				out, e := userAssets(r.Context(), tx, p.UserID)
				_ = tx.Rollback()
				respond(w, out, e)
				return
			}
			out, e := s.History(r.Context(), p.UserID, cursor)
			respond(w, out, e)
		}); err != nil {
			return err
		}
	}
	if err := users.RegisterUserRoute(http.MethodPost, "/api/rewards/redeem", func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
		var in struct {
			Code *string `json:"code"`
		}
		if err := decode(r, &in); err != nil {
			respond(w, nil, err)
			return
		}
		if in.Code == nil {
			respond(w, nil, ErrInvalid)
			return
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			respond(w, nil, ErrInvalid)
			return
		}
		out, err := s.Redeem(r.Context(), p.UserID, keys[0], *in.Code)
		if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrConditions) || errors.Is(err, ErrCapacity) {
			respond(w, redemptionResponse(out, err), nil)
			return
		}
		respond(w, redemptionResponse(out, nil), err)
	}); err != nil {
		return err
	}
	if err := admins.RegisterAdminRoute(http.MethodGet, "/admin/api/rewards", func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
		cursor, err := readCursor(r)
		if err != nil {
			respond(w, nil, err)
			return
		}
		out, e := s.List(r.Context(), p.UserID, cursor)
		respond(w, out, e)
	}); err != nil {
		return err
	}
	if err := admins.RegisterAdminRoute(http.MethodGet, "/admin/api/rewards/assets", func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
		cursor, err := readCursor(r)
		if err != nil || cursor != "" {
			respond(w, nil, ErrInvalid)
			return
		}
		tx, e := s.config.ReadDatabase.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
		if e != nil {
			respond(w, nil, e)
			return
		}
		defer tx.Rollback()
		if e = s.config.Admins.AuthorizeAdmin(r.Context(), tx, p.UserID); e != nil {
			_ = tx.Rollback()
			respond(w, nil, e)
			return
		}
		_ = tx.Rollback()
		respond(w, Assets(), nil)
	}); err != nil {
		return err
	}
	if err := admins.RegisterAdminRoute(http.MethodGet, "/admin/api/rewards/failures", func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(q) > 2 || len(q["campaign_id"]) != 1 || len(q["cursor"]) > 1 {
			respond(w, nil, ErrInvalid)
			return
		}
		for k := range q {
			if k != "campaign_id" && k != "cursor" {
				respond(w, nil, ErrInvalid)
				return
			}
		}
		if r.Body != nil {
			b, e := io.ReadAll(io.LimitReader(r.Body, 1))
			if e != nil || len(b) > 0 {
				respond(w, nil, ErrInvalid)
				return
			}
		}
		out, e := s.Failures(r.Context(), p.UserID, q.Get("campaign_id"), q.Get("cursor"))
		respond(w, out, e)
	}); err != nil {
		return err
	}
	for _, path := range []string{"/admin/api/rewards/preview", "/admin/api/rewards/create"} {
		if err := admins.RegisterAdminRoute(http.MethodPost, path, func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
			var in Input
			if err := decode(r, &in); err != nil {
				respond(w, nil, err)
				return
			}
			if r.URL.Path == "/admin/api/rewards/preview" {
				out, err := s.Preview(r.Context(), p.UserID, in)
				respond(w, out, err)
				return
			}
			values := r.Header.Values("Idempotency-Key")
			if len(values) != 1 {
				respond(w, nil, ErrInvalid)
				return
			}
			if _, err := idempotency.KeyHash(values[0]); err != nil {
				respond(w, nil, ErrInvalid)
				return
			}
			out, err := s.Create(r.Context(), p.UserID, values[0], in)
			respond(w, out, err)
		}); err != nil {
			return err
		}
	}
	if err := admins.RegisterAdminRoute(http.MethodPost, "/admin/api/rewards/update", func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
		var in Edit
		if err := decode(r, &in); err != nil {
			respond(w, nil, err)
			return
		}
		out, err := s.Update(r.Context(), p.UserID, in)
		respond(w, out, err)
	}); err != nil {
		return err
	}
	return admins.RegisterAdminRoute(http.MethodPost, "/admin/api/rewards/retry", func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
		var in struct {
			ID string `json:"id"`
		}
		if err := decode(r, &in); err != nil {
			respond(w, nil, err)
			return
		}
		out, err := s.Retry(r.Context(), p.UserID, in.ID)
		respond(w, out, err)
	})
}
func readCursor(r *http.Request) (string, error) {
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, 1))
		if err != nil || len(b) > 0 {
			return "", ErrInvalid
		}
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", ErrInvalid
	}
	if len(q) > 1 || len(q["cursor"]) > 1 {
		return "", ErrInvalid
	}
	for k := range q {
		if k != "cursor" {
			err = ErrInvalid
		}
	}
	c := q.Get("cursor")
	if len(c) > 20 {
		return "", ErrInvalid
	}
	return c, err
}
func decode(r *http.Request, out any) error {
	if r.URL.RawQuery != "" || r.Body == nil {
		return ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 32769))
	if err != nil || len(raw) > 32768 {
		return ErrInvalid
	}
	if strictjson.ValidateObject(raw) != nil {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return ErrInvalid
	}
	return nil
}
func respond(w http.ResponseWriter, out any, err error) {
	if errors.Is(err, authz.ErrMFARequired) {
		httperr.WriteError(w, httperr.New(httperr.CodeMFARequired, "MFA verification is required."))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if err == nil {
		httperr.WriteJSON(w, http.StatusOK, out)
		return
	}
	code, message := httperr.CodeServiceUnavailable, "The reward could not be delivered. Try again."
	switch {
	case errors.Is(err, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "Check the reward settings."
	case errors.Is(err, ErrConflict), errors.Is(err, idempotency.ErrConflict):
		code, message = httperr.CodeConflict, "The code or configuration has changed."
	case errors.Is(err, authz.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "Sign in to continue."
	case errors.Is(err, authz.ErrForbidden):
		code, message = httperr.CodeForbidden, "This account cannot perform this operation."
	case errors.Is(err, ErrCapacity):
		code, message = httperr.CodeResourceLimitExceeded, "The reward exceeds the available capacity."
	case errors.Is(err, ErrRate):
		code, message = httperr.CodeRateLimited, "Try again in a minute."
	case errors.Is(err, maintenance.ErrMaintenanceOn):
		code, message = httperr.CodeMaintenance, "The site is under maintenance."
	}
	httperr.WriteError(w, httperr.New(code, message))
}
