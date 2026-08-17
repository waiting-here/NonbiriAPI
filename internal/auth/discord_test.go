package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPDiscordProviderExchangeIdentityAndMembership(t *testing.T) {
	var receivedToken atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("code") != "auth-code" || r.Form.Get("client_secret") != "client-secret" {
				http.Error(w, "bad token request", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"access-token"}`))
		case "/users/@me":
			if r.Header.Get("Authorization") != "Bearer access-token" {
				http.Error(w, "missing token", http.StatusUnauthorized)
				return
			}
			receivedToken.Add(1)
			_, _ = w.Write([]byte(`{"id":"discord-id","username":"discord-name","global_name":"Global Name","avatar":"avatar-hash"}`))
		case "/users/@me/guilds/guild-id/member":
			if r.Header.Get("Authorization") != "Bearer access-token" {
				http.Error(w, "missing token", http.StatusUnauthorized)
				return
			}
			receivedToken.Add(1)
			_, _ = w.Write([]byte(`{"roles":["other-role","role-id"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := NewHTTPDiscordProvider(HTTPDiscordProviderConfig{
		ClientID: "client-id", ClientSecret: "client-secret", Scopes: "identify guilds.members.read",
		APIBaseURL: server.URL, AuthorizeEndpoint: server.URL + "/authorize", TokenEndpoint: server.URL + "/oauth2/token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	location, err := provider.AuthorizationURL(context.Background(), DiscordAuthorizeRequest{
		ClientID: "client-id", RedirectURI: "https://example.com/api/auth/discord/callback", State: "signed-state", Intent: OAuthIntentLogin,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(location)
	if err != nil || parsed.Query().Get("state") != "signed-state" || parsed.Query().Get("scope") == "" {
		t.Fatalf("authorization URL=%q err=%v", location, err)
	}
	login, err := provider.Exchange(context.Background(), "auth-code", "https://example.com/api/auth/discord/callback")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if login.Identity.ID != "discord-id" || login.Identity.Username != "Global Name" {
		t.Fatalf("identity=%#v", login.Identity)
	}
	matched, err := login.HasGuildRole(context.Background(), "guild-id", "role-id")
	if err != nil || !matched {
		t.Fatalf("HasGuildRole matched=%t err=%v", matched, err)
	}
	if receivedToken.Load() != 2 {
		t.Fatalf("provider did not perform both token-authenticated calls: %d", receivedToken.Load())
	}
}

func TestHTTPDiscordProviderRejectsRedirectsAndBoundsBodies(t *testing.T) {
	var redirected atomic.Int32
	redirectTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"leaked-token"}`))
	}))
	defer redirectTarget.Close()
	redirectSource := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	defer redirectSource.Close()

	provider, err := NewHTTPDiscordProvider(HTTPDiscordProviderConfig{
		ClientID: "client-id", ClientSecret: "super-secret", APIBaseURL: redirectSource.URL,
		TokenEndpoint: redirectSource.URL + "/oauth2/token", AuthorizeEndpoint: redirectSource.URL + "/authorize",
		HTTPClient: redirectSource.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	login, err := provider.Exchange(context.Background(), "auth-code", "https://example.com/callback")
	if err == nil || !errors.Is(err, ErrProviderUnauthorized) && !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("redirect Exchange login=%#v err=%v", login, err)
	}
	if redirected.Load() != 0 {
		t.Fatal("provider followed redirect and sent credentials to another endpoint")
	}

	large := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" {
			_, _ = w.Write([]byte(`{"access_token":"access-token"}`))
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf(`{"id":"%s"}`, strings.Repeat("x", maxDiscordResponseBytes))))
	}))
	defer large.Close()
	provider, err = NewHTTPDiscordProvider(HTTPDiscordProviderConfig{
		ClientID: "client-id", ClientSecret: "client-secret", APIBaseURL: large.URL,
		TokenEndpoint: large.URL + "/oauth2/token", AuthorizeEndpoint: large.URL + "/authorize",
		HTTPClient: large.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Exchange(context.Background(), "auth-code", "https://example.com/callback")
	if !errors.Is(err, ErrProviderUnavailable) && !errors.Is(err, ErrProviderUnauthorized) {
		t.Fatalf("oversized provider body error=%v", err)
	}
	if strings.Contains(err.Error(), "access-token") || strings.Contains(err.Error(), "client-secret") {
		t.Fatalf("provider error echoed secret material: %v", err)
	}
}

func TestHTTPDiscordProviderRejectsInvalidProviderAndIdentityInput(t *testing.T) {
	cases := []HTTPDiscordProviderConfig{
		{ClientID: "", ClientSecret: "secret"},
		{ClientID: "id", ClientSecret: ""},
		{ClientID: "id", ClientSecret: "secret", APIBaseURL: "ftp://discord.example"},
		{ClientID: "id", ClientSecret: "secret", APIBaseURL: "http://discord.example"},
		{ClientID: "id", ClientSecret: "secret", APIBaseURL: "https://user:password@discord.example"},
	}
	for i, config := range cases {
		if _, err := NewHTTPDiscordProvider(config); err == nil {
			t.Fatalf("case %d accepted invalid provider config", i)
		}
	}
	provider, err := NewHTTPDiscordProvider(HTTPDiscordProviderConfig{ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.AuthorizationURL(context.Background(), DiscordAuthorizeRequest{RedirectURI: "\n", State: "state", Intent: OAuthIntentLogin}); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("invalid authorization request error=%v", err)
	}
	if _, err := provider.Exchange(context.Background(), "code\n", "https://example.com/callback"); !errors.Is(err, ErrProviderUnauthorized) {
		t.Fatalf("invalid OAuth code error=%v", err)
	}
}

func TestDiscordBodyHelperBoundsAndClearsContract(t *testing.T) {
	body, err := readDiscordBody(strings.NewReader(`{"ok":true}`))
	if err != nil || len(body) == 0 {
		t.Fatalf("readDiscordBody body=%q err=%v", body, err)
	}
	clear(body)
	if _, err := readDiscordBody(strings.NewReader(strings.Repeat("x", maxDiscordResponseBytes+1))); err == nil {
		t.Fatal("readDiscordBody accepted an oversized response")
	}
	if _, err := readDiscordBody(nil); err == nil {
		t.Fatal("readDiscordBody accepted nil reader")
	}
}

func TestProviderMalformedJSONDoesNotLeakResponseBody(t *testing.T) {
	secretText := "discord-secret-body"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" {
			_, _ = w.Write([]byte(`{"access_token":"access-token"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(secretText + " not-json"))
	}))
	defer server.Close()
	provider, err := NewHTTPDiscordProvider(HTTPDiscordProviderConfig{ClientID: "id", ClientSecret: "secret", APIBaseURL: server.URL, TokenEndpoint: server.URL + "/oauth2/token", AuthorizeEndpoint: server.URL + "/authorize", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Exchange(context.Background(), "code", "https://example.com/callback")
	if err == nil || strings.Contains(err.Error(), secretText) {
		t.Fatalf("malformed response error=%v leaked provider body", err)
	}
}
