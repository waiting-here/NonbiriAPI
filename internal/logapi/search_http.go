package logapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func adminSearchScope(ctx context.Context) logSearchScope {
	actor, _ := auth.ActorFromContext(ctx)
	return logSearchScope{role: "admin", actorID: actor.UserID}
}

func searchOwner(ctx context.Context, scope logSearchScope) logSearchOwner {
	actor, _ := auth.ActorFromContext(ctx)
	return logSearchOwner{role: scope.role, actorID: scope.actorID, session: actor.SessionTokenHash}
}

func acceptsAsyncSearch(request *http.Request) bool {
	for _, header := range request.Header.Values("Prefer") {
		for preference := range strings.SplitSeq(header, ",") {
			if strings.EqualFold(strings.TrimSpace(preference), "respond-async") {
				return true
			}
		}
	}
	return false
}

func (api *HTTPAPI) listLogs(w http.ResponseWriter, request *http.Request, scope logSearchScope) {
	ctx := request.Context()
	async := acceptsAsyncSearch(request)
	if async {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, logPageTimeout)
		defer cancel()
	}
	var err error
	if scope.role == "steward" {
		err = api.repository.authorizeStewardRead(ctx, scope.actorID, scope.steward)
		if err != nil && !(async && errors.Is(err, context.DeadlineExceeded)) {
			writeLogError(w, err)
			return
		}
	}
	if !requireNoBody(w, request) {
		return
	}
	filter, parseErr := parseListFilter(request.URL.RawQuery, scope.role, false)
	if parseErr != nil {
		writeLogError(w, parseErr)
		return
	}
	var page any
	if err == nil {
		switch scope.role {
		case "user":
			page, err = api.repository.ListUser(ctx, scope.actorID, filter)
		case "steward":
			page, err = api.repository.ListSteward(ctx, scope.actorID, filter, scope.steward)
		case "admin":
			page, err = api.repository.ListAdmin(ctx, filter)
		}
	}
	if err == nil {
		writeSearchPage(w, scope, page)
		return
	}
	if !async || filter.Page == nil || !errors.Is(err, context.DeadlineExceeded) || request.Context().Err() != nil {
		writeLogError(w, err)
		return
	}
	owner := searchOwner(request.Context(), scope)
	id, err := api.repository.searches.start(request.Context(), owner, func(worker context.Context, add func(int64, int64) error) error {
		return api.repository.collectSearchIDs(worker, scope, filter, add)
	})
	if err != nil {
		writeSearchError(w, err)
		return
	}
	w.Header().Set("Preference-Applied", "respond-async")
	writePendingSearch(w, id)
}

func (api *HTTPAPI) registerUserSearchRoutes(registrar UserRouteRegistrar, path, role string) error {
	handler := func(w http.ResponseWriter, r *http.Request, principal UserPrincipal) {
		api.searchResource(w, r, logSearchScope{role: role, actorID: principal.UserID, steward: api.steward})
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		if err := registrar.RegisterUserRoute(method, path, handler); err != nil {
			return err
		}
	}
	return nil
}

func (api *HTTPAPI) adminSearch(w http.ResponseWriter, r *http.Request) {
	api.searchResource(w, r, adminSearchScope(r.Context()))
}

func (api *HTTPAPI) searchResource(w http.ResponseWriter, r *http.Request, scope logSearchScope) {
	if !requireNoBody(w, r) {
		return
	}
	ctx := r.Context()
	owner := searchOwner(ctx, scope)
	id := r.PathValue("search_id")
	registry := &api.repository.searches
	if r.Method == http.MethodDelete {
		if r.URL.RawQuery != "" {
			writeLogError(w, ErrInvalid)
			return
		}
		// Cancellation exposes no result and can release the reader even if a
		// fresh grant check would have to queue behind that same search.
		registry.forget(id, owner)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	window, err := parseSearchWindow(r.URL.RawQuery)
	if err != nil {
		writeLogError(w, err)
		return
	}
	lease, err := registry.lookup(id, owner)
	if err != nil {
		if grantErr := api.repository.authorizeSearch(ctx, scope); grantErr != nil {
			writeLogError(w, grantErr)
			return
		}
		writeSearchError(w, err)
		return
	}
	defer lease.release()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lease.ctx, cancel)
	defer stop()
	defer cancel()
	failed := func(err error) {
		if lease.ctx.Err() != nil && r.Context().Err() == nil {
			writeLogError(w, ErrNotFound)
			return
		}
		if errors.Is(err, ErrSearchExpired) || errors.Is(err, ErrForbidden) ||
			errors.Is(err, authz.ErrMFARequired) || errors.Is(err, authz.ErrPermissionDenied) || errors.Is(err, authz.ErrUnauthorized) {
			registry.forget(id, owner)
		}
		writeSearchError(w, err)
	}
	if lease.matches == nil {
		if err := api.repository.authorizeSearch(ctx, scope); err != nil {
			failed(err)
			return
		}
		if err := lease.ctx.Err(); err != nil {
			failed(err)
			return
		}
		writePendingSearch(w, id)
		return
	}
	page, err := api.repository.readSearchPage(ctx, scope, func(now int64) ([]int64, pagination.Metadata, error) {
		return lease.matches.window(ctx, window, now)
	})
	if err != nil {
		failed(err)
		return
	}
	if err := lease.ctx.Err(); err != nil {
		failed(err)
		return
	}
	writeSearchPage(w, scope, page)
}

func parseSearchWindow(raw string) (pagination.Request, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return pagination.Request{}, ErrInvalid
	}
	for key := range values {
		if key != "page" && key != "page_size" {
			return pagination.Request{}, ErrInvalid
		}
	}
	window, _, err := pagination.Parse(values)
	if err != nil {
		return pagination.Request{}, ErrInvalid
	}
	return window, nil
}

func writeSearchPage(w http.ResponseWriter, scope logSearchScope, page any) {
	if scope.role == "user" {
		writeLogJSON(w, http.StatusOK, page)
	} else {
		writeManagementLogJSON(w, http.StatusOK, page)
	}
}

func writePendingSearch(w http.ResponseWriter, id string) {
	w.Header().Set("Retry-After", "1")
	httperr.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "running", "search_id": id})
}

func writeSearchError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errSearchBusy):
		w.Header().Set("Retry-After", "2")
		httperr.WriteError(w, httperr.New(httperr.CodeRateLimited, "log search queue is full; retry shortly"))
	case errors.Is(err, ErrSearchExpired):
		httperr.WriteError(w, httperr.New(httperr.CodeRefreshRequired, "log search results changed; refresh the search"))
	default:
		writeLogError(w, err)
	}
}
