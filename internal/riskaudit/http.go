package riskaudit

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type AdminRouteRegistrar interface {
	RegisterAdminRoute(string, string, http.Handler) error
}
type route struct{ method, path, action string }

var routes = []route{
	{http.MethodPost, "/client-scans", "scan_create"}, {http.MethodGet, "/client-scans", "scan_recent"},
	{http.MethodGet, "/client-scans/{id}", "scan_get"},
	{http.MethodGet, "/client-scans/{id}/results", "scan_results"},
	{http.MethodPost, "/client-scans/{id}/cancel", "scan_cancel"},
	{http.MethodGet, "/users", "users"}, {http.MethodGet, "/users/{id}", "user"},
	{http.MethodGet, "/shared-ips", "ips"}, {http.MethodGet, "/client-rules", "rules"},
	{http.MethodPost, "/client-rules", "create_rule"}, {http.MethodPatch, "/client-rules/{id}", "update_rule"},
	{http.MethodDelete, "/client-rules/{id}", "delete_rule"},
	{http.MethodGet, "/config", "config"}, {http.MethodPut, "/config", "update_config"},
}

func RegisterAdminRoutes(registrar AdminRouteRegistrar, repository *Repository) error {
	if registrar == nil || repository == nil {
		return ErrInvalid
	}
	for _, route := range routes {
		action := route.action
		if err := registrar.RegisterAdminRoute(route.method, "/admin/api/abuse-audit"+route.path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor, ok := auth.ActorFromContext(r.Context())
			if !ok || actor.Kind != authz.ActorAdminSession {
				auditError(w, ErrForbidden)
				return
			}
			serve(repository, action, Actor{Admin: true, UserID: actor.UserID}, w, r)
		})); err != nil {
			return err
		}
	}
	return nil
}
func RegisterStewardRoutes(registrar resources.UserRouteRegistrar, repository *Repository) error {
	if registrar == nil || repository == nil {
		return ErrInvalid
	}
	for _, route := range routes {
		action := route.action
		if err := registrar.RegisterUserRoute(route.method, "/api/steward/abuse-audit"+route.path, func(w http.ResponseWriter, r *http.Request, p resources.UserPrincipal) {
			actor, ok := auth.ActorFromContext(r.Context())
			if !ok || actor.Kind != authz.ActorUserSession || actor.UserID != p.UserID {
				auditError(w, ErrForbidden)
				return
			}
			serve(repository, action, Actor{UserID: p.UserID}, w, r)
		}); err != nil {
			return err
		}
	}
	return nil
}
func auditError(w http.ResponseWriter, err error) {
	code, message := httperr.CodeServiceUnavailable, "audit unavailable"
	switch {
	case errors.Is(err, ErrInvalid):
		code = httperr.CodeInvalidRequest
		message = "invalid audit request"
	case errors.Is(err, ErrForbidden):
		code = httperr.CodeForbidden
		message = "audit access forbidden"
	case errors.Is(err, ErrNotFound):
		code = httperr.CodeNotFound
		message = "audit record not found"
	case errors.Is(err, ErrConflict):
		code = httperr.CodeConflict
		message = "audit record changed; refresh and retry"
	}
	httperr.WriteError(w, httperr.New(code, message))
}
func auditJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > 8<<20 {
		auditError(w, ErrUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}
func intQuery(q url.Values, key string, fallback int64) (int64, error) {
	values, ok := q[key]
	if !ok {
		return fallback, nil
	}
	if len(values) != 1 || values[0] == "" {
		return 0, ErrInvalid
	}
	n, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != values[0] {
		return 0, ErrInvalid
	}
	return n, nil
}
func parseWindow(q url.Values, now int64, parseAfter bool) (Window, error) {
	var w Window
	var err error
	w.From, err = intQuery(q, "from", 0)
	if err != nil {
		return w, err
	}
	w.To, err = intQuery(q, "to", 0)
	if err != nil {
		return w, err
	}
	if q.Has("lookback_hours") {
		hours, e := intQuery(q, "lookback_hours", 0)
		if e != nil || hours < 1 || hours > 720 || q.Has("from") || q.Has("to") {
			return w, ErrInvalid
		}
		w.From, w.To = max(0, now-hours*3600), now
	}
	limit, err := intQuery(q, "limit", 50)
	if err != nil || limit > MaxPage {
		return w, ErrInvalid
	}
	w.Limit = int(limit)
	if parseAfter {
		w.After, err = intQuery(q, "after", 0)
		if err != nil {
			return w, err
		}
	}
	w.Kind = q.Get("kind")
	w.Model = q.Get("model")
	return w.validate(now)
}
func decodeBody(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	return nil
}

type ruleInput struct {
	Name         string      `json:"name"`
	Status       string      `json:"status"`
	Enabled      bool        `json:"enabled"`
	Revision     int64       `json:"revision"`
	Conditions   []Condition `json:"conditions"`
	EvidenceNote string      `json:"evidence_note"`
	EvidenceURL  string      `json:"evidence_url"`
}

func serve(repository *Repository, action string, actor Actor, w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(action, "scan_") {
		serveScans(repository, action, actor, w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if len(r.URL.RawQuery) > 4096 || (r.Method == http.MethodGet && r.ContentLength != 0) {
		auditError(w, ErrInvalid)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		auditError(w, ErrInvalid)
		return
	}
	allowed := map[string]bool{"from": true, "to": true, "lookback_hours": true, "limit": true, "after": true, "kind": true, "signal": true, "revision": true, "model": true}
	for key, values := range q {
		if !allowed[key] || len(values) != 1 {
			auditError(w, ErrInvalid)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var output any
	status := http.StatusOK
	switch action {
	case "config":
		output, err = repository.GetConfig(ctx, actor)
	case "update_config":
		if !actor.Admin {
			auditError(w, ErrForbidden)
			return
		}
		var value Config
		err = decodeBody(w, r, &value)
		if err == nil {
			output, err = repository.UpdateConfig(ctx, actor, value)
		}
	case "rules":
		var rules []Rule
		rules, err = repository.Rules(ctx, actor)
		if err != nil {
			break
		}
		limit, e := intQuery(q, "limit", 100)
		if e != nil || limit < 1 || limit > 100 {
			err = ErrInvalid
			break
		}
		after := q.Get("after")
		if len(after) > 64 {
			err = ErrInvalid
			break
		}
		items := make([]Rule, 0)
		for _, rule := range rules {
			if rule.ID > after {
				items = append(items, rule)
			}
		}
		more := len(items) > int(limit)
		if more {
			items = items[:limit]
		}
		next := ""
		if len(items) > 0 {
			next = items[len(items)-1].ID
		}
		output = struct {
			Items   []Rule `json:"items"`
			HasMore bool   `json:"has_more"`
			Next    string `json:"next,omitempty"`
			Total   int    `json:"total"`
		}{items, more, next, len(rules)}
	case "create_rule", "update_rule":
		var value ruleInput
		err = decodeBody(w, r, &value)
		if err != nil {
			break
		}
		rule := Rule{ID: r.PathValue("id"), Name: value.Name, Status: value.Status, Enabled: value.Enabled, Revision: value.Revision, Conditions: value.Conditions, EvidenceNote: value.EvidenceNote, EvidenceURL: value.EvidenceURL}
		output, err = repository.PutRule(ctx, actor, rule, action == "create_rule")
		if action == "create_rule" {
			status = http.StatusCreated
		}
	case "delete_rule":
		revision, e := intQuery(q, "revision", 0)
		if e != nil {
			err = e
			break
		}
		err = repository.DeleteRule(ctx, actor, r.PathValue("id"), revision)
		output = struct {
			Deleted bool `json:"deleted"`
		}{true}
	case "users":
		var window Window
		window, err = parseWindow(q, repository.now().Unix(), true)
		if err != nil {
			break
		}
		signal := q.Get("signal")
		if signal == "client" {
			output, err = repository.ClientMatches(ctx, actor, window)
		} else if signal == "" || signal == "rpm" || signal == "concurrency" {
			var page Page[UserSummary]
			page, err = repository.Users(ctx, actor, window)
			if err == nil && signal != "" {
				filtered := make([]UserSummary, 0)
				page.Scanned = len(page.Items)
				for _, item := range page.Items {
					if signal == "rpm" && item.RPMRisk || signal == "concurrency" && item.ConcurrencyRisk {
						filtered = append(filtered, item)
					}
				}
				page.Items = filtered
				if page.Coverage != "bounded_scan" {
					page.Coverage = "candidate_page"
				}
			}
			output = page
		} else {
			err = ErrInvalid
		}
	case "user":
		user, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil || user <= 0 || strconv.FormatInt(user, 10) != r.PathValue("id") {
			err = ErrInvalid
			break
		}
		var window Window
		window, err = parseWindow(q, repository.now().Unix(), true)
		if err == nil {
			output, err = repository.User(ctx, actor, user, window)
		}
	case "ips":
		var window Window
		window, err = parseWindow(q, repository.now().Unix(), false)
		if err == nil {
			// Leave the omitted lower bound to the configured shared-IP window.
			if !q.Has("from") && !q.Has("lookback_hours") {
				window.From = 0
			}
			output, err = repository.SharedIPs(ctx, actor, window, q.Get("after"))
		}
	default:
		err = ErrInvalid
	}
	if err != nil {
		auditError(w, err)
		return
	}
	auditJSON(w, status, output)
}
