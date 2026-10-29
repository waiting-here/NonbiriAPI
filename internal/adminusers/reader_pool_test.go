package adminusers

import (
	"context"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"testing"
)

func TestReaderPoolLegacyManagementAndCurrentAuthority(t *testing.T) {
	f := newAdminUsersFixture(t)
	user := f.seedUser("reader-user", false)
	dbtest.WithoutWriter(t, f.store.DB(), f.store.ReadDB(), func(ctx context.Context) {
		if _, err := f.service.ListUsers(ctx, f.adminID, UserListQuery{}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.GetUser(ctx, f.adminID, user); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.SiteUsage(ctx, f.adminID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.UserUsage(ctx, f.adminID, PageQuery{}); err != nil {
			t.Fatal(err)
		}
		f.auth.err = authz.ErrForbidden
		if _, err := f.service.GetUser(ctx, f.adminID, user); !errors.Is(err, ErrForbidden) {
			t.Fatalf("current authority: %v", err)
		}
	})
}
