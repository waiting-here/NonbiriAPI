package lakenotes

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const baseRoute = "/api/games/lake-notes"
const adminBaseRoute = "/admin/api/games/lake-notes"

func decode(r *http.Request, out any, required ...string) error {
	if r.URL.RawQuery != "" || r.Body == nil {
		return ErrInvalid
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, 16385))
	if e != nil || len(raw) > 16384 {
		return ErrInvalid
	}
	if e = strictjson.ValidateObject(raw); e != nil {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(out); e != nil {
		return ErrInvalid
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(raw, &fields); e != nil {
		return ErrInvalid
	}
	for _, key := range required {
		if v, ok := fields[key]; !ok || string(v) == "null" {
			return ErrInvalid
		}
	}
	for key, value := range fields {
		if string(value) == "null" && key != "id" {
			return ErrInvalid
		}
	}
	if edges, ok := fields["edges"]; ok {
		var values []map[string]json.RawMessage
		if json.Unmarshal(edges, &values) != nil {
			return ErrInvalid
		}
		for _, v := range values {
			if v["tick"] == nil || v["held"] == nil || string(v["held"]) == "null" {
				return ErrInvalid
			}
		}
	}
	if actionRaw, ok := fields["action"]; ok {
		var name string
		if json.Unmarshal(actionRaw, &name) != nil {
			return ErrInvalid
		}
		allowed := map[string][]string{"buy_gear": {"id"}, "equip_gear": {"id", "slot"}, "save_gear_loadout": {"index"}, "load_gear_loadout": {"index"}, "buy_bait": {"id"}, "select_bait": {"id"}, "sell_fish": {"fish_ids"}, "sell_all_fish": {}, "set_fish_lock": {"fish_ids", "locked"}, "sell_debris": {"id"}, "sell_all_debris": {}, "switch_location": {"id"}, "rest": {}, "choose_skill": {"id"}, "respec": {}, "accept_contract": {"id"}, "cancel_contract": {"id"}, "claim_contract": {"id"}}
		keys, ok := allowed[name]
		if !ok {
			return ErrInvalid
		}
		set := map[string]bool{"action": true, "expected_profile_revision": true}
		if name == "buy_bait" {
			set["quantity"] = true
		}
		for _, key := range keys {
			set[key] = true
			if _, ok := fields[key]; !ok {
				return ErrInvalid
			}
		}
		for key := range fields {
			if !set[key] {
				return ErrInvalid
			}
		}
	}
	return nil
}
func empty(r *http.Request) bool {
	if r.URL.RawQuery != "" {
		return false
	}
	if r.Body == nil {
		return true
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, 1))
	return e == nil && len(raw) == 0
}
func key(r *http.Request) (string, error) {
	v := r.Header.Values("Idempotency-Key")
	if len(v) != 1 {
		return "", ErrInvalid
	}
	return v[0], nil
}
func respond(w http.ResponseWriter, v any, e error) {
	w.Header().Set("Cache-Control", "no-store")
	if e == nil {
		httperr.WriteJSON(w, http.StatusOK, v)
		return
	}
	code, message := httperr.CodeServiceUnavailable, "The saved game could not be loaded. Please try again."
	switch {
	case errors.Is(e, ErrInvalid), errors.Is(e, rules.ErrInvalid), errors.Is(e, rules.ErrOverflow):
		code, message = httperr.CodeInvalidRequest, "Check the selected action or amount."
	case errors.Is(e, authz.ErrUnauthorized), errors.Is(e, resources.ErrUnauthorized):
		code, message = httperr.CodeUnauthorized, "Sign in to continue."
	case errors.Is(e, authz.ErrForbidden), errors.Is(e, resources.ErrForbidden):
		code, message = httperr.CodeForbidden, "This account cannot play this game."
	case errors.Is(e, ErrConflict), errors.Is(e, rules.ErrBusy):
		code, message = httperr.CodeConflict, "The saved state changed. Reload it to continue."
	case errors.Is(e, ErrNotFound):
		code, message = httperr.CodeNotFound, "The saved game or period was not found."
	case errors.Is(e, ErrClosed):
		code, message = httperr.CodeFeatureDisabled, "This game is closed. Your progress is saved."
	case errors.Is(e, ErrCapacity):
		code, message = httperr.CodeResourceLimitExceeded, "Please wait briefly and try again."
	case errors.Is(e, ledger.ErrInsufficientBalance), errors.Is(e, rules.ErrFunds):
		code, message = httperr.CodeInsufficientCredits, "There is not enough available balance."
	case errors.Is(e, maintenance.ErrMaintenanceOn), errors.Is(e, resources.ErrMaintenance):
		code, message = httperr.CodeMaintenance, "The site is under maintenance."
	}
	httperr.WriteError(w, httperr.New(code, message))
}

