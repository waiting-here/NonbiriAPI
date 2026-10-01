package auth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
)

func TestVerifiedForbiddenLoginProjectsOnlyOwnCurrentRestrictions(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	loginUser(t, f, "initial", "")
	var user, started int64
	if err := f.store.DB().QueryRow(`SELECT id,created_at FROM users WHERE discord_id='discord-1'`).Scan(&user, &started); err != nil {
		t.Fatal(err)
	}
	id, err := db.GenerateOpaqueID("abc_")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1,auto_banned=1,banned_until=? WHERE id=?`, started+3600, user); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.DB().Exec(`UPDATE users SET discord_id='123456789012345678' WHERE id=?`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`INSERT INTO abuse_cases(id,user_id,kind,reason_code,started_at,ends_at,state,result) VALUES(?,?,'ban','charity_rpm',?,?,'active','applied')`, id, user, started, started+3600); err != nil {
		t.Fatal(err)
	}
	addLogin(f.provider, "denied", "123456789012345678")
	start := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start?route_id=account", "", nil, nil)
	authorization, _ := url.Parse(start.Header().Get("Location"))
	callback := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=denied&state="+url.QueryEscape(authorization.Query().Get("state")), "", []*http.Cookie{responseCookie(t, start, OAuthStateCookieName)}, nil)
	location, err := url.Parse(callback.Header().Get("Location"))
	if err != nil || callback.Code != http.StatusFound || location.Path != "/access-denied" || location.RawQuery != "" {
		t.Fatal(callback.Code, location, err)
	}
	if location.Fragment != "" {
		t.Fatal("denial fragment must be empty")
	}
	grant := responseCookie(t, callback, DenialCookieName)
	if !grant.HttpOnly || !grant.Secure || grant.MaxAge != 300 || grant.Path != denialCookiePath || grant.SameSite != http.SameSiteLaxMode {
		t.Fatal(grant)
	}
	read := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/access-denied-reasons", "", []*http.Cookie{grant}, nil)
	var page DenialPage
	if err = json.Unmarshal(read.Body.Bytes(), &page); err != nil || read.Code != 200 || len(page.Items) != 1 || page.Items[0].Automatic == nil || page.Items[0].Automatic.ReasonCode != "charity_rpm" {
		t.Fatal(read.Code, read.Body.String(), err)
	}
	for _, c := range callback.Result().Cookies() {
		if c.Name != DenialCookieName && (c.Value != "" || c.MaxAge >= 0) {
			t.Fatal("denial issued cookie", c.Name)
		}
	}
	if strings.Contains(read.Body.String(), "123456789012345678") || strings.Contains(read.Body.String(), id) {
		t.Fatal("denial leaked identity")
	}
}

func TestForbiddenLoginRedirectsWithoutIssuingSession(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "initial", "")
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1 WHERE discord_id='discord-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_id='123456789012345678' WHERE discord_id='discord-1'`); err != nil {
		t.Fatal(err)
	}
	addLogin(f.provider, "banned", "123456789012345678")
	start := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start?route_id=account", "", nil, nil)
	authorization, _ := url.Parse(start.Header().Get("Location"))
	state := authorization.Query().Get("state")
	callback := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=banned&state="+url.QueryEscape(state), "", []*http.Cookie{responseCookie(t, start, OAuthStateCookieName), cookie}, nil)
	if callback.Code != http.StatusFound || callback.Header().Get("Location") != "/access-denied" || callback.Header().Get("Cache-Control") != "no-store" || callback.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("callback status=%d headers=%v", callback.Code, callback.Header())
	}
	for _, c := range callback.Result().Cookies() {
		if c.Name != DenialCookieName && (c.Value != "" || c.MaxAge >= 0) {
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
