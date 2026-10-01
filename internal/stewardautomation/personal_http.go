package stewardautomation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func (s *Service) authorizeAutomation(ctx context.Context, userID int64, personal bool) error {
	var err error
	if personal {
		tx, beginErr := s.beginPersonal(ctx, userID)
		err = beginErr
		if tx != nil {
			tx.Rollback()
		}
	} else {
		tx, beginErr := s.begin(ctx, userID)
		err = beginErr
		if tx != nil {
			tx.Rollback()
		}
	}
	return err
}
func (s *Service) serveAutomation(w http.ResponseWriter, r *http.Request, route automationRoute) {
	allowed := false
	for _, method := range route.methods {
		allowed = allowed || method == r.Method
	}
	if !allowed {
		httperr.WriteError(w, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
		return
	}
	userID := int64(0)
	if route.personal {
		identity, ok := authz.PersonalCallerFromContext(r.Context())
		if ok {
			userID = identity.UserID
		}
	} else {
		identity, ok := authz.StewardCallerFromContext(r.Context())
		if ok {
			userID = identity.UserID
		}
	}
	if userID == 0 {
		writePersonalError(w, authz.ErrUnauthorized)
		return
	}
	timeout := 5 * time.Second
	if r.Method == http.MethodPost {
		timeout = s.createTimeout
		if route.kind == "binding_append" {
			timeout = s.bindingTimeout
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(timeout))
	defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
	if err := s.authorizeAutomation(ctx, userID, route.personal); err != nil {
		writePersonalError(w, err)
		return
	}
	if !s.admit(userID) {
		httperr.WriteError(w, httperr.New(httperr.CodeRateLimited, "automation concurrency limit reached"))
		return
	}
	defer s.release(userID)
	s.track(userID, cancel)
	ids := make([]int64, len(route.ids))
	for i, value := range route.ids {
		id, err := numericID(value)
		if err != nil {
			writePersonalError(w, errInvalid)
			return
		}
		ids[i] = id
	}
	if r.Method == http.MethodGet {
		if len(r.Header.Values("Idempotency-Key")) != 0 {
			writePersonalError(w, errInvalid)
			return
		}
		body, err := httpapi.ReadBody(w, r, httpapi.BodyOptions{MaxBytes: 1})
		if err != nil || len(body) != 0 {
			writePersonalError(w, errInvalid)
			return
		}
		list := route.kind == "endpoints" || route.kind == "models" || route.kind == "keys" || route.kind == "bindings" || route.kind == "donations" || route.kind == "donation_keys" || route.kind == "donation_catalog" || route.kind == "charity_models" || route.kind == "charity_bindings" || route.kind == "charity_candidates"
		page, q, err := automationQuery(r.URL, list)
		if err != nil {
			writePersonalError(w, err)
			return
		}
		out, err := s.readAutomation(ctx, userID, route, ids, q, page)
		if r.Context().Err() != nil {
			return
		}
		if err != nil {
			writePersonalError(w, err)
			return
		}
		if err = s.authorizeAutomation(ctx, userID, route.personal); err != nil {
			writePersonalError(w, err)
			return
		}
		s.writeAutomationJSON(w, http.StatusOK, out, 256*1024)
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writePersonalError(w, errInvalid)
		return
	}
	headers := r.Header.Values("Idempotency-Key")
	if len(headers) != 1 {
		writePersonalError(w, errInvalid)
		return
	}
	if _, err := idempotency.KeyHash(headers[0]); err != nil {
		writePersonalError(w, errInvalid)
		return
	}
	body, err := httpapi.ReadBody(w, r, httpapi.BodyOptions{MaxBytes: idempotency.MaxControlBodyBytes, ContentType: httpapi.JSONContentType, Validate: func(body []byte) error { return strictPersonalJSON(body) }})
	defer clear(body)
	if err != nil {
		if errors.Is(err, httpapi.ErrTooLarge) {
			httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writePersonalError(w, errInvalid)
		}
		return
	}
	var out batchResult
	var status int
	switch route.kind {
	case "key_import":
		var input importInput
		if decode(body, &input) != nil {
			writePersonalError(w, errInvalid)
			return
		}
		defer func() {
			for i := range input.Keys {
				input.Keys[i].Secret = nil
			}
		}()
		out, status, err = s.importKeys(ctx, userID, ids[0], headers[0], input)
	case "binding_append":
		var input appendInput
		if decode(body, &input) != nil {
			writePersonalError(w, errInvalid)
			return
		}
		out, status, err = s.appendBindings(ctx, userID, ids[0], headers[0], input)
	default:
		err = errInvalid
	}
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		writePersonalError(w, err)
		return
	}
	s.writeAutomationJSON(w, status, out, idempotency.MaxResponseBytes)
}
func automationQuery(u *url.URL, list bool) (pagination.Request, string, error) {
	if u.ForceQuery {
		return pagination.Request{}, "", errInvalid
	}
	if !list {
		if u.RawQuery != "" {
			return pagination.Request{}, "", errInvalid
		}
		return pagination.Default(), "", nil
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return pagination.Request{}, "", errInvalid
	}
	for key, entries := range values {
		if len(entries) != 1 || (key != "q" && key != "page" && key != "page_size") {
			return pagination.Request{}, "", errInvalid
		}
	}
	page, _, err := pagination.Parse(values)
	q := values.Get("q")
	if err != nil || !utf8.ValidString(q) || utf8.RuneCountInString(q) > 200 {
		return pagination.Request{}, "", errInvalid
	}
	return page, q, nil
}
func (s *Service) writeAutomationJSON(w http.ResponseWriter, status int, value any, limit int) {
	body, err := json.Marshal(value)
	if err != nil {
		writePersonalError(w, err)
		return
	}
	if len(body) > limit {
		httperr.WriteError(w, httperr.New(httperr.CodeResourceLimitExceeded, "response exceeds the configured byte budget; request a smaller page"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
func writePersonalError(w http.ResponseWriter, err error) {
	if errors.Is(err, errBatchCapacity) {
		httperr.WriteError(w, httperr.New(httperr.CodeRateLimited, "automation batch capacity reached"))
		return
	}
	code, message := safePersonalError(err)
	if code == "incomplete" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusGatewayTimeout)
		_ = json.NewEncoder(w).Encode(httperr.Envelope{Error: httperr.New(httperr.CodeServiceUnavailable, message)})
		return
	}
	httperr.WriteError(w, httperr.New(code, message))
}
