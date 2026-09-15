package blackjack

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	randomhttp "github.com/waiting-here/NonbiriAPI/internal/game/randomness/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (s *Service) RegisterRoutes(r host.Registrars) error {
	if r.User == nil || r.Continuation == nil || r.Admin == nil || r.Maintenance == nil {
		return ErrInvariant
	}
	base := "/api/games/blackjack"
	if err := randomhttp.RegisterContinuation(r.Continuation, s.database, s.authorizer, "blackjack", s.now, s.authorizeRandomness); err != nil {
		return err
	}
	if err := r.User.RegisterUserRoute("POST", base+"/queue", func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
		if r.URL.RawQuery != "" {
			writeError(w, ErrInvalid)
			return
		}
		key, ok := requestKey(w, r)
		if !ok {
			return
		}
		var body struct {
			Stake      string `json:"stake"`
			ConfigHash string `json:"config_hash"`
		}
		if !readJSON(w, r, &body) {
			return
		}
		value, err := s.Enqueue(r.Context(), EnqueueInput{Identity: Identity{UserID: p.UserID}, Key: key, Stake: body.Stake, ConfigHash: body.ConfigHash})
		writeMutation(w, value, err)
	}); err != nil {
		return err
	}
	for _, route := range config.Descriptor().Routes {
		if route.Station != "user" || !route.Continuation || route.Pattern == base+"/randomness/{id}" {
			continue
		}
		if err := r.Continuation.RegisterContinuationUserRoute(route.Method, route.Pattern, func(w http.ResponseWriter, r *http.Request, p resources.ContinuationUserPrincipal) {
			identity := Identity{UserID: p.UserID, SessionBinding: p.SessionBinding}
			if route.Method == "GET" {
				if !noBody(w, r) {
					return
				}
				if route.Pattern == base+"/history" {
					page, err := parsePage(r.URL.RawQuery, false)
					if err != nil {
						writeError(w, err)
						return
					}
					value, err := s.History(r.Context(), identity, page)
					writeValue(w, value, err)
					return
				}
				if r.URL.RawQuery != "" {
					writeError(w, ErrInvalid)
					return
				}
				if route.Pattern == base+"/state" {
					value, err := s.Read(r.Context(), identity)
					writeValue(w, value, err)
				} else {
					value, err := s.HistoryDetail(r.Context(), identity, r.PathValue("id"))
					writeValue(w, value, err)
				}
				return
			}
			if r.URL.RawQuery != "" {
				writeError(w, ErrInvalid)
				return
			}
			key, ok := requestKey(w, r)
			if !ok {
				return
			}
			if route.Method == "DELETE" {
				if !noBody(w, r) {
					return
				}
				value, err := s.Leave(r.Context(), identity, key, r.PathValue("id"))
				writeMutation(w, value, err)
				return
			}
			if route.Pattern == base+"/sessions/{id}/emotes" {
				var body struct {
					Emote string `json:"emote"`
				}
				if !readJSON(w, r, &body) {
					return
				}
				value, err := s.Emote(r.Context(), identity, key, r.PathValue("id"), body.Emote)
				writeMutation(w, value, err)
				return
			}
			var body struct {
				Hand     int    `json:"hand"`
				Revision string `json:"revision"`
				Action   string `json:"action"`
			}
			if !readJSON(w, r, &body) {
				return
			}
			value, err := s.Act(r.Context(), ActionInput{Identity: identity, Key: key, SessionID: r.PathValue("id"), Hand: body.Hand, Revision: body.Revision, Action: body.Action})
			writeMutation(w, value, err)
		}); err != nil {
			return err
		}
	}
	adminBase := "/admin/api/games/blackjack/history"
	for _, route := range config.Descriptor().Routes {
		if route.Station != "admin" {
			continue
		}
		if err := r.Admin.RegisterAdminRoute(route.Method, route.Pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if route.Method == "POST" {
				if r.URL.RawQuery != "" {
					writeError(w, ErrInvalid)
					return
				}
				var body struct {
					Dataset string  `json:"dataset"`
					Cursor  *string `json:"cursor"`
				}
				if !readJSON(w, r, &body) {
					return
				}
				in := PageInput{Dataset: body.Dataset}
				if body.Cursor != nil {
					if *body.Cursor == "" {
						writeError(w, ErrInvalid)
						return
					}
					in.Cursor = *body.Cursor
				}
				value, err := s.AdminExport(r.Context(), in)
				writeValue(w, value, err)
				return
			}
			if !noBody(w, r) {
				return
			}
			in, err := parsePage(r.URL.RawQuery, true)
			if err != nil {
				writeError(w, err)
				return
			}
			if route.Pattern == adminBase {
				value, err := s.AdminHistory(r.Context(), in)
				writeValue(w, value, err)
			} else {
				if in.Cursor != "" || in.Limit != 0 {
					writeError(w, ErrInvalid)
					return
				}
				value, err := s.AdminDetail(r.Context(), in.Dataset, r.PathValue("id"))
				writeValue(w, value, err)
			}
		})); err != nil {
			return err
		}
	}
	return r.Maintenance.Register("blackjack_session", s.ContinuationRegistration())
}

