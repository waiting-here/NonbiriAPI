package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestOAuthRouteAllowlistExactQueryAndLiveThrottle(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	handler := f.runtime.UserHandler()
	forceQuery := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start?", "", nil, nil)
	if forceQuery.Code != http.StatusBadRequest {
		t.Fatalf("force query=%d body=%s", forceQuery.Code, forceQuery.Body.String())
	}
	for _, target := range []string{"?route_id=unknown", "?route_id=account&resource_id=x", "?resource_id=x", "?route_id=endpoint-detail", "?route_id=endpoint-detail&resource_id=a/b", "?route_id=home&route_id=account"} {
		rec := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start"+target, "", nil, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("target %q status=%d body=%s", target, rec.Code, rec.Body.String())
		}
	}
	overlong := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start?route_id="+strings.Repeat("a", maxReturnQueryBytes), "", nil, nil)
	if overlong.Code != http.StatusBadRequest {
		t.Fatalf("overlong query=%d body=%s", overlong.Code, overlong.Body.String())
	}
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key='oauth_start_rate_limit'`); err != nil {
		t.Fatal(err)
	}
	first := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start?route_id=account", "", nil, nil)
	if first.Code != http.StatusFound {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	second := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, nil)
	if second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") == "" {
		t.Fatalf("second=%d retry=%q body=%s", second.Code, second.Header().Get("Retry-After"), second.Body.String())
	}
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='0' WHERE key='oauth_start_rate_limit'`); err != nil {
		t.Fatal(err)
	}
	disabled := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, nil)
	if disabled.Code != http.StatusFound {
		t.Fatalf("disabled=%d %s", disabled.Code, disabled.Body.String())
	}
	stateCookie := responseCookie(t, disabled, OAuthStateCookieName)
	location, _ := url.Parse(disabled.Header().Get("Location"))
	state := location.Query().Get("state")
	addLogin(f.provider, "exact", "discord-exact")
	extra := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=exact&state="+url.QueryEscape(state)+"&extra=1", "", []*http.Cookie{stateCookie}, nil)
	if extra.Code != http.StatusBadRequest {
		t.Fatalf("callback extra=%d %s", extra.Code, extra.Body.String())
	}
	cleared := responseCookie(t, extra, OAuthStateCookieName)
	if cleared.Value != "" || cleared.MaxAge != -1 {
		t.Fatalf("malformed callback did not clear state cookie: %+v", cleared)
	}
}

func TestAuthenticatedEmptyQueryRouteRejectsTrailingQuestionMark(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "force-query", "")
	rec := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session?", "", []*http.Cookie{cookie}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestOAuthGETRoutesRejectRequestBodies(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	handler := f.runtime.UserHandler()

	startWithBody := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "unexpected", nil, nil)
	if startWithBody.Code != http.StatusBadRequest {
		t.Fatalf("start status=%d body=%s", startWithBody.Code, startWithBody.Body.String())
	}

	start := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, nil)
	if start.Code != http.StatusFound {
		t.Fatalf("start=%d body=%s", start.Code, start.Body.String())
	}
	stateCookie := responseCookie(t, start, OAuthStateCookieName)
	authorization, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	callbackWithBody := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=unused&state="+url.QueryEscape(authorization.Query().Get("state")), "unexpected", []*http.Cookie{stateCookie}, nil)
	if callbackWithBody.Code != http.StatusBadRequest {
		t.Fatalf("callback status=%d body=%s", callbackWithBody.Code, callbackWithBody.Body.String())
	}
	cleared := responseCookie(t, callbackWithBody, OAuthStateCookieName)
	if cleared.Value != "" || cleared.MaxAge != -1 {
		t.Fatalf("callback did not clear state cookie: %+v", cleared)
	}
}

func TestProviderAuthorizationURLFailureIsServiceUnavailable(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	f.provider.mu.Lock()
	f.provider.authorizationURL = "https://identity.example/authorize?state=wrong"
	f.provider.mu.Unlock()

	start := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, nil)
	if start.Code != http.StatusServiceUnavailable || !strings.Contains(start.Body.String(), httperr.CodeServiceUnavailable) {
		t.Fatalf("login start=%d body=%s", start.Code, start.Body.String())
	}

	f.provider.mu.Lock()
	f.provider.authorizationURL = ""
	f.provider.mu.Unlock()
	cookie := loginUser(t, f, "authorization-location", "")
	f.provider.mu.Lock()
	f.provider.authorizationURL = "https://identity.example/authorize?state=wrong"
	f.provider.mu.Unlock()
	elevate := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/auth/elevate", "", []*http.Cookie{cookie}, nil)
	if elevate.Code != http.StatusServiceUnavailable || !strings.Contains(elevate.Body.String(), httperr.CodeServiceUnavailable) {
		t.Fatalf("elevation start=%d body=%s", elevate.Code, elevate.Body.String())
	}
}

func TestAnonymousRouteDoesNotInheritLogoutMaintenanceExemption(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	called := false
	if err := f.runtime.RegisterAnonymousUserRoute(http.MethodGet, "/api/auth/logout", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}); err != nil {
		t.Fatal(err)
	}
	f.gate.enabled = true
	rec := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/logout", "", nil, nil)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), httperr.CodeMaintenance) || called {
		t.Fatalf("status=%d called=%v body=%s", rec.Code, called, rec.Body.String())
	}
}

func TestMaintenanceBlocksLoginButNotAuthenticatedUserLogout(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "maintenance", "")
	f.gate.enabled = true
	start := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, nil)
	if start.Code != http.StatusServiceUnavailable || !strings.Contains(start.Body.String(), httperr.CodeMaintenance) {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	logout := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/auth/logout", "", []*http.Cookie{cookie}, nil)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout=%d %s", logout.Code, logout.Body.String())
	}
}

