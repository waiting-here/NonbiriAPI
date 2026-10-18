package observability

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestDatabaseInFlightTimeSpansWindows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := NewPerformance()
		ctx := p.Context(context.Background())
		finish := databaseTimer(ctx, "transaction", "lake_checkpoint")
		time.Sleep(time.Minute)
		row := p.Drain()[0]
		if row.Completed != 0 || row.Active != 1 || row.ActiveMS != 60000 || row.MaxMS != 0 {
			t.Fatalf("unfinished transaction = %+v", row)
		}
		time.Sleep(2 * time.Second)
		finish(nil)
		finish(nil)
		row = p.Drain()[0]
		if row.Completed != 1 || row.Active != 0 || row.ActiveMS != 2000 || row.MaxMS != 62000 {
			t.Fatalf("completed transaction = %+v", row)
		}
		if rows := p.Drain(); len(rows) != 0 {
			t.Fatalf("unexpected empty-window samples = %+v", rows)
		}
	})
}

func TestDatabasePhasesRejectUnregisteredLabels(t *testing.T) {
	p := NewPerformance()
	ctx := p.Context(context.Background())
	DatabaseOperation(nil, "lake_checkpoint")(nil)
	DatabasePhase(ctx, "private-user", "read")(nil)
	DatabasePhase(ctx, "lake_checkpoint", "private-query")(nil)
	DatabaseOperation(ctx, "private-user")(nil)
	if rows := p.Drain(); len(rows) != 0 {
		t.Fatalf("unregistered labels recorded: %+v", rows)
	}
	finish := DatabasePhase(ctx, "lake_checkpoint", "rules")
	finish(errors.New("private-error"))
	finish(nil)
	if rows := p.Drain(); len(rows) != 1 || rows[0].Route != "phase.rules.lake_checkpoint" || rows[0].Failures != 1 || rows[0].Completed != 1 {
		t.Fatalf("phase result = %+v", rows)
	}
}

func TestMeasuredTransactionSeparatesAcquisitionAndReturnsConnection(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	p := NewPerformance()
	ctx := p.Context(context.Background())
	held, err := database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, _, err := BeginMeasuredTx(waitCtx, database, nil, "catalog"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked acquisition = %v", err)
	}
	_ = held.Close()
	rows := p.Drain()
	if len(rows) != 1 || rows[0].Route != "connection.catalog" || rows[0].Canceled != 1 || rows[0].Active != 0 {
		t.Fatalf("acquisition report = %+v", rows)
	}
	tx, finish, err := BeginMeasuredTx(ctx, database, nil, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	_, queryErr := tx.ExecContext(ctx, "SELECT missing FROM missing_table")
	if queryErr == nil {
		t.Fatal("invalid query succeeded")
	}
	finish(queryErr)
	finish(queryErr)
	if database.Stats().InUse != 0 {
		t.Fatal("failed transaction retained a connection")
	}
	rows = p.Drain()
	if len(rows) != 2 || rows[1].Route != "transaction.catalog" || rows[1].Failures != 1 || rows[1].ServerErrors != 0 || rows[1].Completed != 1 {
		t.Fatalf("transaction report = %+v", rows)
	}
	tx, finish, err = BeginMeasuredTx(ctx, database, nil, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	err = tx.Commit()
	finish(err)
	if err != nil || database.Stats().InUse != 0 {
		t.Fatalf("commit = %v, stats = %+v", err, database.Stats())
	}
}

func TestHTTPWindowPeakDoesNotSumRoutePeaksOrDatabaseWork(t *testing.T) {
	p := NewPerformance()
	first := p.begin(performanceKey{"/api/me", "GET"})
	p.begin(performanceKey{"transaction.catalog", ""})(time.Millisecond, 0, false, false)
	first(time.Millisecond, 0, false, false)
	second := p.begin(performanceKey{"/v1/models", "GET"})
	_, totals := p.drain()
	if totals.Completed != 1 || totals.Active != 1 || totals.Peak != 1 {
		t.Fatalf("first HTTP window = %+v", totals)
	}
	second(time.Second, 0, false, true)
	_, totals = p.drain()
	if totals.Completed != 1 || totals.Active != 0 || totals.Peak != 1 {
		t.Fatalf("second HTTP window = %+v", totals)
	}
	_, totals = p.drain()
	if totals != (HTTPPerformance{}) {
		t.Fatalf("empty HTTP window = %+v", totals)
	}
}
