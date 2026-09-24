package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteReplacementConnectionPreservesConstraints(t *testing.T) {
	for _, replace := range []string{"discard", "cancelled_transaction"} {
		t.Run(replace, func(t *testing.T) {
			database, err := openSQLite(filepath.Join(t.TempDir(), "connection.db"), "rwc")
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if _, err := database.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
CREATE TABLE parents (id INTEGER PRIMARY KEY);
CREATE TABLE children (id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parents(id) ON DELETE CASCADE);
INSERT INTO parents VALUES(1); INSERT INTO children VALUES(1,1);`); err != nil {
				t.Fatal(err)
			}
			if replace == "discard" {
				connection, err := database.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				_ = connection.Raw(func(any) error { return driver.ErrBadConn })
				_ = connection.Close()
			} else {
				ctx, cancel := context.WithCancel(context.Background())
				transaction, err := database.BeginTx(ctx, &sql.TxOptions{})
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				defer transaction.Rollback()
				cancel()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var foreignKeys, busyTimeout int
			if err := database.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
				t.Fatal(err)
			}
			if err := database.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
				t.Fatal(err)
			}
			if foreignKeys != 1 || busyTimeout != 5000 {
				t.Fatalf("replacement connection lost protections: foreign_keys=%d busy_timeout=%d", foreignKeys, busyTimeout)
			}
			if _, err := database.ExecContext(ctx, `INSERT INTO children VALUES(2,999)`); err == nil {
				t.Fatal("replacement connection accepted an orphan reference")
			}
			if _, err := database.ExecContext(ctx, `DELETE FROM parents WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			var children int
			if err := database.QueryRowContext(ctx, `SELECT count(*) FROM children`).Scan(&children); err != nil || children != 0 {
				t.Fatalf("cascade failed after connection replacement: children=%d err=%v", children, err)
			}
		})
	}
}
