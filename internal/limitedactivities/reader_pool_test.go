package limitedactivities

import (
	"context"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"testing"
)

func TestReaderPoolCatalogAndCurrentSession(t *testing.T) {
	f := newFixture(t)
	dbtest.WithoutWriter(t, f.database, f.service.readDB, func(ctx context.Context) {
		user := context.WithValue(ctx, actorContext{}, f.actors[f.user])
		if _, err := f.service.List(user, f.user); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.Detail(user, f.user, PictureBook); err != nil && !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		if _, err := f.service.Wallet(user, f.user); err != nil {
			t.Fatal(err)
		}
		admin := context.WithValue(ctx, actorContext{}, f.actors[f.admin])
		if _, err := f.service.AdminConfig(admin, f.admin, PictureBook); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := f.database.Exec("DELETE FROM sessions WHERE user_id=?", f.user); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Wallet(f.ctx(f.user), f.user); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("revoked session: %v", err)
	}
}
