package stewardautomation

import (
	"context"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"testing"
)

func TestReaderPoolAutomationPreflight(t *testing.T) {
	f := newPersonalFixture(t)
	dbtest.WithoutWriter(t, f.store.DB(), f.store.ReadDB(), func(ctx context.Context) {
		// Preserve the fixture's current caller identity while bounding the read.
		bounded, cancel := context.WithCancel(f.ctx)
		stop := context.AfterFunc(ctx, cancel)
		defer stop()
		defer cancel()
		if err := f.service.authorizeAutomation(bounded, f.userID, true); err != nil {
			t.Fatal(err)
		}
	})
}
