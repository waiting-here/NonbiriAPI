package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
)

func TestDiscordRegistrationSessionAndDeepLink(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "first", "?route_id=endpoint-detail&resource_id=ep_A")
	var users, wallets, callerKeys int
	for query, target := range map[string]*int{`SELECT COUNT(*) FROM users WHERE discord_id='discord-1' AND is_admin=0`: &users, `SELECT COUNT(*) FROM credit_accounts WHERE kind='user'`: &wallets, `SELECT COUNT(*) FROM caller_keys WHERE generation=0 AND key_hash IS NULL`: &callerKeys} {
		if err := f.store.DB().QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if users != 1 || wallets != 1 || callerKeys != 1 {
		t.Fatalf("registration rows users=%d wallets=%d caller_keys=%d", users, wallets, callerKeys)
	}
	rec := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{cookie}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope UserEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.User.ID != "1" || envelope.User.Username != "Alice" || envelope.User.Avatar == nil || envelope.User.GuildNick == nil || envelope.User.GuildAvatarURL == nil || !strings.HasPrefix(*envelope.User.GuildAvatarURL, "https://cdn.discordapp.com/guilds/") {
		t.Fatalf("envelope=%+v", envelope.User)
	}
	if envelope.User.Balance != "0" || envelope.User.DonationCredit != "0" || envelope.User.EffectiveLevel != 1 || envelope.User.LevelDisplayName != "Lv. 1" {
		t.Fatalf("economy/level=%+v", envelope.User)
	}
	usage := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/me/usage", "", []*http.Cookie{cookie}, nil)
	if usage.Code != http.StatusOK {
		t.Fatalf("usage status=%d body=%s", usage.Code, usage.Body.String())
	}
	var summary UsageSummary
	if err := json.Unmarshal(usage.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary != envelope.User.Usage {
		t.Fatalf("usage mismatch got=%+v want=%+v", summary, envelope.User.Usage)
	}
	// A second login for the same Discord identity bypasses a closed registration
	// gate, refreshes the profile, and replaces the previous session generation.
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='0' WHERE key='registration_open'`); err != nil {
		t.Fatal(err)
	}
	replacement := loginUser(t, f, "second", "")
	old := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{cookie}, nil)
	if old.Code != http.StatusUnauthorized {
		t.Fatalf("replaced session status=%d body=%s", old.Code, old.Body.String())
	}
	current := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{replacement}, nil)
	if current.Code != http.StatusOK {
		t.Fatalf("replacement status=%d body=%s", current.Code, current.Body.String())
	}
}

func TestRegistrationGateAndRollback(t *testing.T) {
	t.Run("closed redirects", func(t *testing.T) {
		f := newRuntimeFixture(t, nil)
		if _, err := f.store.DB().Exec(`UPDATE site_config SET value='0' WHERE key='registration_open'`); err != nil {
			t.Fatal(err)
		}
		memberLookupCalled := false
		f.provider.mu.Lock()
		f.provider.logins["closed"] = DiscordLogin{
			Identity: DiscordIdentity{ID: "new-discord", Username: "Alice"},
			GuildMember: func(context.Context, string) (GuildMember, error) {
				memberLookupCalled = true
				return GuildMember{}, ErrProviderUnavailable
			},
		}
		f.provider.mu.Unlock()
		start := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, nil)
		stateCookie := responseCookie(t, start, OAuthStateCookieName)
		u, _ := url.Parse(start.Header().Get("Location"))
		callback := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=closed&state="+url.QueryEscape(u.Query().Get("state")), "", []*http.Cookie{stateCookie}, nil)
		if callback.Code != http.StatusFound || callback.Header().Get("Location") != "/registration-closed" {
			t.Fatalf("status=%d location=%q body=%s", callback.Code, callback.Header().Get("Location"), callback.Body.String())
		}
		if memberLookupCalled {
			t.Fatal("closed registration performed a membership lookup")
		}
	})
	t.Run("role mismatch", func(t *testing.T) {
		f := newRuntimeFixture(t, nil)
		f.provider.logins["wrong-role"] = DiscordLogin{Identity: DiscordIdentity{ID: "new-discord", Username: "Alice"}, GuildMember: func(context.Context, string) (GuildMember, error) { return GuildMember{Roles: []string{"other"}}, nil }}
		start := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, nil)
		stateCookie := responseCookie(t, start, OAuthStateCookieName)
		u, _ := url.Parse(start.Header().Get("Location"))
		callback := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=wrong-role&state="+url.QueryEscape(u.Query().Get("state")), "", []*http.Cookie{stateCookie}, nil)
		if callback.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", callback.Code, callback.Body.String())
		}
	})
	t.Run("registration hook failure rolls back", func(t *testing.T) {
		f := newRuntimeFixture(t, nil)
		if _, err := f.store.DB().Exec(`CREATE TRIGGER fail_caller_key BEFORE INSERT ON caller_keys BEGIN SELECT RAISE(ABORT,'fail'); END`); err != nil {
			t.Fatal(err)
		}
		addLogin(f.provider, "fail", "rollback-discord")
		start := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, nil)
		stateCookie := responseCookie(t, start, OAuthStateCookieName)
		u, _ := url.Parse(start.Header().Get("Location"))
		callback := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=fail&state="+url.QueryEscape(u.Query().Get("state")), "", []*http.Cookie{stateCookie}, nil)
		if callback.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", callback.Code, callback.Body.String())
		}
		var count int
		if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE discord_id='rollback-discord'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("user count=%d err=%v", count, err)
		}
		if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_accounts WHERE kind='user'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("wallet count=%d err=%v", count, err)
		}
	})
}

func TestSessionIdleAbsoluteBanDeleteAndLogout(t *testing.T) {
	f := newRuntimeFixture(t, func(c *RuntimeConfig) { c.SessionIdleTTL = 2 * time.Minute; c.SessionAbsoluteTTL = 3 * time.Minute })
	cookie := loginUser(t, f, "session", "")
	f.clock.Add(90 * time.Second)
	touch := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{cookie}, nil)
	if touch.Code != http.StatusOK {
		t.Fatalf("touch=%d %s", touch.Code, touch.Body.String())
	}
	renewed := responseCookie(t, touch, UserSessionCookieName)
	if renewed.Value != cookie.Value || renewed.Expires.Unix() != authTestNow+int64((3*time.Minute)/time.Second) {
		t.Fatalf("renewed cookie=%+v", renewed)
	}
	f.clock.Add(91 * time.Second)
	expired := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{cookie}, nil)
	if expired.Code != http.StatusUnauthorized {
		t.Fatalf("absolute status=%d body=%s", expired.Code, expired.Body.String())
	}
	cookie = loginUser(t, f, "session", "")
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1 WHERE discord_id='discord-1'`); err != nil {
		t.Fatal(err)
	}
	banned := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{cookie}, nil)
	if banned.Code != http.StatusForbidden {
		t.Fatalf("ban status=%d body=%s", banned.Code, banned.Body.String())
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=0 WHERE discord_id='discord-1'`); err != nil {
		t.Fatal(err)
	}
	logout := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/auth/logout", "", []*http.Cookie{cookie}, nil)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", logout.Code, logout.Body.String())
	}
	if _, err := f.store.DB().Exec(`DELETE FROM users WHERE discord_id='discord-1'`); err != nil {
		t.Fatal(err)
	}
	deleted := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{cookie}, nil)
	if deleted.Code != http.StatusUnauthorized {
		t.Fatalf("deleted status=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestUserEnvelopeWideScalarsAmountsNullsAndLevelFive(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "dto", "")
	max, _ := db.ParseU128Decimal("340282366920938463463374607431768211455")
	donation, _ := db.ParseU128Decimal("1234")
	negative, _ := db.ParseU128Decimal("1500")
	if _, err := f.store.DB().Exec(`UPDATE users SET donation_credit_mag=?,level=5,lang='en',endpoint_limit=0,rpm_limit=NULL,concurrency_limit=17,total_requests=?,total_uncached_input_tokens=?,total_cache_write_input_tokens=?,total_cache_read_input_tokens=?,total_output_tokens=?,total_unknown_usage_requests=?,charity_suspended_until=?,updated_at=? WHERE id=1`, db.EncodeU128(donation), db.EncodeU128(max), db.EncodeU128(donation), db.EncodeU128(donation), db.EncodeU128(donation), db.EncodeU128(donation), db.EncodeU128(donation), authTestNow+600, authTestNow+1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE credit_accounts SET balance_sign=-1,balance_mag=? WHERE user_id=1`, db.EncodeU128(negative)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='Steward' WHERE key='level_display_name_5'`); err != nil {
		t.Fatal(err)
	}
	rec := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/me", "", []*http.Cookie{cookie}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got UserEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.User.Balance != "-1.5" || got.User.DonationCredit != "1.234" || got.User.EffectiveLevel != 5 || got.User.LevelDisplayName != "Steward" || got.User.EndpointLimit == nil || *got.User.EndpointLimit != "0" || got.User.RPMLimit != nil || got.User.ConcurrencyLimit == nil || *got.User.ConcurrencyLimit != "17" || got.User.Usage.TotalRequests != max.Decimal() || got.User.Usage.TotalPromptTokens != "3702" {
		t.Fatalf("dto=%+v", got.User)
	}
	expectedBytes, err := os.ReadFile("testdata/user_envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected UserEnvelope
	if err := json.Unmarshal(expectedBytes, &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("fixture mismatch\n got=%s\nwant=%s", rec.Body.String(), string(expectedBytes))
	}
	usageBytes, err := os.ReadFile("testdata/usage_summary.json")
	if err != nil {
		t.Fatal(err)
	}
	var expectedUsage UsageSummary
	if err := json.Unmarshal(usageBytes, &expectedUsage); err != nil {
		t.Fatal(err)
	}
	if got.User.Usage != expectedUsage {
		t.Fatalf("usage fixture got=%+v want=%+v", got.User.Usage, expectedUsage)
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	user := raw["user"].(map[string]any)
	for _, forbidden := range []string{"manual_level", "default_locale", "credits", "blocked_reason"} {
		if _, ok := user[forbidden]; ok {
			t.Fatalf("forbidden field %q in %s", forbidden, rec.Body.String())
		}
	}
}

func TestUserEnvelopeLevelNamesAndAutomaticHighWater(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "levels", "")
	for level, name := range map[int]string{1: "Level One", 2: "Level Two", 3: "Level Three", 4: "Level Four", 5: "Level Five"} {
		if _, err := f.store.DB().Exec(`UPDATE site_config SET value=? WHERE key=?`, name, "level_display_name_"+strconv.Itoa(level)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.DB().Exec(`UPDATE users SET level=? WHERE id=1`, level); err != nil {
			t.Fatal(err)
		}
		rec := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/me", "", []*http.Cookie{cookie}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("level %d status=%d body=%s", level, rec.Code, rec.Body.String())
		}
		var envelope UserEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.User.EffectiveLevel != level || envelope.User.LevelDisplayName != name {
			t.Fatalf("level %d projection=%+v", level, envelope.User)
		}
	}

	for level, threshold := range map[int]string{2: "1000", 3: "2000", 4: "3000"} {
		if _, err := f.store.DB().Exec(`UPDATE site_config SET value=? WHERE key=?`, threshold, "level_threshold_"+strconv.Itoa(level)+"_milli"); err != nil {
			t.Fatal(err)
		}
	}
	donation, err := db.ParseU128Decimal("3000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET level=NULL,auto_level=1,donation_credit_mag=? WHERE id=1`, db.EncodeU128(donation)); err != nil {
		t.Fatal(err)
	}
	promoted := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/me", "", []*http.Cookie{cookie}, nil)
	var promotedEnvelope UserEnvelope
	if promoted.Code != http.StatusOK || json.Unmarshal(promoted.Body.Bytes(), &promotedEnvelope) != nil || promotedEnvelope.User.EffectiveLevel != 4 || promotedEnvelope.User.LevelDisplayName != "Level Four" {
		t.Fatalf("promoted=%d body=%s", promoted.Code, promoted.Body.String())
	}
	zero := db.EncodeU128(db.U128{})
	if _, err := f.store.DB().Exec(`UPDATE users SET donation_credit_mag=? WHERE id=1`, zero); err != nil {
		t.Fatal(err)
	}
	highWater := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/me", "", []*http.Cookie{cookie}, nil)
	var highWaterEnvelope UserEnvelope
	if highWater.Code != http.StatusOK || json.Unmarshal(highWater.Body.Bytes(), &highWaterEnvelope) != nil || highWaterEnvelope.User.EffectiveLevel != 4 {
		t.Fatalf("high-water=%d body=%s", highWater.Code, highWater.Body.String())
	}
}
