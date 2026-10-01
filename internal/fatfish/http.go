package fatfish

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const userPrefix = "/api/limited-activities/fat-fish"
const adminPrefix = "/admin/api/limited-activities/fat-fish"
const capabilityHeader = "X-FatFish-Tab-Capability"

func RegisterUserRoutes(routes limitedactivities.UserRouteRegistrar, s *Service) error {
	if routes == nil || s == nil {
		return ErrInvalid
	}
	get := func(fn func(*http.Request, limitedactivities.UserPrincipal) (any, error)) limitedactivities.AuthorizedUserHandler {
		return func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
			if !emptyBody(r) {
				writeHTTPError(w, ErrInvalid)
				return
			}
			v, err := fn(r, p)
			writeHTTPResult(w, http.StatusOK, v, err)
		}
	}
	mutation := func(max int, fn func(*http.Request, limitedactivities.UserPrincipal, string, []byte) (any, error)) limitedactivities.AuthorizedUserHandler {
		return func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
			if r.URL.RawQuery != "" {
				writeHTTPError(w, ErrInvalid)
				return
			}
			key, err := idempotencyKey(r)
			if err != nil {
				writeHTTPError(w, err)
				return
			}
			raw, err := readBody(r, max)
			if err != nil {
				writeHTTPError(w, err)
				return
			}
			defer clear(raw)
			v, err := fn(r, p, key, raw)
			status := http.StatusOK
			if r.URL.Path == userPrefix+"/challenges/"+r.PathValue("id")+"/submit" {
				status = submitHTTPStatus(v)
			}
			writeHTTPResult(w, status, v, err)
		}
	}
	for _, route := range []struct {
		method, path string
		handler      limitedactivities.AuthorizedUserHandler
	}{
		{http.MethodGet, userPrefix + "/periods", func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
			page, err := collectionPage(r)
			if err != nil {
				writeHTTPError(w, err)
				return
			}
			v, err := s.ListPeriods(r.Context(), p.UserID, page)
			writeHTTPResult(w, http.StatusOK, v, err)
		}},
		{http.MethodGet, userPrefix + "/periods/{p}", get(func(r *http.Request, p limitedactivities.UserPrincipal) (any, error) {
			return s.Period(r.Context(), p.UserID, r.PathValue("p"))
		})},
		{http.MethodGet, userPrefix + "/periods/{p}/nodes/{n}", get(func(r *http.Request, p limitedactivities.UserPrincipal) (any, error) {
			return s.Node(r.Context(), p.UserID, r.PathValue("p"), r.PathValue("n"))
		})},
		{http.MethodGet, userPrefix + "/challenges/current", get(func(r *http.Request, p limitedactivities.UserPrincipal) (any, error) {
			return s.CurrentChallenge(r.Context(), p.UserID, r.Header.Get(capabilityHeader))
		})},
		{http.MethodGet, userPrefix + "/challenges/{id}", get(func(r *http.Request, p limitedactivities.UserPrincipal) (any, error) {
			return s.Challenge(r.Context(), p.UserID, r.PathValue("id"), r.Header.Get(capabilityHeader))
		})},
		{http.MethodGet, userPrefix + "/history", func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
			if !emptyBodyOnly(r) {
				writeHTTPError(w, ErrInvalid)
				return
			}
			if _, err := singleQueryValues(r, "limit", "page"); err != nil {
				writeHTTPError(w, err)
				return
			}
			limit, err := queryInt(r, "limit", 20)
			if err != nil {
				writeHTTPError(w, err)
				return
			}
			page, err := queryInt(r, "page", 1)
			if err != nil {
				writeHTTPError(w, err)
				return
			}
			v, err := s.History(r.Context(), p.UserID, limit, page)
			writeHTTPResult(w, http.StatusOK, v, err)
		}},
		{http.MethodGet, userPrefix + "/periods/{p}/leaderboard", func(w http.ResponseWriter, r *http.Request, p limitedactivities.UserPrincipal) {
			if !emptyBodyOnly(r) {
				writeHTTPError(w, ErrInvalid)
				return
			}
			if _, err := singleQueryValues(r, "page", "page_size", "node_id"); err != nil {
				writeHTTPError(w, err)
				return
			}
			page, err := queryInt(r, "page", 1)
			if err != nil {
				writeHTTPError(w, err)
				return
			}
			size, err := queryInt(r, "page_size", 20)
			if err != nil {
				writeHTTPError(w, err)
				return
			}
			v, err := s.Leaderboard(r.Context(), p.UserID, r.PathValue("p"), r.URL.Query().Get("node_id"), page, size)
			writeHTTPResult(w, http.StatusOK, v, err)
		}},
		{http.MethodPost, userPrefix + "/periods/{p}/nodes/{n}/unlock", mutation(4096, func(r *http.Request, p limitedactivities.UserPrincipal, key string, raw []byte) (any, error) {
			var in UnlockInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.Unlock(r.Context(), p.UserID, r.PathValue("p"), r.PathValue("n"), in, key)
		})},
		{http.MethodPost, userPrefix + "/challenges/prepare", mutation(4096, func(r *http.Request, p limitedactivities.UserPrincipal, key string, raw []byte) (any, error) {
			var in PrepareInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.Prepare(r.Context(), p.UserID, in, key)
		})},
		{http.MethodPost, userPrefix + "/challenges/{id}/start", mutation(4096, func(r *http.Request, p limitedactivities.UserPrincipal, key string, raw []byte) (any, error) {
			var in StartInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.Start(r.Context(), p.UserID, r.PathValue("id"), in, key)
		})},
		{http.MethodPost, userPrefix + "/challenges/{id}/submit", mutation((4<<20)+1024, func(r *http.Request, p limitedactivities.UserPrincipal, key string, raw []byte) (any, error) {
			in, err := decodeSubmit(raw)
			defer clear(in.Inputs)
			if err != nil {
				return nil, err
			}
			return s.Submit(r.Context(), p.UserID, r.PathValue("id"), in, key)
		})},
		{http.MethodPost, userPrefix + "/challenges/{id}/abandon", mutation(4096, func(r *http.Request, p limitedactivities.UserPrincipal, key string, raw []byte) (any, error) {
			var in AbandonInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.Abandon(r.Context(), p.UserID, r.PathValue("id"), in, key)
		})},
	} {
		if err := routes.RegisterUserRoute(route.method, route.path, route.handler); err != nil {
			return err
		}
	}
	return nil
}

