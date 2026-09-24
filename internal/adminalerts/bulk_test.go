package adminalerts

import (
	"context"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"testing"
)

func TestKindFilterCursorAndAtomicBulkResolution(t *testing.T) {
	f := newAlertTestEnvironment(t)
	ctx := context.Background()
	first := f.seedAlert(t, "donation_failure_disabled", "one", "", nil, alertTestNow, false)
	second := f.seedAlert(t, "donation_failure_disabled", "two", "", nil, alertTestNow, false)
	other := f.seedAlert(t, "forward_error", "other", "", nil, alertTestNow, false)
	page, err := f.repository.List(ctx, f.adminID, ListQuery{Kind: KindDonationFailureDisabled, Limit: 1})
	if err != nil || len(page.Data) != 1 || page.NextCursor == nil {
		t.Fatal(page, err)
	}
	if _, err = f.repository.List(ctx, f.adminID, ListQuery{Kind: KindForwardError, Limit: 1, Cursor: *page.NextCursor}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cross-kind cursor=%v", err)
	}
	if _, err = f.repository.ResolveMany(ctx, f.adminID, []int64{first, 999999}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	var count int
	if err = f.store.DB().QueryRow("SELECT count(*) FROM admin_alerts WHERE resolved=1").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial bulk write", count, err)
	}
	result, err := f.repository.ResolveMany(ctx, f.adminID, []int64{first, second})
	if err != nil || result.ResolvedCount != 2 {
		t.Fatal(result, err)
	}
	f.clock.Add(20)
	if _, err = f.repository.ResolveMany(ctx, f.adminID, []int64{first, second}); err != nil {
		t.Fatal(err)
	}
	var at int64
	if err = f.store.DB().QueryRow("SELECT resolved_at FROM admin_alerts WHERE id=?", first).Scan(&at); err != nil || at != alertTestNow {
		t.Fatal("replay changed resolution time", at, err)
	}
	f.authorizer.forced = authz.ErrForbidden
	if _, err = f.repository.ResolveMany(ctx, f.adminID, []int64{other}); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = f.repository.ResolveMany(ctx, f.adminID, []int64{other, other}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}
