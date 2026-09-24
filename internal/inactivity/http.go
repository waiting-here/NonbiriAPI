package inactivity

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
)

type AdminRouteRegistrar interface {
	RegisterAdminRoute(string, string, http.Handler) error
}

func RegisterAdminRoutes(registrar AdminRouteRegistrar, s *Service) error {
	if registrar == nil || s == nil {
		return ErrInvalid
	}
	for _, route := range []struct {
		method, path string
		handler      http.HandlerFunc
	}{
		{http.MethodGet, "", s.getHTTP}, {http.MethodPut, "", s.putHTTP},
		{http.MethodPost, "/preview", s.previewHTTP}, {http.MethodGet, "/runs", s.runsHTTP},
		{http.MethodGet, "/audits", s.auditsHTTP},
	} {
		if err := registrar.RegisterAdminRoute(route.method, "/admin/api/inactivity-policy"+route.path, route.handler); err != nil {
			return err
		}
	}
	return nil
}
func query(r *http.Request, allowed ...string) (url.Values, error) {
	if r == nil || r.URL == nil || len(r.URL.RawQuery) > 512 {
		return nil, ErrInvalid
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, ErrInvalid
	}
	for key, entries := range values {
		if len(entries) != 1 || entries[0] == "" {
			return nil, ErrInvalid
		}
		found := false
		for _, name := range allowed {
			found = found || key == name
		}
		if !found {
			return nil, ErrInvalid
		}
	}
	return values, nil
}
func noBody(r *http.Request) error {
	if r.Body == nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(b) != 0 {
		return ErrInvalid
	}
	return nil
}
func body(w http.ResponseWriter, r *http.Request, out any) error {
	if _, err := query(r); err != nil {
		return err
	}
	content, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || content != "application/json" {
		return ErrInvalid
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil || strictjson.ValidateObject(raw) != nil {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func reply(w http.ResponseWriter, result any, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if err == nil {
		httperr.WriteJSON(w, 200, result)
		return
	}
	code, message := httperr.CodeServiceUnavailable, "The inactivity policy is temporarily unavailable."
	switch {
	case errors.Is(err, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "Check the policy fields and request format."
	case errors.Is(err, ErrForbidden):
		code, message = httperr.CodeForbidden, "This operation requires a current authorized session."
	case errors.Is(err, ErrConflict), errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		code, message = httperr.CodeConflict, "The policy changed or this request is already in progress. Reload and try again."
	}
	httperr.WriteError(w, httperr.New(code, message))
}
func (s *Service) getHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := query(r); err != nil {
		reply(w, nil, err)
		return
	}
	if err := noBody(r); err != nil {
		reply(w, nil, err)
		return
	}
	out, err := s.Get(r.Context())
	reply(w, out, err)
}
func (s *Service) putHTTP(w http.ResponseWriter, r *http.Request) {
	var input Update
	if err := body(w, r, &input); err != nil {
		reply(w, nil, err)
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		reply(w, nil, ErrInvalid)
		return
	}
	out, err := s.Put(r.Context(), input, keys[0])
	reply(w, out, err)
}
func (s *Service) previewHTTP(w http.ResponseWriter, r *http.Request) {
	var input PreviewInput
	if err := body(w, r, &input); err != nil {
		reply(w, nil, err)
		return
	}
	out, err := s.Preview(r.Context(), input)
	reply(w, out, err)
}
func (s *Service) runsHTTP(w http.ResponseWriter, r *http.Request) {
	s.historyHTTP(w, r, false)
}
func (s *Service) auditsHTTP(w http.ResponseWriter, r *http.Request) {
	s.historyHTTP(w, r, true)
}
func (s *Service) historyHTTP(w http.ResponseWriter, r *http.Request, audits bool) {
	values, err := query(r, "cursor", "page_size")
	if err != nil {
		reply(w, nil, err)
		return
	}
	if err = noBody(r); err != nil {
		reply(w, nil, err)
		return
	}
	before, err := decodePosition(values.Get("cursor"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	limit := 100
	if value := values.Get("page_size"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || strconv.Itoa(limit) != value {
			reply(w, nil, ErrInvalid)
			return
		}
	}
	var out any
	if audits {
		out, err = s.Audits(r.Context(), before, limit)
	} else {
		out, err = s.Runs(r.Context(), before, limit)
	}
	reply(w, out, err)
}

// StatusHTTP is mounted behind the user-session route. The service also checks
// the same live session inside its read transaction before projecting dates.
func (s *Service) StatusHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		reply(w, nil, ErrInvalid)
		return
	}
	if _, err := query(r); err != nil {
		reply(w, nil, err)
		return
	}
	if err := noBody(r); err != nil {
		reply(w, nil, err)
		return
	}
	out, err := s.UserStatus(r.Context())
	reply(w, out, err)
}
