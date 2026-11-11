package db

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

// The first result must be immediate, with expensive work only in Rows.Next.
// Testing only QueryContext misses this failure mode in the upstream driver.
const sparseTail = `SELECT 1 AS value UNION ALL SELECT (WITH RECURSIVE n(x) AS
 (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10000000) SELECT SUM(x) FROM n)`

func TestRowCancellationAcrossConnectionPaths(t *testing.T) {
	for _, mode := range []string{"writer", "reader", "prepared", "transaction"} {
		t.Run(mode, func(t *testing.T) {
			database := openDMLTestDB(t)
			if mode == "reader" {
				reader, err := sql.Open("sqlite", "file:row-cancel?mode=memory&cache=private")
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				database = reader
			}
			database.SetMaxOpenConns(1)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var rows *sql.Rows
			var err error
			switch mode {
			case "prepared":
				stmt, e := database.PrepareContext(t.Context(), sparseTail)
				if e != nil {
					t.Fatal(e)
				}
				defer stmt.Close()
				rows, err = stmt.QueryContext(ctx)
			case "transaction":
				tx, e := database.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback()
				rows, err = tx.QueryContext(ctx, sparseTail)
			default:
				rows, err = database.QueryContext(ctx, sparseTail)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				t.Fatal("missing immediate row", rows.Err())
			}
			var first int
			if err := rows.Scan(&first); err != nil || first != 1 {
				t.Fatalf("first=%d err=%v", first, err)
			}
			started := time.Now()
			timer := time.AfterFunc(30*time.Millisecond, cancel)
			defer timer.Stop()
			if rows.Next() {
				t.Fatal("canceled tail returned a result")
			}
			if !errors.Is(rows.Err(), context.Canceled) {
				t.Fatalf("tail: %v", rows.Err())
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("canceled tail retained connection for %v", elapsed)
			}
			rows.Close()
			if mode != "transaction" {
				var value int
				if err := database.QueryRowContext(t.Context(), "SELECT 7").Scan(&value); err != nil || value != 7 {
					t.Fatalf("reuse: %d %v", value, err)
				}
			}
		})
	}
}

func TestInterleavedRowsKeepIndependentContexts(t *testing.T) {
	database := openDMLTestDB(t)
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithCancel(t.Context())
	a, err := tx.QueryContext(ctx, sparseTail)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !a.Next() {
		t.Fatal(a.Err())
	}
	b, err := tx.QueryContext(t.Context(), "SELECT 3 UNION ALL SELECT 4")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	defer cancel()
	timer := time.AfterFunc(30*time.Millisecond, cancel)
	defer timer.Stop()
	if a.Next() || !errors.Is(a.Err(), context.Canceled) {
		t.Fatalf("canceled cursor: %v", a.Err())
	}
	for _, want := range []int{3, 4} {
		if !b.Next() {
			t.Fatal("unrelated cursor interrupted", b.Err())
		}
		var value int
		if err := b.Scan(&value); err != nil || value != want {
			t.Fatalf("value=%d want=%d err=%v", value, want, err)
		}
	}
	if b.Next() || b.Err() != nil {
		t.Fatal(b.Err())
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedRowCancellationRebindsValues(t *testing.T) {
	database := openDMLTestDB(t)
	database.SetMaxOpenConns(1)
	stmt, err := database.PrepareContext(t.Context(), `SELECT ?1 UNION ALL SELECT (
 WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?2) SELECT SUM(x) FROM n)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	rows, err := stmt.QueryContext(ctx, make([]byte, 64*1024), 10000000)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	timer := time.AfterFunc(30*time.Millisecond, cancel)
	defer timer.Stop()
	if rows.Next() || !errors.Is(rows.Err(), context.Canceled) {
		t.Fatalf("tail cancellation: %v", rows.Err())
	}
	rows.Close()
	// Rebind on the same prepared statement after its interrupted VM reset.
	// BLOB/text/NULL/integer parameters must not retain or reuse freed values.
	for _, want := range []any{"fresh", nil, []byte("blob"), int64(7)} {
		rows, err := stmt.QueryContext(t.Context(), want, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var got any
		if err := rows.Scan(&got); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got=%v want=%v err=%v", got, want, err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
