package db

import (
	"container/list"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
)

const queryStatementLimit = 64

type queryStatement interface {
	driver.Stmt
	driver.StmtQueryContext
}

type cachedQuery struct {
	query string
	stmt  queryStatement
	busy  bool
}

// Preserve the metadata interfaces provided by the SQLite driver.
type sqliteRows interface {
	driver.Rows
	driver.RowsColumnTypeDatabaseTypeName
	driver.RowsColumnTypeLength
	driver.RowsColumnTypeNullable
	driver.RowsColumnTypePrecisionScale
	driver.RowsColumnTypeScanType
}

func reusableQuery(query string) bool {
	if len(query) > dmlSQLByteLimit || strings.ContainsAny(query, ";\x00") {
		return false
	}
	trimmed := strings.TrimSpace(query)
	end := strings.IndexAny(trimmed, " \t\r\n")
	return end >= 0 && trimmed[:end] == "SELECT"
}

func (c *dmlConnection) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !reusableQuery(query) {
		return c.sqliteConnection.QueryContext(ctx, query, args)
	}
	entry := c.queries[query]
	if entry != nil && entry.Value.(*cachedQuery).busy {
		// Nested queries may have the same SQL while the first cursor is open.
		return c.sqliteConnection.QueryContext(ctx, query, args)
	}
	if entry == nil {
		if len(c.queries) == queryStatementLimit {
			oldest := c.queryLRU.Back()
			for oldest != nil && oldest.Value.(*cachedQuery).busy {
				oldest = oldest.Prev()
			}
			if oldest == nil {
				return c.sqliteConnection.QueryContext(ctx, query, args)
			}
			if err := c.removeQuery(oldest); err != nil {
				return nil, err
			}
		}
		stmt, err := c.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		prepared, ok := stmt.(queryStatement)
		if !ok {
			return nil, errors.Join(errors.New("SQLite statement context query unavailable"), stmt.Close())
		}
		if c.queries == nil {
			c.queries = make(map[string]*list.Element)
		}
		entry = c.queryLRU.PushFront(&cachedQuery{query: query, stmt: prepared})
		c.queries[query] = entry
	} else {
		c.queryLRU.MoveToFront(entry)
	}
	statement := entry.Value.(*cachedQuery)
	rows, err := statement.stmt.QueryContext(ctx, args)
	if err != nil {
		_ = c.removeQuery(entry)
		return nil, err
	}
	metadata, ok := rows.(sqliteRows)
	if !ok {
		return nil, errors.Join(errors.New("SQLite row metadata unavailable"), rows.Close(), c.removeQuery(entry))
	}
	statement.busy = true
	return &cachedQueryRows{sqliteRows: metadata, conn: c, entry: entry, ctx: ctx}, nil
}

type cachedQueryRows struct {
	sqliteRows
	conn   *dmlConnection
	entry  *list.Element
	ctx    context.Context
	failed bool
	closed bool
}

func (r *cachedQueryRows) Next(dest []driver.Value) error {
	err := r.sqliteRows.Next(dest)
	if err != nil && err != io.EOF {
		r.failed = true
	}
	return err
}

func (r *cachedQueryRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	err := r.sqliteRows.Close()
	r.entry.Value.(*cachedQuery).busy = false
	if err != nil || r.failed || r.ctx.Err() != nil {
		err = errors.Join(err, r.conn.removeQuery(r.entry))
	}
	return err
}

func (c *dmlConnection) removeQuery(entry *list.Element) error {
	statement := entry.Value.(*cachedQuery)
	delete(c.queries, statement.query)
	c.queryLRU.Remove(entry)
	return statement.stmt.Close()
}

func (c *dmlConnection) closeQueries() error {
	var err error
	for c.queryLRU.Len() != 0 {
		err = errors.Join(err, c.removeQuery(c.queryLRU.Back()))
	}
	return err
}
