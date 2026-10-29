package db

import (
	"context"
	"testing"
)

type captureTestKey struct{}

func TestManagementCaptureCommitsOnlyDrainedSnapshots(t *testing.T) {
	slot := &connectionObserverSlot{}
	slot.capture.Store(&managementCapture{eventID: func(ctx context.Context) string { id, _ := ctx.Value(captureTestKey{}).(string); return id }})
	d, err := openObservedSQLiteHandle(":memory:", "rwc", slot)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = d.Close() })
	_, err = d.Exec(`CREATE TABLE management_capture_context(id INTEGER PRIMARY KEY,event_id TEXT);CREATE TABLE management_capture_rows(event_id TEXT);CREATE TABLE management_capture_totals(event_id TEXT);CREATE TABLE business(value TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), captureTestKey{}, "event1")
	for _, drained := range []bool{false, true} {
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var id string
		if err = tx.QueryRow(`SELECT event_id FROM management_capture_context`).Scan(&id); err != nil || id != "event1" {
			t.Fatalf("activation missing: %q %v", id, err)
		}
		if _, err = tx.Exec(`INSERT INTO business VALUES('change');INSERT INTO management_capture_rows VALUES('event1')`); err != nil {
			t.Fatal(err)
		}
		if drained {
			if _, err = tx.Exec(`DELETE FROM management_capture_rows`); err != nil {
				t.Fatal(err)
			}
		}
		err = tx.Commit()
		if (err == nil) != drained {
			t.Fatalf("drained=%v commit error=%v", drained, err)
		}
		var count int
		if err = d.QueryRow(`SELECT count(*) FROM management_capture_context`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("stale capture: %d %v", count, err)
		}
	}
	var count int
	if err = d.QueryRow(`SELECT count(*) FROM business`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("business rollback was not atomic: %d %v", count, err)
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = d.QueryRow(`SELECT count(*) FROM management_capture_context`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback left activation: %d %v", count, err)
	}
}
