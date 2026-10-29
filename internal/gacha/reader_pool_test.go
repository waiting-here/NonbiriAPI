package gacha

import (
	"context"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"testing"
)

func TestReaderPoolCatalogHistoryStatisticsAndAppearance(t *testing.T) {
	f := newFixture(t)
	appearance := newAppearanceService(t, f, nil)
	dbtest.WithoutWriter(t, f.db, f.s.config.ReadDatabase, func(ctx context.Context) {
		if _, err := f.s.State(ctx, f.user); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.Entry(ctx, f.user); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.History(ctx, f.user, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.AdminConfig(ctx, f.admin); err != nil {
			t.Fatal(err)
		}
		tx, _, err := f.s.statisticsTx(ctx, f.admin)
		if err != nil {
			t.Fatal(err)
		}
		tx.Rollback()
		if _, err := appearance.State(ctx, f.user); err != nil {
			t.Fatal(err)
		}
	})
}
