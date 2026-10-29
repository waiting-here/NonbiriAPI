package claim

import (
	"context"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"testing"
)

func TestReaderPoolRecoveryBacklogHint(t *testing.T) {
	f := newClaimFixture(t)
	dbtest.WithoutWriter(t, f.store.DB(), f.store.ReadDB(), func(ctx context.Context) {
		pending, err := f.service.hasClaimRecoveryWork(ctx)
		if err != nil || pending {
			t.Fatalf("empty recovery backlog=%v: %v", pending, err)
		}
	})
}
