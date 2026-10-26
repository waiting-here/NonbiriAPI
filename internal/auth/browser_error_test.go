package auth

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
)

var browserHeaders = map[string]string{"Accept": "text/html,application/xhtml+xml,*/*;q=0.8", "Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate"}

func browserAuthHandler(t *testing.T, f *runtimeFixture) http.Handler {
	t.Helper()
	h, err := httpmw.New(httpmw.Config{UserHost: "user.example", AdminHost: "admin.example", SiteBaseURL: "https://user.example"}, f.runtime.UserHandler())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestBrowserOAuthFailureReasons(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		explanation string
	}{
		{"invalid callback", 400, "This Discord sign-in could not be completed"},
		{"missing cookie", 401, "This sign-in has expired or could not be verified"},
		{"expired state", 401, "This sign-in has expired or could not be verified"},
		{"replayed state", 401, "This sign-in has expired or could not be verified"},
		{"provider rejected", 401, "Discord did not accept this authorization"},
		{"provider unavailable", 503, "Sign-in is temporarily unavailable"},
		{"invalid identity", 401, "Your Discord account details could not be verified"},
		{"guild gate", 401, "Discord access requirements were not met"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRuntimeFixture(t, nil)
			h := browserAuthHandler(t, f)
			addLogin(f.provider, "private-code", "private-discord-id")
			start := request(t, h, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start?route_id=account", "", nil, browserHeaders)
			if start.Code != http.StatusFound {
				t.Fatalf("start=%d", start.Code)
			}
			authorize, err := url.Parse(start.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			state := authorize.Query().Get("state")
			callbackURL := "https://user.example/api/auth/discord/callback?code=private-code&state=" + url.QueryEscape(state)
			cookies := []*http.Cookie{responseCookie(t, start, OAuthStateCookieName)}
			switch tc.name {
			case "invalid callback":
				callbackURL = "https://user.example/api/auth/discord/callback?error=access_denied&error_description=private-description&state=" + url.QueryEscape(state)
			case "missing cookie":
				cookies = nil
			case "expired state":
				f.clock.Add(DefaultOAuthStateTTL + time.Second)
			case "replayed state":
				first := request(t, h, host.StationUser, http.MethodGet, callbackURL, "", cookies, browserHeaders)
				if first.Code != http.StatusFound || first.Header().Get("Location") != "/account" {
					t.Fatalf("first callback=%d", first.Code)
				}
			case "provider rejected":
				f.provider.exchangeErr = ErrProviderUnauthorized
			case "provider unavailable":
				f.provider.exchangeErr = ErrProviderUnavailable
			case "invalid identity":
				f.provider.logins["private-code"] = DiscordLogin{Identity: DiscordIdentity{ID: "private-discord-id"}}
			case "guild gate":
				f.provider.logins["private-code"] = DiscordLogin{Identity: DiscordIdentity{ID: "private-discord-id", Username: "Alice"}, GuildMember: func(context.Context, string) (GuildMember, error) {
					return GuildMember{Roles: []string{"other-role"}}, nil
				}}
			}
			callback := request(t, h, host.StationUser, http.MethodGet, callbackURL, "", cookies, browserHeaders)
			if callback.Code != tc.status || callback.Header().Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(callback.Body.String(), tc.explanation) {
				t.Fatalf("status=%d; explanation %q missing", callback.Code, tc.explanation)
			}
			for _, private := range []string{"private-code", "private-description", "private-discord-id", state, "other-role"} {
				if strings.Contains(callback.Body.String(), private) {
					t.Fatalf("page exposed %q", private)
				}
			}
			if tc.name != "guild gate" && strings.Contains(callback.Body.String(), "required role") {
				t.Fatal("unrelated error was classified as a guild gate failure")
			}
		})
	}
}

func TestBrowserAuthNavigationFailures(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	h := browserAuthHandler(t, f)
	for _, tc := range []struct {
		path   string
		status int
		text   string
	}{
		{"/api/missing", 404, "Page not found"},
		{"/api/auth/logout", 405, "This request could not be completed"},
		{"/api/session", 401, "Please sign in again"},
	} {
		rec := request(t, h, host.StationUser, http.MethodGet, "https://user.example"+tc.path, "", nil, browserHeaders)
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.text) {
			t.Fatalf("%s status=%d", tc.path, rec.Code)
		}
		if tc.status == 405 && rec.Header().Get("Allow") != "POST" {
			t.Fatalf("Allow=%q", rec.Header().Get("Allow"))
		}
	}
	f.gate.enabled = true
	rec := request(t, h, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, browserHeaders)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "The site is under maintenance") {
		t.Fatalf("maintenance=%d", rec.Code)
	}
	f.gate.enabled = false
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key='oauth_start_rate_limit'`); err != nil {
		t.Fatal(err)
	}
	first := request(t, h, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, browserHeaders)
	if first.Code != http.StatusFound {
		t.Fatalf("first start=%d", first.Code)
	}
	limited := request(t, h, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, browserHeaders)
	if limited.Code != 429 || limited.Header().Get("Retry-After") == "" || !strings.Contains(limited.Body.String(), "Please wait a moment") {
		t.Fatalf("rate limit=%d; retry=%q", limited.Code, limited.Header().Get("Retry-After"))
	}
}
