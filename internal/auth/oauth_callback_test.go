package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/host"
)

func TestDiscordCallbackIssuerQuery(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		valid       bool
	}{
		{"legacy", "code=code&state=state", true},
		{"issuer", "code=code&state=state&iss=https://discord.com", true},
		{"encoded issuer", "iss=https%3A%2F%2Fdiscord.com&state=state&code=code", true},
		{"wrong issuer", "code=code&state=state&iss=https://other.example", false},
		{"trailing slash", "code=code&state=state&iss=https://discord.com/", false},
		{"empty issuer", "code=code&state=state&iss=", false},
		{"duplicate issuer", "code=code&state=state&iss=https://discord.com&iss=https://discord.com", false},
		{"duplicate code", "code=code&code=other&state=state&iss=https://discord.com", false},
		{"duplicate state", "code=code&state=state&state=other&iss=https://discord.com", false},
		{"unknown parameter", "code=code&state=state&iss=https://discord.com&extra=1", true},
		{"legacy extension", "code=code&state=state&scope=identify", true},
		{"missing code", "state=state&iss=https://discord.com", false},
		{"empty code", "code=&state=state&iss=https://discord.com", false},
		{"empty state", "code=code&state=&iss=https://discord.com", false},
		{"malformed escape", "code=code&state=state&iss=%ZZ", false},
		{"overlong query", "code=code&state=state&iss=" + strings.Repeat("a", maxCallbackQueryBytes), false},
		{"authorization error", "error=access_denied&error_description=Denied&state=state&iss=https://discord.com", false},
		{"mixed error details", "code=code&state=state&error_description=Denied", false},
		{"mixed error URI", "code=code&state=state&error_uri=https://example.com/error", false},
		{"mixed success and error", "code=code&state=state&iss=https://discord.com&error=access_denied", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/auth/discord/callback?"+tc.query, nil)
			code, state, ok := parseCallbackQuery(req)
			if ok != tc.valid || (ok && (code != "code" || state != "state")) {
				t.Fatalf("code=%q state=%q valid=%v; want valid=%v", code, state, ok, tc.valid)
			}
		})
	}
}

func TestDiscordCallbackIssuerLoginAndStateBinding(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	handler := f.runtime.UserHandler()
	addLogin(f.provider, "issuer-login", "discord-issuer")
	start := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start?route_id=account", "", nil, nil)
	if start.Code != http.StatusFound {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	stateCookie := responseCookie(t, start, OAuthStateCookieName)
	authorization, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	callbackURL := "https://user.example/api/auth/discord/callback?code=issuer-login&state=" + url.QueryEscape(authorization.Query().Get("state"))
	invalid := request(t, handler, host.StationUser, http.MethodGet, callbackURL+"&iss=https://other.example", "", []*http.Cookie{stateCookie}, nil)
	if invalid.Code != http.StatusBadRequest || f.provider.exchangeCalls != 0 {
		t.Fatalf("invalid issuer status=%d exchanges=%d", invalid.Code, f.provider.exchangeCalls)
	}
	callbackURL += "&iss=https%3A%2F%2Fdiscord.com&future_parameter=value"
	missingCookie := request(t, handler, host.StationUser, http.MethodGet, callbackURL, "", nil, nil)
	if missingCookie.Code != http.StatusUnauthorized || f.provider.exchangeCalls != 0 {
		t.Fatalf("missing cookie status=%d exchanges=%d", missingCookie.Code, f.provider.exchangeCalls)
	}
	// Invalid callbacks must not consume the pending state.
	success := request(t, handler, host.StationUser, http.MethodGet, callbackURL, "", []*http.Cookie{stateCookie}, nil)
	if success.Code != http.StatusFound || success.Header().Get("Location") != "/account" || f.provider.exchangeCalls != 1 {
		t.Fatalf("login status=%d location=%q exchanges=%d body=%s", success.Code, success.Header().Get("Location"), f.provider.exchangeCalls, success.Body.String())
	}
	session := responseCookie(t, success, UserSessionCookieName)
	current := request(t, handler, host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{session}, nil)
	if current.Code != http.StatusOK {
		t.Fatalf("session=%d %s", current.Code, current.Body.String())
	}
	replay := request(t, handler, host.StationUser, http.MethodGet, callbackURL, "", []*http.Cookie{stateCookie}, nil)
	if replay.Code != http.StatusUnauthorized || f.provider.exchangeCalls != 1 {
		t.Fatalf("replay status=%d exchanges=%d", replay.Code, f.provider.exchangeCalls)
	}
}
