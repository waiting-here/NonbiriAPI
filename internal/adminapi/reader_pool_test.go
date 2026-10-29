package adminapi

import (
	"context"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"testing"
)

func TestReaderPoolSiteConfigurationAndAuthority(t *testing.T) {
	store := openConcurrentSiteConfigStore(t)
	store.DB().SetMaxOpenConns(1)
	authority := &siteConfigTestFinalAuthorizer{}
	repository := newSiteConfigTestRepository(t, store, authority)
	dbtest.WithoutWriter(t, store.DB(), store.ReadDB(), func(ctx context.Context) {
		if _, err := repository.ReadAdminPublicConfig(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.ReadSiteConfig(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.ReadSiteConfigCatalog(ctx, 1); err != nil {
			t.Fatal(err)
		}
		authority.setError(authz.ErrForbidden)
		if _, err := repository.ReadSiteConfig(ctx, 1); !errors.Is(err, ErrSiteConfigForbidden) {
			t.Fatalf("current authority: %v", err)
		}
	})
}
