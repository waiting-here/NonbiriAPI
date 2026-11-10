package logapi

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestLogReadPhasesSeparateConnectionWaitAndQueryWork(t *testing.T) {
	f, reader := newSeparateLogReaders(t, logReadAuthFunc(func(ctx context.Context, tx *sql.Tx, role string, id int64) error {
		return authorizePageAccount(ctx, tx, role, id, 1_000_000)
	}))
	performance := observability.NewPerformance()
	ctx := performance.Context(t.Context())
	held, err := reader.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	pattern := "CHARITY-LOGICAL"
	status, from, to := 200, int64(0), int64(1_000_000)
	filter := ListFilter{CharityModel: &pattern, Status: &status, From: &from, To: &to, Page: &pagination.Request{Page: 1, Size: 10}}
	if _, err := f.repo.ListAdmin(waitCtx, filter); err == nil {
		t.Fatal("blocked reader unexpectedly succeeded")
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	phases := performance.Drain()
	if len(phases) != 1 || phases[0].Route != "log_read.admin.acquire" || phases[0].Canceled != 1 || phases[0].Active != 0 {
		t.Fatalf("connection wait was attributed to query work: %+v", phases)
	}
	page, err := f.repo.ListSteward(ctx, 999, filter, allowLogStewardRead{})
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("steward read: %+v %v", page, err)
	}
	phases = performance.Drain()
	want := map[string]bool{"acquire": false, "authorize": false, "models": false, "count": false, "rows": false}
	for _, phase := range phases {
		for name := range want {
			if phase.Route == "log_read.steward."+name {
				want[name] = true
				if phase.Active != 0 || phase.Completed != 1 || phase.Failures != 0 || phase.Canceled != 0 {
					t.Fatalf("unfinished read phase: %+v", phase)
				}
			}
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("missing phase %s", name)
		}
	}
	f.mustExec("UPDATE request_logs SET model=model||? WHERE logical_request_id=?", strings.Repeat("x", 513), f.charityID)
	if _, err := f.repo.ListSteward(ctx, 999, filter, allowLogStewardRead{}); err == nil {
		t.Fatal("invalid row projection unexpectedly succeeded")
	}
	scanFailure := false
	for _, phase := range performance.Drain() {
		if phase.Active != 0 {
			t.Fatalf("phase leaked after row consumption: %+v", phase)
		}
		if phase.Route == "log_read.steward.rows" && phase.Failures == 1 {
			scanFailure = true
		}
	}
	if !scanFailure || reader.Stats().InUse != 0 {
		t.Fatal("row consumption error lost timing or retained its reader")
	}
	f.mustExec("UPDATE request_logs SET model='CHARITY-LOGICAL-MODEL-SENTINEL' WHERE logical_request_id=?", f.charityID)
	f.mustExec("DROP TABLE credit_operations")
	if _, err := f.repo.ListSteward(ctx, 999, filter, allowLogStewardRead{}); err == nil {
		t.Fatal("missing projection table unexpectedly succeeded")
	}
	found := false
	for _, phase := range performance.Drain() {
		if phase.Active != 0 {
			t.Fatalf("phase leaked after query error: %+v", phase)
		}
		if phase.Route == "log_read.steward.rows" && phase.Failures == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("row query error missing from rows phase")
	}
}