// RegisterRoutes uses the existing session, CSRF and account-lifecycle bridges.
func RegisterRoutes(users resources.UserRouteRegistrar, continuation resources.ContinuationUserRouteRegistrar, admins host.AdminRegistrar, s *Service) error {
	if users == nil || admins == nil || s == nil {
		return ErrInvalid
	}
	get := func(fn func(*http.Request, int64) (any, error)) resources.AuthorizedUserHandler {
		return func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
			if !empty(r) {
				respond(w, nil, ErrInvalid)
				return
			}
			v, e := fn(r, p.UserID)
			respond(w, v, e)
		}
	}
	for _, route := range []struct {
		path string
		fn   func(*http.Request, int64) (any, error)
	}{
		{baseRoute + "/profile", func(r *http.Request, id int64) (any, error) { return s.Profile(r.Context(), id) }},
		{baseRoute + "/casts/{id}", func(r *http.Request, id int64) (any, error) { return s.Cast(r.Context(), id, r.PathValue("id")) }},
		{baseRoute + "/rules", func(r *http.Request, id int64) (any, error) {
			tx, e := s.database.BeginTx(r.Context(), nil)
			if e != nil {
				return nil, e
			}
			defer tx.Rollback()
			if e = s.authorize(r.Context(), tx, id, false); e != nil {
				return nil, e
			}
			if e = tx.Commit(); e != nil {
				return nil, e
			}
			return struct {
				RulesID string          `json:"rules_id"`
				Catalog json.RawMessage `json:"catalog"`
			}{rules.RulesID, rules.CatalogJSON()}, nil
		}},
	} {
		if e := continuation.RegisterContinuationUserRoute("GET", route.path, func(w http.ResponseWriter, r *http.Request, p resources.ContinuationUserPrincipal) {
			get(route.fn)(w, r, resources.UserPrincipal{UserID: p.UserID})
		}); e != nil {
			return e
		}
	}
	type postFn func(*http.Request, int64, string) (any, error)
	posts := []struct {
		path string
		fn   postFn
	}{
		{"/exchange", func(r *http.Request, user int64, k string) (any, error) {
			var in ExchangeInput
			if e := decode(r, &in, "direction", "quantity", "expected_settings_revision", "expected_profile_revision"); e != nil {
				return nil, e
			}
			out, e := s.Exchange(r.Context(), user, k, in)
			return out.Value, e
		}},
		{"/actions", func(r *http.Request, user int64, k string) (any, error) {
			var in ActionInput
			if e := decode(r, &in, "action", "expected_profile_revision"); e != nil {
				return nil, e
			}
			out, e := s.Action(r.Context(), user, k, in)
			return out.Value, e
		}},
		{"/casts", func(r *http.Request, user int64, k string) (any, error) {
			var in StartInput
			if e := decode(r, &in, "expected_profile_revision"); e != nil {
				return nil, e
			}
			out, e := s.Start(r.Context(), user, k, in)
			return out.Value, e
		}},
		{"/casts/{id}/checkpoint", func(r *http.Request, user int64, k string) (any, error) {
			var in CheckpointInput
			if e := decode(r, &in, "generation", "expected_revision", "from_tick", "to_tick", "initial_held", "edges"); e != nil {
				return nil, e
			}
			out, e := s.Checkpoint(r.Context(), user, r.PathValue("id"), k, in)
			return out.Value, e
		}},
		{"/casts/{id}/pause", func(r *http.Request, user int64, k string) (any, error) {
			var in ControlInput
			if e := decode(r, &in, "generation", "expected_revision"); e != nil {
				return nil, e
			}
			out, e := s.Pause(r.Context(), user, r.PathValue("id"), k, in)
			return out.Value, e
		}},
		{"/casts/{id}/resume", func(r *http.Request, user int64, k string) (any, error) {
			var in ControlInput
			if e := decode(r, &in, "generation", "expected_revision"); e != nil {
				return nil, e
			}
			out, e := s.Resume(r.Context(), user, r.PathValue("id"), k, in)
			return out.Value, e
		}},
	}
	for _, route := range posts {
		handler := func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
			k, err := key(r)
			if err != nil {
				respond(w, nil, err)
				return
			}
			out, err := route.fn(r, p.UserID, k)
			respond(w, out, err)
		}
		var err error
		if route.path == "/casts/{id}/pause" {
			err = continuation.RegisterContinuationUserRoute("POST", baseRoute+route.path, func(w http.ResponseWriter, r *http.Request, p resources.ContinuationUserPrincipal) {
				handler(w, r, resources.UserPrincipal{UserID: p.UserID})
			})
		} else {
			err = users.RegisterUserRoute("POST", baseRoute+route.path, handler)
		}
		if err != nil {
			return err
		}
	}
	if e := users.RegisterUserRoute("POST", baseRoute+"/exchange/quote", func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
		var in QuoteInput
		if e := decode(r, &in, "direction", "quantity"); e != nil {
			respond(w, nil, e)
			return
		}
		out, e := s.Quote(r.Context(), p.UserID, in)
		respond(w, out, e)
	}); e != nil {
		return e
	}
	if e := admins.RegisterAdminRoute("GET", adminBaseRoute+"/periods", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		page, size := 1, 20
		for name, values := range q {
			if (name != "page" && name != "page_size") || len(values) != 1 {
				respond(w, nil, ErrInvalid)
				return
			}
		}
		var e error
		if v := q.Get("page"); v != "" {
			page, e = strconv.Atoi(v)
			if e != nil {
				respond(w, nil, ErrInvalid)
				return
			}
		}
		if v := q.Get("page_size"); v != "" {
			size, e = strconv.Atoi(v)
			if e != nil {
				respond(w, nil, ErrInvalid)
				return
			}
		}
		if r.Body != nil {
			raw, e := io.ReadAll(io.LimitReader(r.Body, 1))
			if e != nil || len(raw) > 0 {
				respond(w, nil, ErrInvalid)
				return
			}
		}
		out, e := s.Periods(r.Context(), page, size)
		respond(w, out, e)
	})); e != nil {
		return e
	}
	return nil
}
