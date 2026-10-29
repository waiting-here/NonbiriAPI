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

func TestApplicationQueriesUseReaderPool(t *testing.T) {
	f := newGameWireFixture(t)
	if _, err := f.store.DB().Exec("INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','0',0) ON CONFLICT(key) DO UPDATE SET value='0'"); err != nil {
		t.Fatal(err)
	}
	loadActor := func(kind authz.ActorKind, id int64) authz.Actor {
		t.Helper()
		actor := authz.Actor{Kind: kind, UserID: id}
		if err := f.store.ReadDB().QueryRow("SELECT token_hash,cred_gen FROM sessions WHERE user_id=?", id).Scan(&actor.SessionTokenHash, &actor.SessionGeneration); err != nil {
			t.Fatal(err)
		}
		return actor
	}
	user := loadActor(authz.ActorUserSession, f.userID)
	admin := loadActor(authz.ActorAdminSession, f.adminID)
	for _, test := range []struct {
		name  string
		actor authz.Actor
		read  func(context.Context) error
	}{
		{"user details", admin, func(ctx context.Context) error {
			_, err := f.app.adminUsers.GetUser(ctx, f.adminID, f.userID)
			return err
		}},
		{"site usage", admin, func(ctx context.Context) error { _, err := f.app.adminUsers.SiteUsage(ctx, f.adminID); return err }},
		{"image queue", user, func(ctx context.Context) error {
			_, err := f.app.activityRuntime.images.GetQueue(ctx, f.userID)
			return err
		}},
		{"image controls", admin, func(ctx context.Context) error {
			_, err := f.app.activityRuntime.images.Controls(ctx, f.adminID, 20, "")
			return err
		}},
		{"fish periods", user, func(ctx context.Context) error {
			_, err := f.app.activityRuntime.fish.ListPeriods(ctx, f.userID, 1)
			return err
		}},
		{"gacha state", user, func(ctx context.Context) error {
			_, err := f.app.activityRuntime.gacha.State(ctx, f.userID)
			return err
		}},
		{"reward history", user, func(ctx context.Context) error {
			_, err := f.app.activityRuntime.rewards.History(ctx, f.userID, "")
			return err
		}},
		{"appearance", user, func(ctx context.Context) error {
			_, err := f.app.activityRuntime.appearance.State(ctx, f.userID)
			return err
		}},
		{"activity list", user, func(ctx context.Context) error {
			_, err := f.app.activityRuntime.limited.List(ctx, f.userID)
			return err
		}},
		{"inactivity policy", admin, func(ctx context.Context) error {
			_, err := f.app.activityRuntime.inactivity.Get(ctx)
			return err
		}},
		{"lake periods", admin, func(ctx context.Context) error { _, err := f.app.activityRuntime.lake.Periods(ctx, 1, 20); return err }},
		{"game configuration", user, func(ctx context.Context) error { _, err := f.app.homeGames.ReadGamesConfig(ctx); return err }},
		{"game active counts", user, func(ctx context.Context) error { _, err := f.app.homeGames.ActiveCounts(ctx); return err }},
		{"checkin status", user, func(ctx context.Context) error { _, err := f.app.checkin.Status(ctx, f.userID); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			writer, err := f.store.DB().Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			// The real application keeps background workers running. Holding its
			// sole writer proves the query's root wiring independently of them.
			if err := test.read(auth.WithSessionActor(ctx, test.actor)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
