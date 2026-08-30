package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPDiscordProviderNarrowExchangeAndMembership(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			if r.Method != http.MethodPost {
				t.Errorf("token method=%s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "transient-access"})
		case "/users/@me":
			if r.Header.Get("Authorization") != "Bearer transient-access" {
				t.Errorf("authorization=%q", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "discord-1", "username": "Alice", "avatar": "hash"})
		case "/users/@me/guilds/guild-1/member":
			_ = json.NewEncoder(w).Encode(map[string]any{"nick": "Guild Alice", "avatar": "guild-hash", "roles": []string{"role-1"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider, err := NewHTTPDiscordProvider(HTTPDiscordProviderConfig{ClientID: "client", ClientSecret: "secret", APIBaseURL: server.URL, AuthorizeEndpoint: server.URL + "/authorize", TokenEndpoint: server.URL + "/oauth2/token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := provider.AuthorizationURL(context.Background(), DiscordAuthorizeRequest{ClientID: "client", RedirectURI: "https://user.example/api/auth/discord/callback", State: "valid-state", Intent: OAuthIntentLogin})
	if err != nil || authorization == "" {
		t.Fatalf("authorization=%q err=%v", authorization, err)
	}
	login, err := provider.Exchange(context.Background(), "valid-code", "https://user.example/api/auth/discord/callback")
	if err != nil {
		t.Fatal(err)
	}
	if login.Identity.ID != "discord-1" || login.Identity.Username != "Alice" || login.GuildMember == nil {
		t.Fatalf("login=%+v", login)
	}
	member, err := login.GuildMember(context.Background(), "guild-1")
	if err != nil || member.Nick != "Guild Alice" || !hasRole(member.Roles, "role-1") {
		t.Fatalf("member=%+v err=%v", member, err)
	}
}

func TestHTTPDiscordProviderClassifiesServerFailureUnavailable(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"down"}`))
	}))
	defer server.Close()
	provider, err := NewHTTPDiscordProvider(HTTPDiscordProviderConfig{ClientID: "client", ClientSecret: "secret", APIBaseURL: server.URL, AuthorizeEndpoint: server.URL + "/authorize", TokenEndpoint: server.URL + "/token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Exchange(context.Background(), "code", "https://user.example/api/auth/discord/callback"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("exchange error=%v", err)
	}
}

func TestHTTPDiscordProviderClassifiesGuildMembershipOutcomes(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "transient-access"})
		case "/users/@me":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "discord-1", "username": "Alice"})
		case "/users/@me/guilds/missing/member":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Unknown Member"})
		case "/users/@me/guilds/down/member":
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "upstream unavailable"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider, err := NewHTTPDiscordProvider(HTTPDiscordProviderConfig{ClientID: "client", ClientSecret: "secret", APIBaseURL: server.URL, AuthorizeEndpoint: server.URL + "/authorize", TokenEndpoint: server.URL + "/oauth2/token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	login, err := provider.Exchange(context.Background(), "valid-code", "https://user.example/api/auth/discord/callback")
	if err != nil {
		t.Fatal(err)
	}
	member, err := login.GuildMember(context.Background(), "missing")
	if err != nil || member.Nick != "" || member.Avatar != "" || len(member.Roles) != 0 {
		t.Fatalf("definite nonmember=%+v err=%v", member, err)
	}
	if _, err := login.GuildMember(context.Background(), "down"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("server failure=%v", err)
	}
}
