package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestTimeContextReadsWithBusyWriterAndRechecksAuthority(t *testing.T) {
	f := newGameWireFixture(t)
	if _, err := f.store.DB().Exec(`INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','480',0) ON CONFLICT(key) DO UPDATE SET value='480'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("UPDATE users SET level=6 WHERE id=?", f.userID); err != nil {
		t.Fatal(err)
	}
	admin := authz.Actor{Kind: authz.ActorAdminSession, UserID: f.adminID}
	steward := authz.Actor{Kind: authz.ActorUserSession, UserID: f.userID}
	for _, actor := range []*authz.Actor{&admin, &steward} {
		if err := f.store.ReadDB().QueryRow("SELECT token_hash,cred_gen FROM sessions WHERE user_id=?", actor.UserID).Scan(&actor.SessionTokenHash, &actor.SessionGeneration); err != nil {
			t.Fatal(err)
		}
	}
	resolver := &timeContextResolver{store: f.store, authority: &roleFinalTxAuthorizer{authorizer: f.app.authorizer}}
	read := func(revoked bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		writer, err := f.store.DB().Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		for _, actor := range []authz.Actor{admin, steward} {
			call := resolver.StewardTimeContext
			if actor.Kind == authz.ActorAdminSession {
				call = resolver.AdminTimeContext
			}
			value, err := call(auth.WithSessionActor(ctx, actor), actor.UserID)
			if revoked {
				if !errors.Is(err, authz.ErrUnauthorized) && !errors.Is(err, authz.ErrForbidden) {
					t.Fatalf("stale authority returned %+v, %v", value, err)
				}
			} else if err != nil || !value.Configured || value.OffsetMinutes != 480 {
				t.Fatalf("time context waited for writer or lost configuration: %+v, %v", value, err)
			}
		}
	}
	read(false)
	if _, err := f.store.DB().Exec("DELETE FROM sessions WHERE token_hash=?", admin.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("UPDATE users SET level=4 WHERE id=?", steward.UserID); err != nil {
		t.Fatal(err)
	}
	read(true)
}
