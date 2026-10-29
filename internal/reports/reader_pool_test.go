package reports

import (
	"context"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"testing"
)

func TestReaderPoolCaseListAndBadge(t *testing.T) {
	e := newReportTestEnvironment(t)
	actor := e.seedActor(t, true, 1)
	dbtest.WithoutWriter(t, e.store.DB(), e.store.ReadDB(), func(ctx context.Context) {
		if _, err := e.repository.ListCases(ctx, actor, "", "", 20); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repository.Badge(ctx, actor); err != nil {
			t.Fatal(err)
		}
	})
}
