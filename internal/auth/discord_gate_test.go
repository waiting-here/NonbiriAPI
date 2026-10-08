package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
)

func discordGateCallback(t *testing.T, f *runtimeFixture, login DiscordLogin) *httptest.ResponseRecorder {
	t.Helper()
	f.provider.mu.Lock()
	f.provider.logins["gate-login"] = login
	f.provider.mu.Unlock()
	start := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, nil)
	authorization, err := url.Parse(start.Header().Get("Location"))
	if err != nil || start.Code != http.StatusFound {
		t.Fatal("OAuth start", start.Code, err)
	}
	return request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=gate-login&state="+url.QueryEscape(authorization.Query().Get("state")), "", []*http.Cookie{responseCookie(t, start, OAuthStateCookieName)}, nil)
}

func setDiscordGate(t *testing.T, f *runtimeFixture, global, policy string) {
	t.Helper()
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value=? WHERE key='discord_registered_user_gate_exempt'`, global); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_gate_policy=? WHERE discord_id='discord-1'`, policy); err != nil {
		t.Fatal(err)
	}
}

func TestExistingDiscordGateGlobalAndUserPolicyMatrix(t *testing.T) {
	for _, global := range []string{"0", "1"} {
		for _, policy := range []string{db.DiscordGatePolicyInherit, db.DiscordGatePolicyRequire, db.DiscordGatePolicyExempt} {
			t.Run(global+"/"+policy, func(t *testing.T) {
				f := newRuntimeFixture(t, nil)
				cookie := loginUser(t, f, "initial", "")
				setDiscordGate(t, f, global, policy)
				if _, err := f.store.DB().Exec(`UPDATE site_config SET value='0' WHERE key='registration_open'`); err != nil {
					t.Fatal(err)
				}
				login := DiscordLogin{Identity: DiscordIdentity{ID: "discord-1", Username: "Alice"}, GuildMember: func(context.Context, string) (GuildMember, error) {
					return GuildMember{Roles: []string{"other-role"}}, nil
				}}
				want := http.StatusUnauthorized
				if policy == db.DiscordGatePolicyExempt || policy == db.DiscordGatePolicyInherit && global == "1" {
					want = http.StatusFound
				}
				got := discordGateCallback(t, f, login)
				if got.Code != want {
					t.Fatalf("missing role: got %d %s, want %d", got.Code, got.Body, want)
				}
				if want == http.StatusUnauthorized {
					session := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{cookie}, nil)
					if session.Code != http.StatusOK {
						t.Fatal("login policy invalidated an existing session", session.Code)
					}
				}
				login.GuildMember = func(context.Context, string) (GuildMember, error) { return GuildMember{Roles: []string{"role-1"}}, nil }
				if got := discordGateCallback(t, f, login); got.Code != http.StatusFound {
					t.Fatal("matching member denied", got.Code, got.Body)
				}
			})
		}
	}
}

func TestDiscordGateSettingsRecheckedAfterMembershipLookup(t *testing.T) {
	for _, tc := range []struct {
		name, initialGlobal, initialPolicy, query string
		status                                    int
	}{
		{"global strict", "1", "inherit", `UPDATE site_config SET value='0' WHERE key='discord_registered_user_gate_exempt'`, http.StatusUnauthorized},
		{"user strict", "1", "exempt", `UPDATE users SET discord_gate_policy='require' WHERE discord_id='discord-1'`, http.StatusUnauthorized},
		{"global exempt", "0", "inherit", `UPDATE site_config SET value='1' WHERE key='discord_registered_user_gate_exempt'`, http.StatusFound},
		{"user exempt", "0", "require", `UPDATE users SET discord_gate_policy='exempt' WHERE discord_id='discord-1'`, http.StatusFound},
		{"guild changed", "0", "require", `UPDATE site_config SET value='guild-2' WHERE key='discord_guild_id'`, http.StatusServiceUnavailable},
		{"role changed", "0", "require", `UPDATE site_config SET value='role-2' WHERE key='discord_role_id'`, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRuntimeFixture(t, nil)
			loginUser(t, f, "initial", "")
			setDiscordGate(t, f, tc.initialGlobal, tc.initialPolicy)
			login := DiscordLogin{Identity: DiscordIdentity{ID: "discord-1", Username: "Alice"}, GuildMember: func(context.Context, string) (GuildMember, error) {
				if _, err := f.store.DB().Exec(tc.query); err != nil {
					t.Fatal(err)
				}
				if tc.name == "guild changed" || tc.name == "role changed" {
					return GuildMember{Roles: []string{"role-1"}}, nil
				}
				return GuildMember{}, nil
			}}
			if got := discordGateCallback(t, f, login); got.Code != tc.status {
				t.Fatal("stale configuration used", got.Code, got.Body)
			}
		})
	}
}

