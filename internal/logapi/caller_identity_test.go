package logapi

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStewardCallerIdentityUsesCurrentAccountOnly(t *testing.T) {
	f := newLogFixture(t)
	ctx := context.Background()
	f.mustExec(`INSERT INTO users(id,discord_id,username,guild_nick) VALUES(?,?,'synced display name','server nickname')`, logUserOne, "123456789012345678901234567890")
	assertIdentity := func(nickname, id *string) {
		t.Helper()
		detail, err := f.repo.GetSteward(ctx, 999, f.charityID, AttemptFilter{}, allowLogStewardRead{})
		if err != nil {
			t.Fatal(err)
		}
		page, err := f.repo.ListSteward(ctx, 999, ListFilter{}, allowLogStewardRead{})
		if err != nil {
			t.Fatal(err)
		}
		identity := detail.Request.CallerIdentity
		if identity == nil {
			t.Fatal("existing caller lost identity projection")
		}
		if !equalIdentityText(identity.DiscordNickname, nickname) || !equalIdentityText(identity.DiscordID, id) {
			t.Fatalf("current identity=%+v", identity)
		}
		found := false
		for _, row := range page.Data {
			if row.ID == f.charityID {
				found = true
				if row.CallerIdentity == nil || !equalIdentityText(row.CallerIdentity.DiscordNickname, nickname) || !equalIdentityText(row.CallerIdentity.DiscordID, id) {
					t.Fatalf("list/detail identity differ: %+v", row)
				}
			} else if row.RouteKind != RouteCharityChat && row.CallerIdentity != nil {
				t.Fatal("self/discovery identity leaked")
			}
		}
		if !found {
			t.Fatal("charity request absent from list")
		}
		requireNoJSONKeys(t, detail, "model", "guild_avatar_url", "avatar", "endpoint_note", "key_note")
		noLogSentinel(t, detail, "RAW-DISCORD", "RAW-PRIVATE-NOTE")
	}
	nickname, id := "server nickname", "123456789012345678901234567890"
	assertIdentity(&nickname, &id)
	f.mustExec(`UPDATE users SET guild_nick='' WHERE id=?`, logUserOne)
	nickname = "synced display name"
	assertIdentity(&nickname, &id)
	f.mustExec(`UPDATE users SET username='latest name' WHERE id=?`, logUserOne)
	nickname = "latest name"
	assertIdentity(&nickname, &id)
	f.mustExec(`UPDATE users SET username='',discord_id=NULL WHERE id=?`, logUserOne)
	assertIdentity(nil, nil)
	f.mustExec(`UPDATE request_logs SET user_id=NULL WHERE logical_request_id=?`, f.charityID)
	f.mustExec(`UPDATE users SET username='must not resurrect',discord_id='changed identity' WHERE id=?`, logUserOne)
	detail, err := f.repo.GetSteward(ctx, 999, f.charityID, AttemptFilter{}, allowLogStewardRead{})
	if err != nil || detail.Request.CallerIdentity != nil {
		t.Fatalf("deidentified log recovered identity: %+v %v", detail, err)
	}
	f.mustExec(`UPDATE request_logs SET user_id=? WHERE logical_request_id=?`, logUserOne, f.charityID)
	f.mustExec(`DELETE FROM users WHERE id=?`, logUserOne)
	detail, err = f.repo.GetSteward(ctx, 999, f.charityID, AttemptFilter{}, allowLogStewardRead{})
	if err != nil || detail.Request.CallerIdentity != nil {
		t.Fatalf("deleted account identity: %+v %v", detail, err)
	}
	f.clock.Advance(31 * 24 * time.Hour)
	if _, err = f.repo.GetSteward(ctx, 999, f.charityID, AttemptFilter{}, allowLogStewardRead{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("identity widened retention: %v", err)
	}
}

func equalIdentityText(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
