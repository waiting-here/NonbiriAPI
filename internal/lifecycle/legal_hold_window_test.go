package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestLegalHoldFilteredNumberedAndCursorWindows(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 200)
	adminID := seedLifecycleUser(t, fixture.store.DB(), "hold-window-admin", true, 100)
	coordinator := mustNewLifecycleCoordinator(t, fixture.config)
	tx, err := fixture.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	states := []string{"active", "released", "expired"}
	for i := 1; i <= 61; i++ {
		expires := int64(1000)
		if states[i%3] == "expired" {
			expires = 150
		}
		seedPageHold(t, tx, adminID, i, states[i%3], 100, expires, 150)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		want := []string{}
		for i := 61; i >= 1; i-- {
			if states[i%3] == state {
				want = append(want, fmt.Sprintf("lgh_%021dA", i))
			}
		}
		cursor := ""
		for pageNumber, offset := int64(1), 0; offset < len(want); pageNumber, offset = pageNumber+1, offset+10 {
			request := pagination.Request{Page: pageNumber, Size: 10}
			numbered, err := coordinator.ListLegalHolds(context.Background(), LegalHoldListFilter{
				AdminID: adminID, DecisionNow: 200, State: state, Kind: HeldMaintenanceEvent, Page: &request,
			})
			if err != nil {
				t.Fatal(err)
			}
			requireHoldPage(t, numbered, request, int64(len(want)))
			legacy, err := coordinator.ListLegalHolds(context.Background(), LegalHoldListFilter{
				AdminID: adminID, DecisionNow: 200, State: state, Kind: HeldMaintenanceEvent, Limit: 10, Cursor: cursor,
			})
			if err != nil {
				t.Fatal(err)
			}
			end := min(offset+10, len(want))
			ids := []string{}
			for _, item := range numbered.Data {
				ids = append(ids, item.ID)
			}
			if !reflect.DeepEqual(ids, want[offset:end]) || !reflect.DeepEqual(numbered.Data, legacy.Data) {
				t.Fatalf("state=%s page=%d ids=%v want=%v cursor page=%+v numbered=%+v", state, pageNumber, ids, want[offset:end], legacy, numbered)
			}
			if end == len(want) {
				if legacy.NextCursor != nil {
					t.Fatal("last filtered window has a cursor")
				}
			} else {
				if legacy.NextCursor == nil {
					t.Fatal("filtered window lost its next cursor")
				}
				cursor = *legacy.NextCursor
			}
		}
	}
}

// Cancel after the driver's successful commit, so the test exercises an
// interrupted multi-batch request without timing a partially written batch.
type cancelAfterHoldBatch struct {
	once   sync.Once
	cancel context.CancelFunc
}

func (o *cancelAfterHoldBatch) StartConnectionSpan(_ context.Context, kind string) func(error) {
	if kind != "transaction" {
		return nil
	}
	return func(err error) {
		if err == nil {
			o.once.Do(o.cancel)
		}
	}
}

func TestLegalHoldLegacyBudgetPreservesCommittedExpiryAndReusesConnection(t *testing.T) {
	fixture := newLifecycleTestFixture(t, 200)
	adminID := seedLifecycleUser(t, fixture.store.DB(), "hold-budget-admin", true, 100)
	fixture.config.AdminAuth = &numberedHoldAuthorizer{testFinalAuth: fixture.auth, before: func(ctx context.Context, _ int) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Second {
			t.Fatal("legacy final read lacks its request budget")
		}
		return fixture.auth.AuthorizeAdmin(ctx, nil, adminID)
	}}
	coordinator := mustNewLifecycleCoordinator(t, fixture.config)
	tx, err := fixture.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	const count = WorkerBatchLimit*2 + 1
	for i := 1; i <= count; i++ {
		seedPageHold(t, tx, adminID, i, "active", 100, 150, 0)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.store.SetConnectionObserver(&cancelAfterHoldBatch{cancel: cancel})
	t.Cleanup(func() { fixture.store.SetConnectionObserver(nil) })
	filter := LegalHoldListFilter{AdminID: adminID, DecisionNow: 200, State: "expired", Limit: 100}
	_, err = coordinator.ListLegalHolds(ctx, filter)
	fixture.store.SetConnectionObserver(nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expiry request ignored cancellation after first commit: %v", err)
	}
	var expired int
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM legal_holds WHERE state='expired'`).Scan(&expired); err != nil || expired != WorkerBatchLimit {
		t.Fatalf("committed expiry batch lost: expired=%d error=%v", expired, err)
	}
	if fixture.auth.adminCalls != 0 {
		t.Fatal("cancelled maintenance changed final authorization order")
	}
	page, err := coordinator.ListLegalHolds(context.Background(), filter)
	if err != nil || len(page.Data) != 100 || page.NextCursor == nil || fixture.auth.adminCalls != 1 {
		t.Fatalf("legacy retry page=%+v error=%v authorization=%d", page, err, fixture.auth.adminCalls)
	}
	if err := fixture.store.DB().QueryRow(`SELECT count(*) FROM legal_hold_audits WHERE action='expire'`).Scan(&expired); err != nil || expired != count {
		t.Fatalf("expiry retry duplicated or omitted audits: count=%d error=%v", expired, err)
	}
}