func RegisterAdminRoutes(routes limitedactivities.AdminRouteRegistrar, s *Service) error {
	if routes == nil || s == nil {
		return ErrInvalid
	}
	get := func(fn func(*http.Request, limitedactivities.AdminPrincipal) (any, error)) limitedactivities.AuthorizedAdminHandler {
		return func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
			if !emptyBody(r) {
				writeHTTPError(w, ErrInvalid)
				return
			}
			v, err := fn(r, p)
			writeHTTPResult(w, http.StatusOK, v, err)
		}
	}
	mutation := func(max int, fn func(*http.Request, limitedactivities.AdminPrincipal, string, []byte) (any, error)) limitedactivities.AuthorizedAdminHandler {
		return func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
			if r.URL.RawQuery != "" {
				writeHTTPError(w, ErrInvalid)
				return
			}
			key, err := idempotencyKey(r)
			if err != nil {
				writeHTTPError(w, err)
				return
			}
			raw, err := readBody(r, max)
			if err != nil {
				writeHTTPError(w, err)
				return
			}
			defer clear(raw)
			v, err := fn(r, p, key, raw)
			status := http.StatusOK
			if r.URL.Path == adminPrefix+"/playtests/"+r.PathValue("id")+"/submit" {
				status = submitHTTPStatus(v)
			}
			writeHTTPResult(w, status, v, err)
		}
	}
	for _, route := range []struct {
		method, path string
		handler      limitedactivities.AuthorizedAdminHandler
	}{
		{http.MethodGet, adminPrefix + "/levels", func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
			page, err := collectionPage(r)
			if err != nil {
				writeHTTPError(w, err)
				return
			}
			v, err := s.Levels(r.Context(), p.UserID, page)
			writeHTTPResult(w, http.StatusOK, v, err)
		}},
		{http.MethodGet, adminPrefix + "/levels/{id}", get(func(r *http.Request, p limitedactivities.AdminPrincipal) (any, error) {
			return s.Level(r.Context(), p.UserID, r.PathValue("id"))
		})},
		{http.MethodGet, adminPrefix + "/levels/{id}/versions", func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
			page, err := collectionPage(r)
			if err != nil {
				writeHTTPError(w, err)
				return
			}
			v, err := s.Versions(r.Context(), p.UserID, r.PathValue("id"), page)
			writeHTTPResult(w, http.StatusOK, v, err)
		}},
		{http.MethodGet, adminPrefix + "/versions/{id}", get(func(r *http.Request, p limitedactivities.AdminPrincipal) (any, error) {
			return s.Version(r.Context(), p.UserID, r.PathValue("id"))
		})},
		{http.MethodGet, adminPrefix + "/levels/{id}/export", get(func(r *http.Request, p limitedactivities.AdminPrincipal) (any, error) {
			return s.ExportLevel(r.Context(), p.UserID, r.PathValue("id"))
		})},
		{http.MethodGet, adminPrefix + "/periods", func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
			page, err := collectionPage(r)
			if err != nil {
				writeHTTPError(w, err)
				return
			}
			v, err := s.AdminPeriods(r.Context(), p.UserID, page)
			writeHTTPResult(w, http.StatusOK, v, err)
		}},
		{http.MethodGet, adminPrefix + "/periods/{id}", get(func(r *http.Request, p limitedactivities.AdminPrincipal) (any, error) {
			return s.AdminPeriod(r.Context(), p.UserID, r.PathValue("id"))
		})},
		{http.MethodGet, adminPrefix + "/periods/{id}/nodes/{node}", get(func(r *http.Request, p limitedactivities.AdminPrincipal) (any, error) {
			return s.AdminNode(r.Context(), p.UserID, r.PathValue("id"), r.PathValue("node"))
		})},
		{http.MethodGet, adminPrefix + "/periods/{id}/layout", get(func(r *http.Request, p limitedactivities.AdminPrincipal) (any, error) {
			return s.GraphLayout(r.Context(), p.UserID, r.PathValue("id"))
		})},
		{http.MethodGet, adminPrefix + "/periods/{id}/validate", get(func(r *http.Request, p limitedactivities.AdminPrincipal) (any, error) {
			return s.ValidatePeriod(r.Context(), p.UserID, r.PathValue("id"))
		})},
		{http.MethodGet, adminPrefix + "/playtests/current", get(func(r *http.Request, p limitedactivities.AdminPrincipal) (any, error) {
			return s.CurrentPlaytest(r.Context(), p.UserID)
		})},
		{http.MethodGet, adminPrefix + "/playtests", func(w http.ResponseWriter, r *http.Request, p limitedactivities.AdminPrincipal) {
			values, queryErr := singleQueryValues(r, "version_id")
			if !emptyBodyOnly(r) || queryErr != nil || len(values) != 1 {
				writeHTTPError(w, ErrInvalid)
				return
			}
			v, err := s.Playtests(r.Context(), p.UserID, r.URL.Query().Get("version_id"))
			writeHTTPResult(w, http.StatusOK, v, err)
		}},
		{http.MethodGet, adminPrefix + "/playtests/{id}", get(func(r *http.Request, p limitedactivities.AdminPrincipal) (any, error) {
			return s.PlaytestChallenge(r.Context(), p.UserID, r.PathValue("id"), r.Header.Get(capabilityHeader))
		})},
		{http.MethodPost, adminPrefix + "/levels", mutation((256<<10)+8192, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var in LevelInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.SaveLevel(r.Context(), p.UserID, "", in, key)
		})},
		{http.MethodPost, adminPrefix + "/levels/import", mutation((256<<10)+8192, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var in LevelInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.SaveLevel(r.Context(), p.UserID, "", in, key)
		})},
		{http.MethodPut, adminPrefix + "/levels/{id}", mutation((256<<10)+8192, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var in LevelInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.SaveLevel(r.Context(), p.UserID, r.PathValue("id"), in, key)
		})},
		{http.MethodPost, adminPrefix + "/levels/{id}/versions", mutation(4096, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var in struct {
				ExpectedRevision string `json:"expected_revision"`
			}
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.PublishVersion(r.Context(), p.UserID, r.PathValue("id"), in.ExpectedRevision, key)
		})},
		{http.MethodDelete, adminPrefix + "/levels/{id}", mutation(4096, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var in struct {
				ExpectedRevision string `json:"expected_revision"`
			}
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.DeleteLevel(r.Context(), p.UserID, r.PathValue("id"), in.ExpectedRevision, key)
		})},
		{http.MethodPost, adminPrefix + "/levels/validate", mutation((256<<10)+4096, func(r *http.Request, p limitedactivities.AdminPrincipal, _ string, raw []byte) (any, error) {
			var in struct {
				Level json.RawMessage `json:"level"`
			}
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.ValidateLevel(r.Context(), p.UserID, in.Level)
		})},
		{http.MethodPost, adminPrefix + "/periods", mutation(16384, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var in PeriodInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.SavePeriod(r.Context(), p.UserID, "", in, key)
		})},
		{http.MethodPut, adminPrefix + "/periods/{id}", mutation(16384, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var in PeriodInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.SavePeriod(r.Context(), p.UserID, r.PathValue("id"), in, key)
		})},
		{http.MethodPut, adminPrefix + "/periods/{id}/layout", mutation(32768, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var input GraphLayoutInput
			if err := decodeJSON(raw, &input); err != nil {
				return nil, err
			}
			return s.SaveGraphLayout(r.Context(), p.UserID, r.PathValue("id"), input, key)
		})},
		{http.MethodPost, adminPrefix + "/periods/{id}/nodes", mutation(49152, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var in NodeInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.SaveNode(r.Context(), p.UserID, r.PathValue("id"), "", in, key)
		})},
		{http.MethodPut, adminPrefix + "/periods/{id}/nodes/{node}", mutation(49152, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var in NodeInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.SaveNode(r.Context(), p.UserID, r.PathValue("id"), r.PathValue("node"), in, key)
		})},
		{http.MethodPost, adminPrefix + "/periods/{id}/publish", stateHandler(s, "publish", mutation)},
		{http.MethodPost, adminPrefix + "/periods/{id}/close", stateHandler(s, "close", mutation)},
		{http.MethodPost, adminPrefix + "/periods/{id}/reopen", stateHandler(s, "reopen", mutation)},
		{http.MethodPost, adminPrefix + "/playtests", mutation(4096, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var in PlaytestInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.PreparePlaytest(r.Context(), p.UserID, in, key)
		})},
		{http.MethodPost, adminPrefix + "/playtests/{id}/abandon", mutation(4096, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var in PlaytestAbandonInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.AbandonPlaytest(r.Context(), p.UserID, r.PathValue("id"), in, key)
		})},
		{http.MethodPost, adminPrefix + "/playtests/{id}/start", mutation(4096, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var in StartInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.StartPlaytest(r.Context(), p.UserID, r.PathValue("id"), in, key)
		})},
		{http.MethodPost, adminPrefix + "/playtests/{id}/submit", mutation((4<<20)+1024, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			in, err := decodeSubmit(raw)
			defer clear(in.Inputs)
			if err != nil {
				return nil, err
			}
			return s.SubmitPlaytest(r.Context(), p.UserID, r.PathValue("id"), in, key)
		})},
		{http.MethodPost, adminPrefix + "/challenges/{id}/cancel", mutation(4096, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
			var in CancelInput
			if err := decodeJSON(raw, &in); err != nil {
				return nil, err
			}
			return s.AdminCancelChallenge(r.Context(), p.UserID, r.PathValue("id"), in, key)
		})},
	} {
		if err := routes.RegisterAdminRoute(route.method, route.path, route.handler); err != nil {
			return err
		}
	}
	return nil
}

