package blackjack_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLiveIdentityShowsOnlyCurrentSeatsAndDisappearsOnDelete(t *testing.T) {
	f := newFixture(t, 3)
	f.exec(`UPDATE users SET guild_nick='Visible seated player',guild_avatar_url='https://cdn.discordapp.com/avatars/123/avatar.png',game_profile_public=0 WHERE id=?`, f.users[0].UserID)
	f.exec(`UPDATE users SET guild_nick='Second seated player',guild_avatar_url='https://evil.example/avatar.png',game_profile_public=0 WHERE id=?`, f.users[1].UserID)
	f.join(0)
	f.join(1)
	spectator := f.read(2)
	if spectator.YourSeat != nil || spectator.Table == nil || len(spectator.Table.RealtimeIdentities) != 2 {
		t.Fatal("spectator projection", spectator)
	}
	first, second := spectator.Table.RealtimeIdentities[0], spectator.Table.RealtimeIdentities[1]
	if first.Seat != 0 || first.DisplayName != "Visible seated player" || first.AvatarURL == nil || second.Seat != 1 || second.DisplayName != "Second seated player" || second.AvatarURL != nil {
		t.Fatal("seat order, privacy preference or avatar host", first, second)
	}
	encoded, err := json.Marshal(spectator.Table.Fact)
	if err != nil || strings.Contains(string(encoded), "Visible seated player") || strings.Contains(string(encoded), "avatar_url") {
		t.Fatal("live identity entered historical fact", string(encoded), err)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	finalizer, err := f.s.PrepareDeleteTx(f.ctx, tx, f.users[0].UserID, f.clock.Load())
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if !finalizer.Commit() {
		t.Fatal("delete publication finalizer did not commit")
	}
	spectator = f.read(2)
	if spectator.Table == nil || len(spectator.Table.RealtimeIdentities) != 1 || spectator.Table.RealtimeIdentities[0].DisplayName != "Second seated player" {
		t.Fatal("retired identity remained visible", spectator)
	}
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	exported, _, err := f.s.Module().ExportTx(f.ctx, tx, f.users[1].UserID, f.clock.Load(), 10)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(exported)
	if err != nil || strings.Contains(string(encoded), "Second seated player") || strings.Contains(string(encoded), "realtime_identities") {
		t.Fatal("live identity entered personal export", string(encoded), err)
	}
}
