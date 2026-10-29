package adminalerts

import (
	"context"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"testing"
)

func TestReaderPoolAlertsAndDetail(t *testing.T) {
	e := newAlertTestEnvironment(t)
	id := e.seedAlert(t, string(KindFetchFailed), "read projection", "", nil, alertTestNow-1, false)
	dbtest.WithoutWriter(t, e.store.DB(), e.store.ReadDB(), func(ctx context.Context) {
		page, err := e.repository.List(ctx, e.adminID, ListQuery{Limit: 20})
		if err != nil || len(page.Data) != 1 {
			t.Fatalf("list: %+v %v", page, err)
		}
		if _, err := e.repository.GetDetail(ctx, e.adminID, id); err != nil {
			t.Fatal(err)
		}
	})
}
