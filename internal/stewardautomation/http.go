package stewardautomation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL != nil && r.URL.EscapedPath() == r.URL.Path {
		route := matchAutomationRoute(r.URL.Path)
		if route.personal || len(route.methods) > 0 && route.kind != "failure_policy" && route.kind != "charity_write" && (route.kind != "donations" || r.Method != http.MethodPost) {
			s.serveAutomation(w, r, route)
			return
		}
	}
	if r.URL == nil || r.URL.EscapedPath() != r.URL.Path || (r.URL.Path != DonationsPath && r.URL.Path != BindingsPath && r.URL.Path != FailurePolicyPath) {
		httperr.WriteError(w, httperr.New(httperr.CodeNotFound, "not found"))
		return
	}
	policyRoute := r.URL.Path == FailurePolicyPath
	if policyRoute && r.Method != http.MethodGet && r.Method != http.MethodPatch || !policyRoute && r.Method != http.MethodPost {
		httperr.WriteError(w, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
		return
	}
	if !(policyRoute && r.Method == http.MethodGet) && (r.URL.RawQuery != "" || r.URL.ForceQuery) {
		writeError(w, errInvalid)
		return
	}
	identity, ok := authz.StewardCallerFromContext(r.Context())
	if !ok {
		writeError(w, authz.ErrUnauthorized)
		return
	}
	tx, err := s.begin(r.Context(), identity.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = tx.Rollback()
	if !s.admit(identity.UserID) {
		httperr.WriteError(w, httperr.New(httperr.CodeRateLimited, "automation concurrency limit reached"))
		return
	}
	defer s.release(identity.UserID)
	timeout := s.bindingTimeout
	if r.URL.Path == DonationsPath || policyRoute {
		timeout = s.createTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	s.track(identity.UserID, cancel)
	// Bound body reads as well as business work on real HTTP connections.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(timeout))
	defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
	if policyRoute {
		s.failurePolicyHTTP(w, r.WithContext(ctx), identity.UserID)
		return
	}
	body, err := httpapi.ReadBody(w, r, httpapi.BodyOptions{
		MaxBytes: idempotency.MaxControlBodyBytes,
		Validate: func(body []byte) error { return strictjson.ValidateObjectWithFieldLimit(body, 16384) },
	})
	defer clear(body)
	if err != nil {
		if errors.Is(err, httpapi.ErrTooLarge) {
			httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeError(w, errInvalid)
		}
		return
	}
	if r.URL.Path == DonationsPath {
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			writeError(w, errInvalid)
			return
		}
		if _, err := idempotency.KeyHash(keys[0]); err != nil {
			writeError(w, errInvalid)
			return
		}
		var input createInput
		if decode(body, &input) != nil {
			writeError(w, errInvalid)
			return
		}
		canonical, err := canonicalJSON(body)
		if err != nil {
			writeError(w, errInvalid)
			return
		}
		defer clear(canonical)
		out, err := s.create(ctx, identity.UserID, keys[0], canonical, input)
		if r.Context().Err() != nil {
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, out)
		return
	}
	var input bindingInput
	if decode(body, &input) != nil {
		writeError(w, errInvalid)
		return
	}
	out, status, err := s.bind(ctx, identity.UserID, input)
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, status, encoded)
}

func decode(body []byte, input any) error {
	if err := httpapi.DecodeJSON(body, input); err != nil {
		return err
	}
	// Destination pointers cannot distinguish omitted and forbidden null values;
	// retain the independent domain null policy after the shared decode boundary.
	var values any
	if err := httpapi.DecodeJSONWithNumbers(body, &values); err != nil {
		return err
	}
	return validateNulls(values)
}

func validateNulls(value any) error {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if child == nil {
				switch key {
				case "authorized_expires_at", "expires_at", "price_limit", "calls_limit", "tokens_limit", "id", "alignment", "anchor_local", "week_starts_on":
					continue
				default:
					return errInvalid
				}
			}
			if err := validateNulls(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if child == nil {
				return errInvalid
			}
			if err := validateNulls(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func canonicalJSON(body []byte) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	if len(body) > idempotency.MaxResponseBytes {
		writeError(w, errors.New("response budget exceeded"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