func stateHandler(s *Service, action string, mutation func(int, func(*http.Request, limitedactivities.AdminPrincipal, string, []byte) (any, error)) limitedactivities.AuthorizedAdminHandler) limitedactivities.AuthorizedAdminHandler {
	return mutation(4096, func(r *http.Request, p limitedactivities.AdminPrincipal, key string, raw []byte) (any, error) {
		var in struct {
			ExpectedRevision string `json:"expected_revision"`
		}
		if err := decodeJSON(raw, &in); err != nil {
			return nil, err
		}
		return s.ChangePeriodState(r.Context(), p.UserID, r.PathValue("id"), action, in.ExpectedRevision, key)
	})
}

func readBody(r *http.Request, max int) ([]byte, error) {
	raw, err := httpapi.ReadBody(nil, r, httpapi.BodyOptions{MaxBytes: int64(max)})
	if err != nil || len(raw) == 0 {
		return nil, ErrInvalid
	}
	return raw, nil
}

func decodeJSON(raw []byte, out any) error {
	if strictjson.ValidateObjectWithFieldLimit(raw, 16384) != nil || httpapi.DecodeJSONWithNumbers(raw, out) != nil {
		return ErrInvalid
	}
	return nil
}

func decodeSubmit(raw []byte) (SubmitInput, error) {
	var in SubmitInput
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return in, ErrInvalid
	}
	seen := map[string]bool{}
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return in, ErrInvalid
		}
		key, ok := keyToken.(string)
		if !ok || seen[key] {
			return in, ErrInvalid
		}
		seen[key] = true
		var value json.RawMessage
		if err = dec.Decode(&value); err != nil {
			return in, ErrInvalid
		}
		switch key {
		case "tab_capability":
			if json.Unmarshal(value, &in.TabCapability) != nil {
				return in, ErrInvalid
			}
		case "inputs":
			in.Inputs = append([]byte(nil), value...)
			clear(value)
		case "terminal_tick":
			in.TerminalTick, err = canonicalInt(value)
			if err != nil {
				return in, ErrInvalid
			}
		default:
			return in, ErrInvalid
		}
	}
	if _, err = dec.Token(); err != nil {
		return in, ErrInvalid
	}
	if _, err = dec.Token(); !errors.Is(err, io.EOF) || len(seen) != 3 || len(in.Inputs) == 0 || len(in.Inputs) > 4<<20 {
		return in, ErrInvalid
	}
	return in, nil
}

func idempotencyKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", ErrInvalid
	}
	if _, err := idempotency.KeyHash(values[0]); err != nil {
		return "", ErrInvalid
	}
	return values[0], nil
}

func submitHTTPStatus(value any) int {
	if challenge, ok := value.(ChallengeView); ok && challenge.State == "verifying" {
		return http.StatusAccepted
	}
	return http.StatusOK
}

func singleQueryValues(r *http.Request, allowed ...string) (url.Values, error) {
	if r == nil || r.URL == nil {
		return nil, ErrInvalid
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, ErrInvalid
	}
	for key, entries := range values {
		found := false
		for _, accepted := range allowed {
			if key == accepted {
				found = true
				break
			}
		}
		if !found || len(entries) != 1 {
			return nil, ErrInvalid
		}
	}
	return values, nil
}

func emptyBodyOnly(r *http.Request) bool {
	if r == nil || r.Body == nil {
		return true
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 1))
	return err == nil && len(b) == 0
}

func emptyBody(r *http.Request) bool {
	return r != nil && r.URL != nil && r.URL.RawQuery == "" && emptyBodyOnly(r)
}

func queryInt(r *http.Request, key string, defaultValue int) (int, error) {
	v := r.URL.Query()[key]
	if len(v) == 0 {
		return defaultValue, nil
	}
	if len(v) != 1 {
		return 0, ErrInvalid
	}
	n, err := strconv.Atoi(v[0])
	if err != nil || strconv.Itoa(n) != v[0] {
		return 0, ErrInvalid
	}
	return n, nil
}

