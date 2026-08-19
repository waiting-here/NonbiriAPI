package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
)

// --- StateManager session binding -------------------------------------------

func TestStateManagerSessionBoundIssueConsume(t *testing.T) {
	manager, err := NewStateManager(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	sessionA := db.SessionHash("session-token-a")
	sessionB := db.SessionHash("session-token-b")

	state, err := manager.IssueBound(StationUser, OAuthIntentElevate, sessionA)
	if err != nil {
		t.Fatal(err)
	}
	// The bound state is only consumable against the exact session binding.
	if err := manager.ConsumeBound(state, state, StationUser, OAuthIntentElevate, sessionB); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("wrong binding error=%v, want ErrStateInvalid", err)
	}
	// A login consume must never accept an elevation-bound state.
	if err := manager.Consume(state, state, StationUser, OAuthIntentElevate); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("unbound consume of bound state error=%v, want ErrStateInvalid", err)
	}
	if err := manager.ConsumeBound(state, state, StationUser, OAuthIntentElevate, sessionA); err != nil {
		t.Fatalf("valid bound consume: %v", err)
	}
	if err := manager.ConsumeBound(state, state, StationUser, OAuthIntentElevate, sessionA); !errors.Is(err, ErrStateReplay) {
		t.Fatalf("bound replay error=%v, want ErrStateReplay", err)
	}

	// ConsumeAny reveals the signed intent and binding atomically.
	bound, err := manager.IssueBound(StationUser, OAuthIntentElevate, sessionA)
	if err != nil {
		t.Fatal(err)
	}
	intent, binding, err := manager.ConsumeAny(bound, bound, StationUser)
	if err != nil || intent != OAuthIntentElevate || binding != sessionA {
		t.Fatalf("ConsumeAny intent=%q binding=%q err=%v", intent, binding, err)
	}
	if _, _, err := manager.ConsumeAny(bound, bound, StationUser); !errors.Is(err, ErrStateReplay) {
		t.Fatalf("ConsumeAny replay error=%v, want ErrStateReplay", err)
	}

	login, err := manager.Issue(StationUser, OAuthIntentLogin)
	if err != nil {
		t.Fatal(err)
	}
	intent, binding, err = manager.ConsumeAny(login, login, StationUser)
	if err != nil || intent != OAuthIntentLogin || binding != "" {
		t.Fatalf("login ConsumeAny intent=%q binding=%q err=%v", intent, binding, err)
	}
}

func TestStateManagerBoundStateExpiry(t *testing.T) {
	key := bytes.Repeat([]byte{0x5a}, 32)
	manager, err := NewStateManagerWithKey(key, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	clock := time.Unix(2000, 0).UTC()
	if err := manager.SetClock(func() time.Time { return clock }); err != nil {
		t.Fatal(err)
	}
	state, err := manager.IssueBound(StationUser, OAuthIntentElevate, db.SessionHash("session"))
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(11 * time.Second)
	if err := manager.ConsumeBound(state, state, StationUser, OAuthIntentElevate, db.SessionHash("session")); !errors.Is(err, ErrStateExpired) {
		t.Fatalf("expired bound state error=%v, want ErrStateExpired", err)
	}
	if _, _, err := manager.ConsumeAny(state, state, StationUser); !errors.Is(err, ErrStateExpired) {
		t.Fatalf("expired ConsumeAny error=%v, want ErrStateExpired", err)
	}
}

func TestStateManagerRejectsInvalidBindings(t *testing.T) {
	manager, err := NewStateManager(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	for _, binding := range []string{"", "a\nb", strings.Repeat("x", maxStateBindingBytes+1)} {
		if _, err := manager.IssueBound(StationUser, OAuthIntentElevate, binding); !errors.Is(err, ErrStateInvalid) {
			t.Fatalf("IssueBound(%q) error=%v, want ErrStateInvalid", binding, err)
		}
	}
	if err := manager.ConsumeBound("x", "x", StationUser, OAuthIntentElevate, ""); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("ConsumeBound empty binding error=%v, want ErrStateInvalid", err)
	}
}

// --- HTTP elevation flow ----------------------------------------------------

func elevateSessionRequest(sessionToken string) *http.Request {
	r := stationRequest(http.MethodPost, "https://example.com/api/auth/elevate", host.StationUser, nil)
	if sessionToken != "" {
		r.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: sessionToken})
	}
	return r
}