// All mutation bodies are small, flat objects with exact required keys.
// Token-by-token reading rejects duplicate and case-mismatched field names.
func readJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Body == nil {
		writeError(w, ErrInvalid)
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeError(w, ErrInvalid)
		}
		return false
	}
	if !utf8.Valid(body) {
		writeError(w, ErrInvalid)
		return false
	}
	typ := reflect.TypeOf(out).Elem()
	fields := map[string]reflect.Type{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fields[field.Tag.Get("json")] = field.Type
	}
	d := json.NewDecoder(bytes.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		writeError(w, ErrInvalid)
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			writeError(w, ErrInvalid)
			return false
		}
		name, ok := token.(string)
		field, exists := fields[name]
		if !ok || !exists || seen[name] {
			writeError(w, ErrInvalid)
			return false
		}
		seen[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) && field.Kind() != reflect.Pointer {
			writeError(w, ErrInvalid)
			return false
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') || len(seen) != len(fields) {
		writeError(w, ErrInvalid)
		return false
	}
	if _, err = d.Token(); err != io.EOF || json.Unmarshal(body, out) != nil {
		writeError(w, ErrInvalid)
		return false
	}
	return true
}
func noBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(body) > 0 {
		writeError(w, ErrInvalid)
		return false
	}
	return true
}
func requestKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		writeError(w, ErrInvalid)
		return "", false
	}
	if _, err := idempotency.KeyHash(values[0]); err != nil {
		writeError(w, ErrInvalid)
		return "", false
	}
	return values[0], true
}
func parsePage(raw string, admin bool) (PageInput, error) {
	out := PageInput{}
	if admin {
		out.Dataset = "recent"
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return out, ErrInvalid
	}
	for k, v := range values {
		if len(v) != 1 || v[0] == "" {
			return out, ErrInvalid
		}
		switch k {
		case "cursor":
			out.Cursor = v[0]
		case "limit":
			n, err := strconv.Atoi(v[0])
			if err != nil || n < 1 || n > 50 || strconv.Itoa(n) != v[0] {
				return out, ErrInvalid
			}
			out.Limit = n
		case "dataset":
			if !admin {
				return out, ErrInvalid
			}
			out.Dataset = v[0]
		default:
			return out, ErrInvalid
		}
	}
	if admin && out.Dataset != "recent" && out.Dataset != "anonymous" {
		return out, ErrInvalid
	}
	return out, nil
}
func writeValue(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	body, err := marshal(value)
	if err != nil {
		writeError(w, ErrInvariant)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
func writeMutation(w http.ResponseWriter, value MutationResult, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if value.Status != 204 {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(value.Status)
	if value.Status != 204 {
		_, _ = w.Write(value.Body)
	}
}
func writeError(w http.ResponseWriter, err error) {
	code, message := httperr.CodeInternal, "request failed"
	switch {
	case errors.Is(err, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "invalid request"
	case errors.Is(err, ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "authentication required"
	case errors.Is(err, ErrForbidden):
		code, message = httperr.CodeForbidden, "forbidden"
	case errors.Is(err, ErrNotFound):
		code, message = httperr.CodeNotFound, "resource not found"
	case errors.Is(err, ErrConflict):
		code, message = httperr.CodeConflict, "state conflict"
	case errors.Is(err, ErrLimit), errors.Is(err, ErrRateLimited):
		code, message = httperr.CodeRateLimited, "game request limit exceeded"
	case errors.Is(err, ErrInsufficient):
		code, message = httperr.CodeInsufficientCredits, "insufficient credits"
	case errors.Is(err, ErrMaintenance):
		code, message = httperr.CodeMaintenance, "maintenance mode"
	case errors.Is(err, ErrUnavailable):
		code, message = httperr.CodeServiceUnavailable, "service unavailable"
	}
	w.Header().Set("Cache-Control", "no-store")
	httperr.WriteError(w, httperr.New(code, message))
}
