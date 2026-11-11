package sqlite

import (
	"sync"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// Registered on first use and removed by conn.Close. database/sql serializes operations on a
// physical connection; neither the callback nor waitCtx is read asynchronously.
// No watcher survives a step to interrupt another cursor or the next borrower.
var rowContexts sync.Map // SQLite connection handle -> *conn

func (c *conn) watchRowContext() {
	if !c.rowContextRegistered {
		rowContexts.Store(c.db, c)
		c.rowContextRegistered = true
	}
	sqlite3.Xsqlite3_progress_handler(c.tls, c.db, 1000, cFuncPointer(rowContextProgress), c.db)
}

func (c *conn) unwatchRowContext() {
	sqlite3.Xsqlite3_progress_handler(c.tls, c.db, 0, 0, 0)
}

func rowContextProgress(_ *libc.TLS, handle uintptr) int32 {
	if value, ok := rowContexts.Load(handle); ok {
		if ctx := value.(*conn).waitCtx; ctx != nil && ctx.Err() != nil {
			return 1
		}
	}
	return 0
}
