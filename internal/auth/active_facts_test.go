package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/host"
)

func TestActiveFactsFreshLoginExcludesSessionPollingAndDeniedLogin(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "active-first", "")
	assertState := func(wantSeq, wantAt int64) {
		t.Helper()
		var seq, at int64
		if err := f.store.DB().QueryRow(`SELECT activity_seq,last_active_at FROM user_activity_state WHERE user_id=1`).Scan(&seq, &at); err != nil {
			t.Fatal(err)
		}
		if seq != wantSeq || at != wantAt {
			t.Fatalf("activity=(%d,%d), want (%d,%d)", seq, at, wantSeq, wantAt)
		}
	}
	assertState(1, authTestNow)
	f.clock.Add(time.Minute)
	got := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/session", "", []*http.Cookie{cookie}, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("session: %d %s", got.Code, got.Body.String())
	}
	assertState(1, authTestNow)
	loginUser(t, f, "active-second", "")
	assertState(2, authTestNow+60)
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(time.Minute)
	if _, _, err := f.runtime.refreshExistingUser(context.Background(), 1, DiscordIdentity{ID: "discord-1", Username: "Denied"}, nil); err == nil {
		t.Fatal("banned login accepted")
	}
	assertState(2, authTestNow+60)
}
