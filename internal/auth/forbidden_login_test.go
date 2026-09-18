package auth

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/host"
)

func TestForbiddenLoginRedirectsWithoutIssuingSession(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "initial", "")
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1 WHERE discord_id='discord-1'`); err != nil {
		t.Fatal(err)
	}
	addLogin(f.provider, "banned", "discord-1")
	start := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start?route_id=account", "", nil, nil)
	authorization, _ := url.Parse(start.Header().Get("Location"))
	state := authorization.Query().Get("state")
	callback := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=banned&state="+url.QueryEscape(state), "", []*http.Cookie{responseCookie(t, start, OAuthStateCookieName), cookie}, nil)
	if callback.Code != http.StatusFound || callback.Header().Get("Location") != "/access-denied" || callback.Header().Get("Cache-Control") != "no-store" || callback.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("callback status=%d headers=%v", callback.Code, callback.Header())
	}
	for _, c := range callback.Result().Cookies() {
		if c.Value != "" || c.MaxAge >= 0 {
			t.Fatalf("denied login issued a cookie: %s", c.Name)
		}
	}
	if strings.Contains(callback.Body.String(), state) || strings.Contains(callback.Body.String(), "discord-1") {
		t.Fatal("redirect leaked identity or OAuth material")
	}
	api := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{cookie}, nil)
	if api.Code != http.StatusForbidden || !strings.Contains(api.Header().Get("Content-Type"), "application/json") || api.Header().Get("Location") != "" {
		t.Fatal("ordinary API denial must remain a JSON 403")
	}
	replay := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=banned&state="+url.QueryEscape(state), "", []*http.Cookie{responseCookie(t, start, OAuthStateCookieName)}, nil)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("consumed state replay status=%d", replay.Code)
	}
}
