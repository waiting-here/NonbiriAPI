package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestQueryReuseBindingsFreshnessAndMetadata(t *testing.T) {
	database := openDMLTestDB(t)
	const query = `SELECT :value,typeof(:value)`
	for _, value := range []any{int64(7), "text", []byte("bytes"), nil, float64(1.5)} {
		var got any
		var kind string
		if err := database.QueryRowContext(t.Context(), query, sql.Named("value", value)).Scan(&got, &kind); err != nil || !reflect.DeepEqual(got, value) {
			t.Fatalf("binding %T = %#v (%s): %v", value, got, kind, err)
		}
	}
	if _, err := database.Exec(`INSERT INTO entries VALUES(1,'before')`); err != nil {
		t.Fatal(err)
	}
	const lookup = `SELECT value FROM entries WHERE id=?`
	for _, want := range []string{"before", "after"} {
		var got string
		if err := database.QueryRow(lookup, 1).Scan(&got); err != nil || got != want {
			t.Fatal(got, err)
		}
		if _, err := database.Exec(`UPDATE entries SET value='after' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
	}
	const projection = `SELECT * FROM entries`
	for pass := range 2 {
		rows, err := database.Query(projection)
		if err != nil {
			t.Fatal(err)
		}
		metadata, err := rows.ColumnTypes()
		if err != nil || len(metadata) != 2+pass {
			t.Fatalf("schema reprepare: %d %v", len(metadata), err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		// Explicit Prepare is the driver's uncached reference for all metadata.
		stmt, err := database.Prepare(projection)
		if err != nil {
			t.Fatal(err)
		}
		reference, err := stmt.Query()
		if err != nil {
			t.Fatal(err)
		}
		want, err := reference.ColumnTypes()
		if err != nil || !reflect.DeepEqual(metadata, want) {
			t.Fatalf("row metadata differs from driver: %#v %#v %v", metadata, want, err)
		}
		reference.Close()
		stmt.Close()
		if pass == 0 {
			if _, err := database.Exec(`ALTER TABLE entries ADD COLUMN extra VARCHAR(40)`); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestQueryReuseActiveRowsAndBound(t *testing.T) {
	database := openDMLTestDB(t)
	conn, err := database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	const query = `SELECT ? UNION ALL SELECT 'remaining'`
	first, err := conn.QueryContext(t.Context(), query, "first")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	var got string
	if err := conn.QueryRowContext(t.Context(), query, "second").Scan(&got); err != nil || got != "second" {
		t.Fatalf("active cursor binding: %q %v", got, err)
	}
	active := []*sql.Rows{first}
	for i := range queryStatementLimit + 3 {
		rows, err := conn.QueryContext(t.Context(), fmt.Sprintf(`SELECT %d`, i))
		if err != nil {
			t.Fatal(err)
		}
		active = append(active, rows)
	}
	if err := conn.Raw(func(raw any) error {
		if len(raw.(*dmlConnection).queries) != queryStatementLimit {
			t.Fatal("all-busy fallback exceeded the statement bound")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !first.Next() || first.Scan(&got) != nil || got != "first" {
		t.Fatalf("active statement was reused or evicted: %q %v", got, first.Err())
	}
	for _, rows := range active {
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// Early Close permits fresh binding and eviction of previously pinned entries.
	for i := range queryStatementLimit + 3 {
		var n int
		if err := conn.QueryRowContext(t.Context(), fmt.Sprintf(`SELECT %d`, 100+i)).Scan(&n); err != nil || n != 100+i {
			t.Fatal(n, err)
		}
	}
	conn.Close()
	inspectDMLConnection(t, database, func(c *dmlConnection) {
		if len(c.queries) != queryStatementLimit || c.queries[query] != nil {
			t.Fatal("closed cursor was not evictable")
		}
		for _, entry := range c.queries {
			if entry.Value.(*cachedQuery).busy {
				t.Fatal("closed cursor retained its pin")
			}
		}
	})
}

func TestQueryReuseFailureCancellationAndConnectionClose(t *testing.T) {
	database := openDMLTestDB(t)
	var n int
	const binding = `SELECT :value`
	if err := database.QueryRow(binding, sql.Named("value", 1)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(binding).Scan(&n); err == nil {
		t.Fatal("missing parameter inherited a previous value")
	}
	inspectDMLConnection(t, database, func(c *dmlConnection) {
		if c.queries[binding] != nil {
			t.Fatal("failed query retained")
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	const slow = `SELECT (WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100000000) SELECT SUM(x) FROM n)`
	if err := database.QueryRowContext(ctx, slow).Scan(&n); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("query cancellation: %v", err)
	}
	const badRow = `SELECT abs(-9223372036854775808)`
	if err := database.QueryRow(badRow).Scan(&n); err == nil {
		t.Fatal("row evaluation error lost")
	}
	const laterRow = `SELECT 1 UNION ALL SELECT abs(-9223372036854775808)`
	rows, err := database.Query(laterRow)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("first row unavailable", rows.Err())
	}
	if err := rows.Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if rows.Next() || rows.Err() == nil {
		t.Fatal("later row evaluation error lost")
	}
	rows.Close()
	var old *dmlConnection
	inspectDMLConnection(t, database, func(c *dmlConnection) {
		old = c
		if c.queries[slow] != nil || c.queries[badRow] != nil || c.queries[laterRow] != nil {
			t.Fatal("failed statement retained")
		}
	})
	conn, err := database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	conn.Close()
	if len(old.queries) != 0 {
		t.Fatal("bad connection retained prepared queries")
	}
	if err := database.QueryRow(binding, sql.Named("value", 9)).Scan(&n); err != nil || n != 9 {
		t.Fatal(n, err)
	}
	inspectDMLConnection(t, database, func(c *dmlConnection) { old = c })
	if err := database.Close(); err != nil || len(old.queries) != 0 {
		t.Fatal("shutdown retained queries", err)
	}
}

func TestQueryReuseSelectionAndConcurrentTransactions(t *testing.T) {
	for _, query := range []string{"SELECT; SELECT 1", "select 1", "WITH t AS(SELECT 1) SELECT * FROM t", "SELECT\x00 1", "SELECT 1 /*" + strings.Repeat("x", dmlSQLByteLimit) + "*/"} {
		if reusableQuery(query) {
			t.Fatal("unexpected reusable query", query[:6])
		}
	}
	database := openDMLTestDB(t)
	errs := make(chan error, 16)
	for i := range 16 {
		go func() {
			tx, err := database.BeginTx(t.Context(), nil)
			if err == nil {
				defer tx.Rollback()
				_, err = tx.Exec(`INSERT INTO entries VALUES(?,?)`, i+1, fmt.Sprint(i))
				if err == nil {
					var got string
					err = tx.QueryRow(`SELECT value FROM entries WHERE id=?`, i+1).Scan(&got)
					if err == nil && got != fmt.Sprint(i) {
						err = fmt.Errorf("crossed transaction binding: %q", got)
					}
				}
				if err == nil {
					err = tx.Commit()
				}
			}
			errs <- err
		}()
	}
	for range 16 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}
