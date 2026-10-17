package economyaudit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (f *auditFixture) project(t *testing.T) {
	t.Helper()
	for {
		ready, err := f.service.Advance(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			return
		}
	}
}

func TestDefaultAuditReadMeasurements(t *testing.T) {
	f := newAuditFixture(t)
	f.fund(t, 2400, auditNow-2400)
	start := time.Now()
	f.project(t)
	t.Logf("projection: operations=2400 elapsed=%s", time.Since(start))
	filter := Filter{Asset: ledger.General, From: auditNow - 86400, To: auditNow + 1, Bucket: "hour"}
	before := f.database.Stats()
	start = time.Now()
	summary, err := f.service.Summary(f.ctx, f.admin, filter)
	if err != nil || summary.Flows.Issued != "2400000" || summary.Inventory == nil || summary.Inventory.Net != "2400000" {
		t.Fatal("default summary", summary, err)
	}
	t.Logf("summary: elapsed=%s", time.Since(start))
	start = time.Now()
	series, err := f.service.Series(f.ctx, f.admin, filter)
	if err != nil || len(series.Data) != 25 {
		t.Fatal("default series", series, err)
	}
	t.Logf("series: elapsed=%s writer_wait_count=%d writer_wait_duration=%s", time.Since(start), f.database.Stats().WaitCount-before.WaitCount, f.database.Stats().WaitDuration-before.WaitDuration)
}

func TestAuditReadersDoNotWaitForBusinessWriter(t *testing.T) {
	f := newAuditFixture(t)
	f.fund(t, 1, auditNow-1)
	f.project(t)
	tx, err := f.database.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(f.ctx, `UPDATE economy_audit_checkpoint SET updated_at=updated_at+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	// The service deadline bounds both reads; writer-pool waits are the invariant.
	ctx := f.ctx
	filter := Filter{Asset: ledger.General, From: auditNow - 86400, To: auditNow + 1, Bucket: "hour"}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	before, start := f.database.Stats(), time.Now()
	wg.Add(2)
	go func() {
		defer wg.Done()
		got, err := f.service.Summary(ctx, f.admin, filter)
		if err != nil {
			errs <- fmt.Errorf("summary: %w", err)
		} else if got.Inventory == nil || got.Inventory.Net != "1000" || got.Metadata.LedgerSeq != got.Metadata.ProjectedSeq {
			errs <- fmt.Errorf("inconsistent summary: %+v", got)
		}
	}()
	go func() {
		defer wg.Done()
		got, err := f.service.Series(ctx, f.admin, filter)
		if err != nil {
			errs <- fmt.Errorf("series: %w", err)
		} else if len(got.Data) != 25 || got.Metadata.LedgerSeq != got.Metadata.ProjectedSeq {
			errs <- fmt.Errorf("inconsistent series: %+v", got)
		}
	}()
	wg.Wait()
	close(errs)
	t.Logf("concurrent reads with writer held: elapsed=%s writer_wait_count=%d writer_wait_duration=%s", time.Since(start), f.database.Stats().WaitCount-before.WaitCount, f.database.Stats().WaitDuration-before.WaitDuration)
	if got := f.database.Stats().WaitCount; got != before.WaitCount {
		t.Errorf("audit readers entered the occupied business pool: waits=%d", got-before.WaitCount)
	}
	for err := range errs {
		t.Error(err)
	}
}

type auditAuthHook struct {
	FinalAuthorizer
	after func()
}

func (a auditAuthHook) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, id int64) error {
	if err := a.FinalAuthorizer.AuthorizeAdmin(ctx, tx, id); err != nil {
		return err
	}
	a.after()
	return nil
}

func TestAuditReadKeepsAuthorizationWatermarksAndInventoryInOneSnapshot(t *testing.T) {
	f := newAuditFixture(t)
	f.fund(t, 1, auditNow-2)
	f.project(t)
	auth := f.service.auth
	f.service.auth = auditAuthHook{FinalAuthorizer: auth, after: func() {
		f.fund(t, 1, auditNow-1)
		f.project(t)
	}}
	got, err := f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General))
	if err != nil || got.Metadata.LedgerSeq != "1" || got.Metadata.ProjectedSeq != "1" || got.Inventory == nil || got.Inventory.Net != "1000" || got.Flows.Issued != "1000" || got.Reconciliation.Status != "matched" {
		t.Fatalf("snapshot changed during read: %+v, %v", got, err)
	}
	f.service.auth = auth
	got, err = f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General))
	if err != nil || got.Metadata.LedgerSeq != "2" || got.Inventory == nil || got.Inventory.Net != "2000" {
		t.Fatal(got, err)
	}
}

func TestCanceledAuditReadReleasesConnection(t *testing.T) {
	f := newAuditFixture(t)
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	_, err := f.service.Summary(ctx, f.admin, auditFilter(ledger.General))
	var failure *readFailure
	if !errors.Is(err, context.Canceled) || !errors.As(err, &failure) || failure.stage != "connection" {
		t.Fatal(err)
	}
	if _, err := f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General)); err != nil {
		t.Fatal(err)
	}
}