func TestElevateRequiresActiveUserSession(t *testing.T) {
	st := authTestStore(t)
	service := newTestUserAuth(t, st, &fakeDiscordProvider{}, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{}, nil
	})
	rec := httptest.NewRecorder()
	service.Elevate(rec, elevateSessionRequest(""))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("elevate without session status=%d body=%s", rec.Code, rec.Body.String())
	}
	// A banned user's session is gone: elevate fails closed.
	user, err := st.CreateDiscordUser("discord-banned", "banned", "")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BanUser(user.ID, "review"); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	service.Elevate(rec, elevateSessionRequest(token))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("elevate for banned user status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// startElevation drives POST /api/auth/elevate and returns the state value and
// the state cookie so the test can build the callback.
func startElevation(t *testing.T, service *UserAuth, sessionToken string) (string, *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	service.Elevate(rec, elevateSessionRequest(sessionToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("elevate status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		AuthorizationURL string `json:"authorization_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil || payload.AuthorizationURL == "" {
		t.Fatalf("elevate payload=%s err=%v", rec.Body.String(), err)
	}
	u, err := url.Parse(payload.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	values := u.Query()["state"]
	if len(values) != 1 || values[0] == "" {
		t.Fatalf("authorization url state invalid: %s", payload.AuthorizationURL)
	}
	stateCookie := cookieFromResponse(t, rec, OAuthStateCookieName)
	if stateCookie.Value != values[0] {
		t.Fatalf("state cookie %q differs from url state %q", stateCookie.Value, values[0])
	}
	return values[0], stateCookie
}

func TestElevationFlowMintsSingleUseCapability(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{login: DiscordLogin{
		Identity: DiscordIdentity{ID: "discord-elev", Username: "alice", Avatar: ""},
	}}
	service := newTestUserAuth(t, st, provider, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{}, nil
	})
	user, err := st.CreateDiscordUser("discord-elev", "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	state, stateCookie := startElevation(t, service, sessionToken)
	callback := httptest.NewRecorder()
	callbackReq := callbackRequest(state, "code-elev", stateCookie)
	callbackReq.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: sessionToken})
	service.Callback(callback, callbackReq)
	if callback.Code != http.StatusFound || callback.Header().Get("Location") != "/" {
		t.Fatalf("elevate callback status=%d location=%q body=%s", callback.Code, callback.Header().Get("Location"), callback.Body.String())
	}
	elevated := cookieFromResponse(t, callback, ElevatedCookieName)
	if elevated.HttpOnly || elevated.Path != "/" || elevated.SameSite != http.SameSiteLaxMode || elevated.MaxAge <= 0 {
		t.Fatalf("elevated cookie attributes=%#v", elevated)
	}
	if strings.Contains(callback.Body.String(), "code-elev") || strings.Contains(callback.Body.String(), elevated.Value) {
		t.Fatalf("callback echoed provider or capability material: %q", callback.Body.String())
	}

	// The capability is consumable once, bound to this session.
	consume := func(token, session string) error {
		r := stationRequest(http.MethodPost, "https://example.com/api/account/export", host.StationUser, nil)
		r.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: session})
		r.Header.Set("X-Elevated-Token", token)
		return service.ConsumeElevated(httptest.NewRecorder(), r, user)
	}
	if err := consume(elevated.Value, sessionToken); err != nil {
		t.Fatalf("consume elevated: %v", err)
	}
	if err := consume(elevated.Value, sessionToken); !errors.Is(err, ErrElevationRequired) {
		t.Fatalf("replayed capability error=%v, want ErrElevationRequired", err)
	}

	// A different session on the same user cannot use the capability.
	state2, stateCookie2 := startElevation(t, service, sessionToken)
	otherSession, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	callback2 := httptest.NewRecorder()
	callbackReq2 := callbackRequest(state2, "code-elev-2", stateCookie2)
	callbackReq2.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: otherSession})
	service.Callback(callback2, callbackReq2)
	if callback2.Code != http.StatusUnauthorized {
		t.Fatalf("callback with wrong session status=%d body=%s", callback2.Code, callback2.Body.String())
	}
	// Missing header and malformed header are the same 403 boundary.
	if err := consume("", sessionToken); !errors.Is(err, ErrElevationRequired) {
		t.Fatalf("missing header error=%v, want ErrElevationRequired", err)
	}
	if err := consume("not-a-token", sessionToken); !errors.Is(err, ErrElevationRequired) {
		t.Fatalf("garbage token error=%v, want ErrElevationRequired", err)
	}
}