func collectionPage(r *http.Request) (int, error) {
	if !emptyBodyOnly(r) {
		return 0, ErrInvalid
	}
	if _, err := singleQueryValues(r, "page"); err != nil {
		return 0, err
	}
	page, err := queryInt(r, "page", 1)
	if err != nil || !validCollectionPage(page) {
		return 0, ErrInvalid
	}
	return page, nil
}

func writeHTTPResult(w http.ResponseWriter, status int, value any, err error) {
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	encoded, encodeErr := json.Marshal(value)
	if encodeErr != nil || len(encoded) > 4<<20 {
		writeHTTPError(w, ErrInvariant)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(encoded, '\n'))
}

func writeHTTPError(w http.ResponseWriter, err error) {
	code, message := httperr.CodeServiceUnavailable, "The game is temporarily unavailable."
	switch {
	case errors.Is(err, ErrInvalid):
		code, message = httperr.CodeInvalidRequest, "Check the game request."
	case errors.Is(err, ErrUnauthorized), errors.Is(err, authz.ErrUnauthorized), errors.Is(err, resources.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "Sign in to play."
	case errors.Is(err, ErrForbidden), errors.Is(err, authz.ErrForbidden), errors.Is(err, resources.ErrForbidden):
		code, message = httperr.CodeForbidden, "This account cannot perform this action."
	case errors.Is(err, authz.ErrElevatedRequired):
		code, message = httperr.CodeElevationRequired, "Additional authorization is required."
	case errors.Is(err, ErrNotFound), errors.Is(err, authz.ErrNotFound), errors.Is(err, resources.ErrNotFound):
		code, message = httperr.CodeNotFound, "Game item not found."
	case errors.Is(err, ErrConflict):
		code, message = httperr.CodeConflict, "The game state or revision changed."
	case errors.Is(err, ErrClosed), errors.Is(err, limitedactivities.ErrClosed):
		code, message = httperr.CodeFeatureDisabled, "This game is not accepting that action."
	case errors.Is(err, maintenance.ErrMaintenanceOn), errors.Is(err, resources.ErrMaintenance):
		code, message = httperr.CodeMaintenance, "The site is under maintenance."
	case errors.Is(err, ErrCapacity):
		code, message = httperr.CodeRateLimited, "Verification capacity is full; retry shortly."
		w.Header().Set("Retry-After", "1")
	case errors.Is(err, ledger.ErrInsufficientBalance):
		code, message = httperr.CodeInsufficientCredits, "There are not enough general credits."
	}
	w.Header().Set("Cache-Control", "no-store")
	httperr.WriteError(w, httperr.New(code, message))
}
