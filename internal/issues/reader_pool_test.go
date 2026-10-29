package issues

import (
	"context"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"testing"
)

func TestReaderPoolIssueListAndEmptyRebuildCandidates(t *testing.T) {
	e := newIssueTestEnvironment(t)
	user := e.seedUser(t, "reader-user")
	dbtest.WithoutWriter(t, e.store.DB(), e.store.ReadDB(), func(ctx context.Context) {
		if _, err := e.service.List(ctx, user, ListQuery{State: "current", Limit: 20}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.service.RebuildIncomplete(ctx, 1); err != nil {
			t.Fatal(err)
		}
	})
}