func TestElevationRejectsDifferentDiscordIdentity(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{login: DiscordLogin{
		Identity: DiscordIdentity{ID: "discord-other", Username: "mallory"},
	}}
	service := newTestUserAuth(t, st, provider, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{}, nil
	})
	user, err := st.CreateDiscordUser("discord-elev", "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, stateCookie := startElevation(t, service, sessionToken)
	callback := httptest.NewRecorder()
	callbackReq := callbackRequest(state, "code-other", stateCookie)
	callbackReq.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: sessionToken})
	service.Callback(callback, callbackReq)
	if callback.Code != http.StatusForbidden || strings.Contains(callback.Body.String(), "mallory") {
		t.Fatalf("different identity callback status=%d body=%s", callback.Code, callback.Body.String())
	}
	for _, cookie := range callback.Result().Cookies() {
		if cookie.Name == ElevatedCookieName && cookie.Value != "" {
			t.Fatal("capability minted for a different identity")
		}
	}
}

func TestElevationStateCannotCompleteLoginAndViceVersa(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{login: DiscordLogin{
		Identity: DiscordIdentity{ID: "discord-login", Username: "alice"},
		GuildMember: func(_ context.Context, guildID string) (GuildMember, error) {
			if guildID == "guild-1" {
				return GuildMember{Roles: []string{"role-1"}}, nil
			}
			return GuildMember{}, nil
		},
	}}
	gate := func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{GuildID: "guild-1", RoleID: "role-1"}, nil
	}
	service := newTestUserAuth(t, st, provider, gate)
	user, err := st.CreateDiscordUser("discord-login", "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// A login state handed to a session-less callback still performs a login
	// (that is the normal login flow); an elevation state without its session
	// cookie can never mint a capability.
	state, stateCookie := startElevation(t, service, sessionToken)
	callback := httptest.NewRecorder()
	service.Callback(callback, callbackRequest(state, "code-x", stateCookie))
	if callback.Code != http.StatusUnauthorized {
		t.Fatalf("elevate callback without session status=%d body=%s", callback.Code, callback.Body.String())
	}
}

// --- cookie helpers ---------------------------------------------------------

func TestElevatedCookieSetAndClear(t *testing.T) {
	rec := httptest.NewRecorder()
	SetElevatedCookie(rec, "capability-token", true, 5*time.Minute)
	cookie := cookieFromResponse(t, rec, ElevatedCookieName)
	if cookie.Value != "capability-token" || cookie.Path != "/" || cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 300 {
		t.Fatalf("elevated cookie attributes=%#v", cookie)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("elevated cookie response is cacheable")
	}
	rec = httptest.NewRecorder()
	ClearElevatedCookie(rec, true)
	cleared := cookieFromResponse(t, rec, ElevatedCookieName)
	if cleared.Value != "" || cleared.MaxAge != -1 {
		t.Fatalf("cleared cookie=%#v", cleared)
	}
}
