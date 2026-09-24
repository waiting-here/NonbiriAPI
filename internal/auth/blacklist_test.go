package auth

import (
	"context"
	"errors"
	"testing"
)

func TestBlacklistedIdentityCannotRegisterAndRemovalRestoresEligibility(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	const id = "123456789012345678"
	if _, err := f.store.DB().Exec("INSERT INTO discord_blacklist VALUES(?,?,?)", id, "blocked", authTestNow); err != nil {
		t.Fatal(err)
	}
	identity := DiscordIdentity{ID: id, Username: "blocked-fixture"}
	member := GuildMember{Nick: "fixture", Roles: []string{"role-1"}}
	user, token, _, err := f.runtime.registerUser(context.Background(), identity, member, "guild-1", "role-1")
	if !errors.Is(err, errSessionForbidden) || user != 0 || token != "" {
		t.Fatalf("blocked registration=%d %v", user, err)
	}
	var count int
	if err = f.store.DB().QueryRow("SELECT count(*) FROM users WHERE discord_id=?", id).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err = f.store.DB().Exec("DELETE FROM discord_blacklist WHERE discord_id=?", id); err != nil {
		t.Fatal(err)
	}
	user, token, _, err = f.runtime.registerUser(context.Background(), identity, member, "guild-1", "role-1")
	if err != nil || user <= 0 || token == "" {
		t.Fatalf("restored registration=%d %v", user, err)
	}
}