func TestDiscordGateExemptionDoesNotAuthorizeOtherIdentityOrRestrictedAccount(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"banned", `UPDATE users SET is_banned=1 WHERE discord_id='discord-1'`},
		{"deletion in progress", `INSERT INTO user_deletion_markers(user_id) VALUES(1)`},
		{"deleted", `DELETE FROM users WHERE discord_id='discord-1'`},
		{"different identity", `UPDATE users SET discord_id='other-identity' WHERE discord_id='discord-1'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRuntimeFixture(t, nil)
			loginUser(t, f, "initial", "")
			setDiscordGate(t, f, "1", "exempt")
			if _, err := f.store.DB().Exec(tc.query); err != nil {
				t.Fatal(err)
			}
			if token, _, err := f.runtime.refreshExistingUser(t.Context(), 1, DiscordIdentity{ID: "discord-1", Username: "Alice"}, nil); err == nil || token != "" {
				t.Fatal("restricted account admitted", token, err)
			}
		})
	}
}

func TestDiscordGateExemptionRetainsBlacklistChecks(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	loginUser(t, f, "initial", "")
	setDiscordGate(t, f, "1", "exempt")
	if _, err := f.store.DB().Exec(`UPDATE users SET discord_id='123456789012345678',is_banned=1 WHERE id=1; INSERT INTO discord_blacklist(discord_id,reason,created_at) VALUES('123456789012345678','test',0)`); err != nil {
		t.Fatal(err)
	}
	if token, _, err := f.runtime.refreshExistingUser(t.Context(), 1, DiscordIdentity{ID: "123456789012345678", Username: "Alice"}, nil); err == nil || token != "" {
		t.Fatal("blacklisted account admitted", err)
	}
}

func TestDiscordGateExemptionNeverAppliesToNewRegistration(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	setDiscordGate(t, f, "1", "inherit")
	login := DiscordLogin{Identity: DiscordIdentity{ID: "new-identity", Username: "New"}, GuildMember: func(context.Context, string) (GuildMember, error) { return GuildMember{}, nil }}
	if got := discordGateCallback(t, f, login); got.Code != http.StatusUnauthorized {
		t.Fatal("new account bypassed membership", got.Code, got.Body)
	}
	var users int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM users WHERE discord_id='new-identity'`).Scan(&users); err != nil || users != 0 {
		t.Fatal(users, err)
	}
	login.GuildMember = func(context.Context, string) (GuildMember, error) { return GuildMember{Roles: []string{"role-1"}}, nil }
	if got := discordGateCallback(t, f, login); got.Code != http.StatusFound {
		t.Fatal("eligible new account denied", got.Code, got.Body)
	}
}

func TestDiscordGateStrictUnavailableAndExemptInvalidIdentity(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	loginUser(t, f, "initial", "")
	login := DiscordLogin{Identity: DiscordIdentity{ID: "discord-1", Username: "Alice"}, GuildMember: func(context.Context, string) (GuildMember, error) { return GuildMember{}, ErrProviderUnavailable }}
	if got := discordGateCallback(t, f, login); got.Code != http.StatusServiceUnavailable {
		t.Fatal(got.Code, got.Body)
	}
	setDiscordGate(t, f, "1", "exempt")
	login.Identity.Username = "invalid\nidentity"
	if got := discordGateCallback(t, f, login); got.Code != http.StatusUnauthorized {
		t.Fatal("invalid OAuth identity accepted", got.Code, got.Body)
	}
	_, _, err := f.runtime.refreshExistingUser(t.Context(), 1, login.Identity, nil)
	if !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("invalid verified identity: %v", err)
	}
}
