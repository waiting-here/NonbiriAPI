package imageactivity

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
)

func RegisterRoutes(users UserRouteRegistrar, admins AdminRouteRegistrar, s *Service) error {
	if missing(users) || missing(admins) || s == nil {
		return ErrInvalid
	}
	userGet := func(fn func(*http.Request, int64) (any, error)) AuthorizedUserHandler {
		return func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
			if !emptyBody(r) {
				writeError(w, ErrInvalid)
				return
			}
			result, err := fn(r, p.UserID)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, result)
		}
	}
	adminGet := func(fn func(*http.Request, int64) (any, error)) AuthorizedAdminHandler {
		return func(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
			if !emptyBody(r) {
				writeError(w, ErrInvalid)
				return
			}
			result, err := fn(r, p.UserID)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, result)
		}
	}
	for _, route := range []struct {
		path string
		fn   func(*http.Request, int64) (any, error)
	}{
		{"/models", func(r *http.Request, u int64) (any, error) {
			limit, cursor, err := listQuery(r)
			if err != nil {
				return nil, err
			}
			return s.UserModels(r.Context(), u, limit, cursor)
		}},
		{"/queue", func(r *http.Request, u int64) (any, error) {
			if r.URL.RawQuery != "" {
				return nil, ErrInvalid
			}
			return s.GetQueue(r.Context(), u)
		}},
		{"/tasks", func(r *http.Request, u int64) (any, error) {
			limit, cursor, err := listQuery(r)
			if err != nil {
				return nil, err
			}
			return s.ListTasks(r.Context(), u, limit, cursor)
		}},
		{"/tasks/{id}", func(r *http.Request, u int64) (any, error) {
			if r.URL.RawQuery != "" {
				return nil, ErrInvalid
			}
			return s.GetTask(r.Context(), u, r.PathValue("id"))
		}},
	} {
		if err := users.RegisterUserRoute(http.MethodGet, userPrefix+route.path, userGet(route.fn)); err != nil {
			return err
		}
	}
	if err := users.RegisterUserRoute(http.MethodPost, userPrefix+"/tasks", func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
		var input SubmitInput
		if err := decodeInput(r, &input, maxJSON, []string{"model_id", "expected_model_revision", "prompt"}); err != nil {
			writeError(w, err)
			return
		}
		key, err := requestKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := s.Submit(r.Context(), p.UserID, key, input)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, result.Value)
	}); err != nil {
		return err
	}
	if err := users.RegisterUserRoute(http.MethodPost, userPrefix+"/tasks/{id}/cancel", func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
		var input struct{}
		if err := decodeInput(r, &input, 1024, nil); err != nil {
			writeError(w, err)
			return
		}
		key, err := requestKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := s.Cancel(r.Context(), p.UserID, r.PathValue("id"), key)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, result.Value)
	}); err != nil {
		return err
	}
	if err := users.RegisterUserRoute(http.MethodGet, userPrefix+"/tasks/{id}/images/{index}", s.serveImage); err != nil {
		return err
	}
	for _, route := range []struct {
		path string
		fn   func(*http.Request, int64) (any, error)
	}{
		{"/upstream", func(r *http.Request, u int64) (any, error) {
			if r.URL.RawQuery != "" {
				return nil, ErrInvalid
			}
			return s.GetUpstream(r.Context(), u)
		}},
		{"/upstream/controls", func(r *http.Request, u int64) (any, error) {
			limit, cursor, err := listQuery(r)
			if err != nil {
				return nil, err
			}
			return s.Controls(r.Context(), u, limit, cursor)
		}},
		{"/models", func(r *http.Request, u int64) (any, error) {
			limit, cursor, err := listQuery(r)
			if err != nil {
				return nil, err
			}
			return s.ListModels(r.Context(), u, true, limit, cursor)
		}},
		{"/models/{id}", func(r *http.Request, u int64) (any, error) {
			if r.URL.RawQuery != "" {
				return nil, ErrInvalid
			}
			return s.GetAdminModel(r.Context(), u, r.PathValue("id"))
		}},
		{"/models/refresh/{id}", func(r *http.Request, u int64) (any, error) {
			if r.URL.RawQuery != "" {
				return nil, ErrInvalid
			}
			return s.GetRefresh(r.Context(), u, r.PathValue("id"))
		}},
	} {
		if err := admins.RegisterAdminRoute(http.MethodGet, adminPrefix+route.path, adminGet(route.fn)); err != nil {
			return err
		}
	}
	if err := admins.RegisterAdminRoute(http.MethodPut, adminPrefix+"/upstream", func(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
		var input UpstreamInput
		if err := decodeInput(r, &input, 1<<20, []string{"expected_revision", "base_url", "secret", "rpm", "concurrency", "per_user_limit", "global_limit", "queue_timeout_seconds", "execution_timeout_seconds", "memory_budget_mib", "image_origins", "adapter"}); err != nil {
			writeError(w, err)
			return
		}
		key, err := requestKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := s.PutUpstream(r.Context(), p.UserID, key, input)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, result.Value)
	}); err != nil {
		return err
	}
	if err := admins.RegisterAdminRoute(http.MethodPut, adminPrefix+"/models/{id}", func(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
		var input ModelInput
		if err := decodeInput(r, &input, 1<<20, []string{"expected_revision", "display_name", "description", "enabled", "price", "parameters", "combinations", "mapping"}); err != nil {
			writeError(w, err)
			return
		}
		key, err := requestKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := s.PutModel(r.Context(), p.UserID, r.PathValue("id"), key, input)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, result.Value)
	}); err != nil {
		return err
	}
	if err := admins.RegisterAdminRoute(http.MethodPost, adminPrefix+"/models/refresh", func(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
		var input struct{}
		if err := decodeInput(r, &input, 1024, nil); err != nil {
			writeError(w, err)
			return
		}
		key, err := requestKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := s.RefreshModels(r.Context(), p.UserID, key)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, result.Value)
	}); err != nil {
		return err
	}
	return admins.RegisterAdminRoute(http.MethodPost, adminPrefix+"/upstream/resume", func(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
		var input ResumeInput
		if err := decodeInput(r, &input, 8192, []string{"control_id", "expected_revision", "reason"}); err != nil {
			writeError(w, err)
			return
		}
		key, err := requestKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := s.Resume(r.Context(), p.UserID, key, input)
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
	if _, err := idempotency.KeyHash(values[0]); err != nil {
		return "", ErrInvalid
	}
	return values[0], nil
}
func emptyBody(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if r.Body == nil {
		return true
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 1))
	return err == nil && len(b) == 0
}
func listQuery(r *http.Request) (int, string, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, "", ErrInvalid
	}
	limit := 20
	cursor := ""
	for key, items := range values {
		if len(items) != 1 {
			return 0, "", ErrInvalid
		}
		switch key {
		case "page_size":
			limit, err = strconv.Atoi(items[0])
			if err != nil || strconv.Itoa(limit) != items[0] || !pageLimit(limit) {
				return 0, "", ErrInvalid
			}
		case "cursor":
			cursor = items[0]
			if len(cursor) > 2048 {
				return 0, "", ErrInvalid
			}
		default:
			return 0, "", ErrInvalid
		}
	}
	return limit, cursor, nil
}
func strictValue(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return ErrInvalid
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrInvalid
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return ErrInvalid
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return ErrInvalid
			}
			keys[name] = true
			if err = strictValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		for decoder.More() {
			if err := strictValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func decodeInput(r *http.Request, out any, limit int, required []string) error {
	if r == nil || r.URL == nil || r.URL.RawQuery != "" || r.Body == nil {
		return ErrInvalid
	}
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" {
		return ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, int64(limit)+1))
	if err != nil || len(raw) > limit {
		return ErrInvalid
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = strictValue(decoder, 0); err != nil {
		return ErrInvalid
	}
	if _, err = decoder.Token(); err != io.EOF {
		return ErrInvalid
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(out); err != nil {
		return ErrInvalid
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return ErrInvalid
	}
	for _, key := range required {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrInvalid
		}
	}
	return nil
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	httperr.WriteJSON(w, http.StatusOK, value)
}
func writeError(w http.ResponseWriter, err error) {
	code, message := httperr.CodeServiceUnavailable, "Image generation is temporarily unavailable."
	switch {
	case errors.Is(err, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "Check the image settings and parameters."
	case errors.Is(err, authz.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "Sign in to use this activity."
	case errors.Is(err, authz.ErrForbidden):
		code, message = httperr.CodeForbidden, "This account cannot perform that image operation."
	case errors.Is(err, ErrNotFound):
		code, message = httperr.CodeNotFound, "The image task or model is unavailable."
	case errors.Is(err, ErrConflict):
		code, message = httperr.CodeConflict, "The model, settings, task state or request identity changed."
	case errors.Is(err, ErrCapacity):
		code, message = httperr.CodeResourceLimitExceeded, "The image queue or memory capacity is full."
	case errors.Is(err, ledger.ErrInsufficientBalance):
		code, message = httperr.CodeInsufficientCredits, "There is not enough activity currency."
	case errors.Is(err, limitedactivities.ErrClosed):
		code, message = httperr.CodeFeatureDisabled, "This activity is not accepting new tasks."
	case errors.Is(err, maintenance.ErrMaintenanceOn):
		code, message = httperr.CodeMaintenance, "The site is under maintenance."
	}
	w.Header().Set("Cache-Control", "no-store")
	httperr.WriteError(w, httperr.New(code, message))
}
func (s *Service) serveImage(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	if !emptyBody(r) || r.URL.RawQuery != "" {
		writeError(w, ErrInvalid)
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 || index >= 16 || strconv.Itoa(index) != r.PathValue("index") {
		writeError(w, ErrInvalid)
		return
	}
	task, err := s.GetTask(r.Context(), p.UserID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	now, err := s.now()
	if err != nil {
		writeError(w, err)
		return
	}
	if task.Status != "succeeded" {
		writeError(w, ErrNotFound)
		return
	}
	img, release, ok := s.memory.image(task.ID, p.UserID, now, index)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	defer release()
	ext := "png"
	if img.mime == "image/jpeg" {
		ext = "jpg"
	} else if img.mime == "image/webp" {
		ext = "webp"
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Type", img.mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(img.data)))
	w.Header().Set("Content-Disposition", `inline; filename="`+task.ID+"-"+strconv.Itoa(index)+"."+ext+`"`)
	_, _ = w.Write(img.data)
}
