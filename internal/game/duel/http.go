package duel

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	randomhttp "github.com/waiting-here/NonbiriAPI/internal/game/randomness/httpapi"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (s *Service) RegisterRoutes(user resources.UserRouteRegistrar, continuation resources.ContinuationUserRouteRegistrar) error {
	if user == nil || continuation == nil {
		return ErrInvariant
	}
	base := "/api/games/" + s.rules.ID()
	if err := randomhttp.RegisterContinuation(continuation, s.database, s.authorizer, s.rules.ID(), s.now, s.authorizeRandomness); err != nil {
		return err
	}
	if err := user.RegisterUserRoute("POST", base+"/queue", func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
		if r.URL.RawQuery != "" {
			writeError(w, ErrInvalidRequest)
			return
		}
		key, ok := requestKey(w, r)
		if !ok {
			return
		}
		var body enqueueBody
		if !readJSON(w, r, &body) {
			return
		}
		if s.rules.ID() == "bidding" && body.Loadout != nil || s.rules.ID() == "likes" && body.Loadout == nil {
			writeError(w, ErrInvalidRequest)
			return
		}
		ip, err := netip.ParseAddr(httpmw.ClientIP(r))
		if err != nil {
			writeError(w, ErrInvalidRequest)
			return
		}
		result, err := s.Enqueue(r.Context(), EnqueueInput{Identity: Identity{UserID: p.UserID}, IdempotencyKey: key, Mode: body.Mode, ExpectedTermsHash: body.ExpectedTermsHash, DeviceToken: body.DeviceToken, CanonicalSourceIP: ip.As16(), Loadout: body.Loadout})
		writeMutation(w, result, err)
	}); err != nil {
		return err
	}
	for _, route := range s.descriptor.Routes {
		if route.Station != "user" || !route.Continuation || route.Pattern == base+"/randomness/{id}" {
			continue
		}
		path := route.Pattern
		method := route.Method
		if err := continuation.RegisterContinuationUserRoute(method, path, func(w http.ResponseWriter, r *http.Request, p resources.ContinuationUserPrincipal) {
			identity := Identity{UserID: p.UserID, SessionBinding: p.SessionBinding}
			if method == "GET" {
				if !noBody(w, r) {
					return
				}
				if path == base+"/state" {
					if r.URL.RawQuery != "" {
						writeError(w, ErrInvalidRequest)
						return
					}
					value, err := s.Read(r.Context(), identity)
					writeValue(w, value, err)
					return
				}
				if path == base+"/catalog" {
					if r.URL.RawQuery != "" {
						writeError(w, ErrInvalidRequest)
						return
					}
					tx, _, err := s.beginRead(r.Context())
					if err != nil {
						writeError(w, err)
						return
					}
					defer tx.Rollback()
					if err := s.authorize(r.Context(), tx, identity); err != nil {
						writeError(w, err)
						return
					}
					value, err := s.PublicCatalog()
					writeValue(w, value, err)
					return
				}
				if path == base+"/history/{id}" {
					if r.URL.RawQuery != "" {
						writeError(w, ErrInvalidRequest)
						return
					}
					value, err := s.HistoryDetail(r.Context(), identity, r.PathValue("id"))
					writeValue(w, value, err)
					return
				}
				page, err := parsePage(r.URL.RawQuery, path == base+"/history")
				if err != nil {
					writeError(w, err)
					return
				}
				if path == base+"/history" {
					value, err := s.History(r.Context(), identity, page)
					writeValue(w, value, err)
					return
				}
				value, err := s.Rounds(r.Context(), identity, r.PathValue("id"), page, strings.Contains(path, "/sessions/"))
				writeValue(w, value, err)
				return
			}
			if r.URL.RawQuery != "" {
				writeError(w, ErrInvalidRequest)
				return
			}
			key, ok := requestKey(w, r)
			if !ok {
				return
			}
			if method == "DELETE" {
				var body cancelBody
				if !readJSON(w, r, &body) {
					return
				}
				value, err := s.CancelQueue(r.Context(), CancelInput{Identity: identity, IdempotencyKey: key, QueueID: r.PathValue("id"), ExpectedRevision: body.ExpectedRevision})
				writeMutation(w, value, err)
				return
			}
			if strings.HasSuffix(path, "/surrender") {
				var body surrenderBody
				if !readJSON(w, r, &body) {
					return
				}
				value, err := s.Surrender(r.Context(), ActionInput{Identity: identity, IdempotencyKey: key, SessionID: r.PathValue("id"), PhaseSeq: body.PhaseSeq})
				writeMutation(w, value, err)
				return
			}
			var body actionBody
			if !readJSON(w, r, &body) {
				return
			}
			value, err := s.Action(r.Context(), ActionInput{Identity: identity, IdempotencyKey: key, SessionID: r.PathValue("id"), PhaseSeq: body.PhaseSeq, Action: body.Action})
			writeMutation(w, value, err)
		}); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) PublicCatalog() (json.RawMessage, error) {
	if s.rules.ID() != "likes" {
		return nil, ErrNotFound
	}
	modes := map[string]json.RawMessage{}
	hashes := map[string]string{}
	design := ""
	schema := 0
	for _, mode := range s.descriptor.Modes {
		c, err := s.rules.Catalog(mode)
		if err != nil {
			return nil, err
		}
		modes[mode] = c.JSON
		hashes[mode] = c.Hash
		design = c.DesignVersion
		schema = c.SchemaVersion
	}
	body, err := Encode(hashes)
	if err != nil {
		return nil, err
	}
	return Encode(struct {
		RulesVersion  int                        `json:"rules_version"`
		DesignVersion string                     `json:"design_version"`
		SchemaVersion int                        `json:"schema_version"`
		ContentHash   string                     `json:"content_hash"`
		Modes         map[string]json.RawMessage `json:"modes"`
	}{1, design, schema, digest(body), modes})
}
func readJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Body == nil {
		writeError(w, ErrInvalidRequest)
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<10))
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeError(w, ErrInvalidRequest)
		}
		return false
	}
	if Decode(body, out) != nil {
		writeError(w, ErrInvalidRequest)
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
		writeError(w, ErrInvalidRequest)
		return false
	}
	return true
}
func requestKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		writeError(w, ErrInvalidRequest)
		return "", false
	}
	if _, err := idempotency.KeyHash(values[0]); err != nil {
		writeError(w, ErrInvalidRequest)
		return "", false
	}
	return values[0], true
}
func parsePage(raw string, mode bool) (PageInput, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return PageInput{}, ErrInvalidRequest
	}
	page := PageInput{}
	for key, v := range values {
		if len(v) != 1 || v[0] == "" {
			return page, ErrInvalidRequest
		}
		switch key {
		case "cursor":
			page.Cursor = v[0]
		case "limit":
			limit, err := strconv.Atoi(v[0])
			if err != nil || limit < 1 || strconv.Itoa(limit) != v[0] {
				return page, ErrInvalidRequest
			}
			page.Limit = limit
		case "mode":
			if !mode {
				return page, ErrInvalidRequest
			}
			page.Mode = v[0]
		default:
			return page, ErrInvalidRequest
		}
	}
	return page, nil
}
func writeValue(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	body, err := json.Marshal(value)
	if err != nil {
		writeError(w, ErrInvariant)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
func writeMutation(w http.ResponseWriter, result MutationResult, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if result.Status != 204 {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(result.Status)
	if result.Status != 204 {
		_, _ = w.Write(result.Body)
	}
}
func writeError(w http.ResponseWriter, err error) {
	code, message := httperr.CodeInternal, "request failed"
	switch {
	case errors.Is(err, ErrInvalidRequest):
		code, message = httperr.CodeInvalidRequest, "invalid request"
	case errors.Is(err, ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "authentication required"
	case errors.Is(err, ErrForbidden):
		code, message = httperr.CodeForbidden, "forbidden"
	case errors.Is(err, ErrNotFound):
		code, message = httperr.CodeNotFound, "resource not found"
	case errors.Is(err, ErrConflict):
		code, message = httperr.CodeConflict, "state conflict"
	case errors.Is(err, ErrRateLimited), errors.Is(err, ErrResourceLimit):
		code, message = httperr.CodeRateLimited, "game request limit exceeded"
	case errors.Is(err, ErrInsufficientCredits):
		code, message = httperr.CodeInsufficientCredits, "insufficient credits"
	case errors.Is(err, ErrMaintenance):
		code, message = httperr.CodeMaintenance, "maintenance mode"
	case errors.Is(err, ErrUnavailable):
		code, message = httperr.CodeServiceUnavailable, "service unavailable"
	}
	w.Header().Set("Cache-Control", "no-store")
	httperr.WriteError(w, httperr.New(code, message))
}