func TestOAuthLoginAndElevationShareLiveClientIPThrottle(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "shared-throttle", "")
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key='oauth_start_rate_limit'`); err != nil {
		t.Fatal(err)
	}
	elevate := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/auth/elevate", "", []*http.Cookie{cookie}, nil)
	if elevate.Code != http.StatusTooManyRequests || elevate.Header().Get("Retry-After") == "" {
		t.Fatalf("status=%d retry=%q body=%s", elevate.Code, elevate.Header().Get("Retry-After"), elevate.Body.String())
	}
}

func TestConcurrentDiscordIdentityConvergesToOneRegistration(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	handler := f.runtime.UserHandler()
	addLogin(f.provider, "race-a", "discord-race")
	addLogin(f.provider, "race-b", "discord-race")
	type startData struct {
		cookie *http.Cookie
		state  string
	}
	starts := make([]startData, 2)
	for index := range starts {
		rec := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, nil)
		parsed, _ := url.Parse(rec.Header().Get("Location"))
		starts[index] = startData{cookie: responseCookie(t, rec, OAuthStateCookieName), state: parsed.Query().Get("state")}
	}
	results := make([]int, 2)
	var wg sync.WaitGroup
	for index, code := range []string{"race-a", "race-b"} {
		wg.Add(1)
		go func(index int, code string) {
			defer wg.Done()
			rec := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code="+code+"&state="+url.QueryEscape(starts[index].state), "", []*http.Cookie{starts[index].cookie}, nil)
			results[index] = rec.Code
		}(index, code)
	}
	wg.Wait()
	for index, status := range results {
		if status != http.StatusFound {
			t.Fatalf("callback %d status=%d", index, status)
		}
	}
	for label, query := range map[string]string{"users": `SELECT COUNT(*) FROM users WHERE discord_id='discord-race'`, "wallets": `SELECT COUNT(*) FROM credit_accounts WHERE kind='user'`, "caller_keys": `SELECT COUNT(*) FROM caller_keys`, "sessions": `SELECT COUNT(*) FROM sessions`} {
		var count int
		if err := f.store.DB().QueryRow(query).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", label, count, err)
		}
	}
}

func TestFinalTxAdapterRechecksLiveActor(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "adapter", "")
	var userID int64
	var generation string
	if err := f.store.DB().QueryRow(`SELECT user_id,cred_gen FROM sessions WHERE token_hash=?`, sessionLookupHash(cookie.Value)).Scan(&userID, &generation); err != nil {
		t.Fatal(err)
	}
	actor := authz.Actor{Kind: authz.ActorUserSession, UserID: userID, SessionTokenHash: sessionLookupHash(cookie.Value), SessionGeneration: generation}
	ctx := withActor(context.Background(), actor)
	authorize := func(ctx context.Context, user int64) error {
		tx, err := f.store.DB().BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		return f.runtime.AuthorizeUserMutation(ctx, tx, user)
	}
	if err := authorize(ctx, userID); err != nil {
		t.Fatalf("valid actor=%v", err)
	}
	if err := authorize(ctx, userID+1); !errors.Is(err, resources.ErrUnauthorized) {
		t.Fatalf("foreign id=%v", err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1 WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if err := authorize(ctx, userID); !errors.Is(err, resources.ErrForbidden) {
		t.Fatalf("live ban=%v", err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=0 WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	_ = loginUser(t, f, "adapter-replace", "")
	if err := authorize(ctx, userID); !errors.Is(err, resources.ErrUnauthorized) {
		t.Fatalf("replaced session=%v", err)
	}
}

func TestEdgeHostStationAndCookieSameOriginBoundary(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "edge", "")
	userHandler, adminHandler := f.runtime.UserHandler(), f.runtime.AdminHandler()
	root := http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		switch httpmw.StationOf(req) {
		case host.StationUser:
			userHandler.ServeHTTP(writer, req)
		case host.StationAdmin:
			adminHandler.ServeHTTP(writer, req)
		default:
			http.NotFound(writer, req)
		}
	})
	edge, err := httpmw.New(httpmw.Config{UserHost: "user.example", AdminHost: "admin.example", SiteBaseURL: "https://user.example"}, root)
	if err != nil {
		t.Fatal(err)
	}
	call := func(target string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		req.RemoteAddr = "192.0.2.20:1234"
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		edge.ServeHTTP(rec, req)
		return rec
	}
	if rec := call("https://user.example/api/auth/logout", cookie, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("missing same-origin proof=%d %s", rec.Code, rec.Body.String())
	}
	if rec := call("https://admin.example/api/auth/logout", cookie, "https://admin.example"); rec.Code != http.StatusNotFound {
		t.Fatalf("user API on admin station=%d %s", rec.Code, rec.Body.String())
	}
	if rec := call("https://user.example/api/auth/logout", cookie, "https://user.example"); rec.Code != http.StatusNoContent {
		t.Fatalf("same-origin logout=%d %s", rec.Code, rec.Body.String())
	}
}

func TestStableUnknownAndMethodResponses(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	unknown := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/unknown", "", nil, nil)
	if unknown.Code != http.StatusNotFound || unknown.Header().Get("Cache-Control") != "no-store" || !strings.Contains(unknown.Body.String(), httperr.CodeNotFound) {
		t.Fatalf("unknown=%d headers=%v body=%s", unknown.Code, unknown.Header(), unknown.Body.String())
	}
	method := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/session", "", nil, nil)
	if method.Code != http.StatusMethodNotAllowed || !strings.Contains(method.Body.String(), httperr.CodeMethodNotAllowed) {
		t.Fatalf("method=%d body=%s", method.Code, method.Body.String())
	}
}
