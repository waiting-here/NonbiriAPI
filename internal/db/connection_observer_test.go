package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type connectionSample struct{ active, completed, failed, canceled int }
type recordingConnectionObserver struct {
	mu      sync.Mutex
	samples map[string]connectionSample
}

func newConnectionRecorder() *recordingConnectionObserver {
	return &recordingConnectionObserver{samples: make(map[string]connectionSample)}
}
func (r *recordingConnectionObserver) StartConnectionSpan(ctx context.Context, kind string) func(error) {
	r.mu.Lock()
	s := r.samples[kind]
	s.active++
	r.samples[kind] = s
	r.mu.Unlock()
	return func(err error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		s := r.samples[kind]
		s.active--
		s.completed++
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			s.canceled++
		} else if err != nil {
			s.failed++
		}
		r.samples[kind] = s
	}
}
func (r *recordingConnectionObserver) sample(kind string) connectionSample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.samples[kind]
}
func (r *recordingConnectionObserver) assertIdle(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for kind, s := range r.samples {
		if s.active != 0 {
			t.Fatalf("%s retained observation: %+v", kind, s)
		}
	}
}
func observedTestDB(t *testing.T) (*sql.DB, *recordingConnectionObserver) {
	t.Helper()
	r := newConnectionRecorder()
	slot := &connectionObserverSlot{}
	slot.current.Store(&connectionObservation{observer: r})
	d, err := openObservedSQLiteHandle(":memory:", "rwc", slot)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
		r.assertIdle(t)
	})
	return d, r
}

func TestConnectionObservationRowsAndPoolWait(t *testing.T) {
	d, r := observedTestDB(t)
	rows, err := d.QueryContext(t.Context(), "SELECT 1 AS value UNION ALL SELECT 2")
	if err != nil {
		t.Fatal(err)
	}
	if r.sample("hold").active != 1 || r.sample("rows").active != 1 {
		t.Fatal("rows did not retain occupancy")
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].Name() != "value" {
		t.Fatalf("column metadata: %v %v", types, err)
	}
	waiting, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := d.PingContext(waiting); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait = %v", err)
	}
	if r.sample("hold").active != 1 || r.sample("hold").completed != 0 || d.Stats().WaitCount != 1 {
		t.Fatal("pool wait counted as another hold")
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	r.assertIdle(t)
	if s := r.sample("hold"); s.completed != 1 || s.canceled != 0 {
		t.Fatalf("hold = %+v", s)
	}

	rows, err = d.QueryContext(t.Context(), "SELECT 3")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	r.assertIdle(t)
	if r.sample("rows").completed != 2 {
		t.Fatal("EOF or repeated Close counted incorrectly")
	}
}

func TestConnectionObservationTransactionPreparedAndErrors(t *testing.T) {
	d, r := observedTestDB(t)
	if _, err := d.Exec("CREATE TABLE values_table(value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	stmt, err := d.PrepareContext(t.Context(), "INSERT INTO values_table VALUES (?)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if _, err := stmt.ExecContext(t.Context(), 7); err != nil {
		t.Fatal(err)
	}
	query, err := d.PrepareContext(t.Context(), "SELECT value FROM values_table")
	if err != nil {
		t.Fatal(err)
	}
	defer query.Close()
	var n int
	if err := query.QueryRowContext(t.Context()).Scan(&n); err != nil || n != 7 {
		t.Fatalf("%d %v", n, err)
	}
	beforeExec, beforeRows := r.sample("exec").completed, r.sample("rows").completed
	for _, commit := range []bool{true, false} {
		tx, err := d.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), "INSERT INTO values_table VALUES (9)"); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM values_table").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if r.sample("exec").completed != beforeExec || r.sample("rows").completed != beforeRows {
		t.Fatal("transaction statements double counted as nontransaction phases")
	}
	if r.sample("transaction").completed != 2 || r.sample("prepare").completed != 2 {
		t.Fatalf("unexpected phases: %+v", r.samples)
	}
	if _, err := d.Exec("INSERT INTO missing_table VALUES (1)"); err == nil {
		t.Fatal("invalid exec succeeded")
	}
	if _, err := d.Query("SELECT missing FROM missing_table"); err == nil {
		t.Fatal("invalid query succeeded")
	}
	if _, err := d.Prepare("SELECT missing FROM missing_table"); err == nil {
		t.Fatal("invalid prepare succeeded")
	}
	if r.sample("hold").failed != 3 {
		t.Fatalf("errors lost: %+v", r.sample("hold"))
	}
	r.assertIdle(t)
}

func TestConnectionObservationCancellationAndReplacement(t *testing.T) {
	d, r := observedTestDB(t)
	ctx, cancel := context.WithCancel(t.Context())
	rows, err := d.QueryContext(ctx, "SELECT 1 UNION ALL SELECT 2")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := rows.Close(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	r.assertIdle(t)
	if r.sample("hold").canceled != 1 || r.sample("rows").canceled != 1 {
		t.Fatal("cancellation lost")
	}
	txCtx, cancelTx := context.WithCancel(t.Context())
	tx, err := d.BeginTx(txCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancelTx()
	_ = tx.Rollback()
	// Wait for database/sql's cancellation rollback, using its pool as the barrier.
	if err := d.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.assertIdle(t)
	if r.sample("transaction").canceled != 1 {
		t.Fatalf("canceled transaction: %+v", r.sample("transaction"))
	}
	conn, err := d.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	_ = conn.Close()
	if err := d.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.assertIdle(t)
	if r.sample("hold").completed != 5 {
		t.Fatalf("retired and replacement connection observations: %+v", r.sample("hold"))
	}
}

func TestConnectionObservationRegisteredAfterStartup(t *testing.T) {
	store := openTestStore(t, filepath.Join(privateDBDir(t), "observed.sqlite"))
	recorder := newConnectionRecorder()
	store.SetConnectionObserver(recorder)
	if err := store.DB().PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if recorder.sample("hold").completed != 1 {
		t.Fatal("existing writer did not pick up observer")
	}
	if err := store.ReadDB().PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if recorder.sample("hold").completed != 1 {
		t.Fatal("reader mixed into writer occupancy")
	}
	store.SetConnectionObserver(nil)
	if err := store.DB().PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if recorder.sample("hold").completed != 1 {
		t.Fatal("disabled observer still recording")
	}
	recorder.assertIdle(t)
}
