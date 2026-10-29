package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type releasedReaderResponse struct {
	*httptest.ResponseRecorder
	check func()
}

func (w releasedReaderResponse) WriteHeader(status int) {
	w.check()
	w.ResponseRecorder.WriteHeader(status)
}

func (w releasedReaderResponse) Write(body []byte) (int, error) {
	w.check()
	return w.ResponseRecorder.Write(body)
}

func TestAuthPureReadsAvoidOccupiedWriter(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	observer := &mfaChangeObserver{}
	if err := f.runtime.AttachUserSessionInvalidationObserver(observer); err != nil {
		t.Fatal(err)
	}
	cookie := loginUser(t, f, "config-reader", "")
	userID, actor := userSessionIdentity(t, f, cookie)
	const deniedDiscord = "123456789012345678"
	if _, err := f.store.DB().Exec(`INSERT INTO discord_blacklist(discord_id,reason,created_at) VALUES(?,'restricted',?)`, deniedDiscord, authTestNow); err != nil {
		t.Fatal(err)
	}
	denial, err := f.runtime.issueDenial(t.Context(), deniedDiscord)
	if err != nil {
		t.Fatal(err)
	}
	held, err := f.store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before := f.store.DB().Stats().WaitCount
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if values, err := f.runtime.registrationConfig(ctx); err != nil || values["discord_guild_id"] != "guild-1" || values["discord_role_id"] != "role-1" {
		t.Fatal(values, err)
	}
	if open, err := f.runtime.registrationOpen(ctx); err != nil || !open {
		t.Fatal(open, err)
	}
	if guild, err := f.runtime.registrationGuild(ctx); err != nil || guild != "guild-1" {
		t.Fatal(guild, err)
	}
	if id, exists, err := f.runtime.findDiscordUser(ctx, "discord-1"); err != nil || !exists || id != userID {
		t.Fatal(id, exists, err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://user.example/api/auth/discord/callback", nil).WithContext(ctx)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	f.runtime.completeUserElevation(rec, req, DiscordLogin{Identity: DiscordIdentity{ID: "discord-1"}}, StateClaims{Binding: actor.SessionTokenHash}, "/")
	if rec.Code != http.StatusFound || responseCookie(t, rec, ElevatedCookieName).Value == "" {
		t.Fatalf("elevation status=%d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "https://user.example/api/auth/access-denied-reasons", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: DenialCookieName, Value: denial})
	rec = httptest.NewRecorder()
	checkReleased := func() {
		if f.store.ReadDB().Stats().InUse != 0 {
			t.Fatal("denial response retained reader")
		}
	}
	f.runtime.accessDeniedReasons(releasedReaderResponse{rec, checkReleased}, req)
	var page DenialPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || rec.Code != http.StatusOK || !page.Restricted || len(page.Items) != 1 || page.Items[0].Reason != "restricted" {
		t.Fatalf("denial status=%d page=%+v error=%v", rec.Code, page, err)
	}
	if _, err := held.ExecContext(ctx, `DELETE FROM auth_denial_grants`); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	f.runtime.accessDeniedReasons(releasedReaderResponse{rec, checkReleased}, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked denial grant retained access: %d", rec.Code)
	}
	if _, err := held.ExecContext(ctx, `UPDATE users SET level=1 WHERE id=?; UPDATE site_config SET value='1' WHERE key='mfa_required_level_1'`, userID); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.InvalidateUnverifiedMFASessions(ctx); err != nil || len(observer.changed) != 1 || observer.changed[0] != userID {
		t.Fatal(observer.changed, err)
	}
	if f.store.DB().Stats().WaitCount != before || f.store.ReadDB().Stats().InUse != 0 {
		t.Fatal("pure reads waited for writer or retained reader")
	}
	if _, err := held.ExecContext(ctx, `UPDATE site_config SET value='0' WHERE key='registration_open'; UPDATE site_config SET value='guild-2' WHERE key='discord_guild_id'`); err != nil {
		t.Fatal(err)
	}
	if open, err := f.runtime.registrationOpen(ctx); err != nil || open {
		t.Fatal("stale registration policy", open, err)
	}
	if values, err := f.runtime.registrationConfig(ctx); err != nil || values["discord_guild_id"] != "guild-2" {
		t.Fatal("stale guild configuration", values, err)
	}
}
