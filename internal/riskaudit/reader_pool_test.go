package riskaudit

import (
	"context"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"testing"
)

func TestReaderPoolIdleScanAndRetentionHints(t *testing.T) {
	f := newAuditFixture(t)
	dbtest.WithoutWriter(t, f.store.DB(), f.store.ReadDB(), func(ctx context.Context) {
		progress, err := f.repository.ProcessScanBatch(ctx)
		if err != nil || progress {
			t.Fatalf("empty scan queue=%v: %v", progress, err)
		}
		if _, err := f.repository.CleanupScans(ctx); err != nil {
			t.Fatal(err)
		}
	})
}
