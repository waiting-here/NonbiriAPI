package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openDMLTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := openSQLite(filepath.Join(t.TempDir(), "statements.sqlite"), "rwc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := database.Exec(`CREATE TABLE entries(id INTEGER PRIMARY KEY, value BLOB CHECK(value IS NULL OR length(value)>0))`); err != nil {
		t.Fatal(err)
	}
	return database
}

func inspectDMLConnection(t *testing.T, database *sql.DB, inspect func(*dmlConnection)) {
	t.Helper()
	conn, err := database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Raw(func(raw any) error {
		cached, ok := raw.(*dmlConnection)
		if !ok {
			t.Fatalf("writer has no statement reuse: %T", raw)
		}
		inspect(cached)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDMLReuseBindingsRollbackAndErrors(t *testing.T) {
	database := openDMLTestDB(t)
	const query = `INSERT INTO entries(id,value) VALUES(:id,:value)`
	for i, value := range []any{[]byte("first"), nil, "third"} {
		tx, err := database.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := tx.ExecContext(t.Context(), query, sql.Named("id", i+1), sql.Named("value", value))
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			t.Fatalf("affected=%d err=%v", affected, err)
		}
		if i == 2 {
			err = tx.Rollback()
		} else {
			err = tx.Commit()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	var saved string
	var empty sql.NullString
	var count int
	if err := database.QueryRow(`SELECT value FROM entries WHERE id=1`).Scan(&saved); err != nil || saved != "first" {
		t.Fatalf("first binding: %q %v", saved, err)
	}
	if err := database.QueryRow(`SELECT value FROM entries WHERE id=2`).Scan(&empty); err != nil || empty.Valid {
		t.Fatalf("NULL inherited prior binding: %+v %v", empty, err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM entries`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("rollback count=%d err=%v", count, err)
	}
	for _, args := range [][]any{{sql.Named("id", 1), sql.Named("value", "duplicate")}, {sql.Named("id", 4)}} {
		if _, err := database.ExecContext(t.Context(), query, args...); err == nil {
			t.Fatal("constraint or missing binding accepted")
		}
	}
	if _, err := database.Exec(query, sql.Named("id", 4), sql.Named("value", "recovered")); err != nil {
		t.Fatal(err)
	}
	// An already prepared statement must honor a subsequently installed trigger.
	if _, err := database.Exec(`CREATE TRIGGER reject_five BEFORE INSERT ON entries WHEN NEW.id=5 BEGIN SELECT RAISE(ABORT,'rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(query, sql.Named("id", 5), sql.Named("value", "blocked")); err == nil {
		t.Fatal("prepared statement bypassed new trigger")
	}
	if _, err := database.Exec(`DROP TRIGGER reject_five`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(query, sql.Named("id", 5), sql.Named("value", "allowed")); err != nil {
		t.Fatal(err)
	}
}

func TestDMLReuseBoundEvictionAndReplacement(t *testing.T) {
	database := openDMLTestDB(t)
	const hot = `UPDATE entries SET value=? WHERE id=?`
	for i := range dmlStatementLimit + 8 {
		if _, err := database.Exec(fmt.Sprintf(`UPDATE entries SET value=? WHERE id=? /* %d */`, i), "cold", 1); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(hot, "hot", 1); err != nil {
			t.Fatal(err)
		}
	}
	var old *dmlConnection
	inspectDMLConnection(t, database, func(c *dmlConnection) {
		old = c
		if len(c.statements) != dmlStatementLimit || c.statements[hot] == nil || c.statements[`UPDATE entries SET value=? WHERE id=? /* 0 */`] != nil {
			t.Fatal("bounded LRU did not retain the hot statement")
		}
	})
	// Scripts, long SQL and explicit prepared statements retain ordinary ownership.
	if _, err := database.Exec(`INSERT INTO entries VALUES(1,'script'); UPDATE entries SET value='updated' WHERE id=1;`); err != nil {
		t.Fatal(err)
	}
	longSQL := `UPDATE entries SET value=? WHERE id=? /*` + strings.Repeat("x", dmlSQLByteLimit) + `*/`
	if _, err := database.Exec(longSQL, "long", 1); err != nil {
		t.Fatal(err)
	}
	stmt, err := database.PrepareContext(t.Context(), `UPDATE entries SET value=? WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.Exec("explicit"); err != nil {
		t.Fatal(err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	conn, err := database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
	if len(old.statements) != 0 {
		t.Fatal("discarded connection retained statements")
	}
	if _, err := database.Exec(hot, "replacement", 1); err != nil {
		t.Fatal(err)
	}
	inspectDMLConnection(t, database, func(c *dmlConnection) {
		if c == old || len(c.statements) != 1 || c.statements[hot] == nil {
			t.Fatal("replacement reused a closed statement")
		}
		old = c
	})
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if len(old.statements) != 0 || database.Stats().OpenConnections != 0 {
		t.Fatal("close retained a connection or statement")
	}
}

func TestDMLReuseCancellationAndConcurrentTransactions(t *testing.T) {
	database := openDMLTestDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err := database.ExecContext(ctx, `INSERT INTO entries(id,value)
		WITH RECURSIVE sequence(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM sequence WHERE x<1000000000)
		SELECT x,'cancelled' FROM sequence`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("execution cancellation: %v", err)
	}
	const query = `INSERT INTO entries(id,value) VALUES(?,?)`
	errs := make(chan error, 16)
	for i := range 16 {
		go func() {
			tx, err := database.BeginTx(t.Context(), nil)
			if err == nil {
				defer tx.Rollback()
				_, err = tx.ExecContext(t.Context(), query, i+1, fmt.Sprint(i))
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
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM entries WHERE value=CAST(id-1 AS TEXT)`).Scan(&count); err != nil || count != 16 {
		t.Fatalf("concurrent parameters or cancellation rollback: count=%d err=%v", count, err)
	}
}
