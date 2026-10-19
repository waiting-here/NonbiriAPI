package db

import (
	"container/list"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"

	"modernc.org/sqlite"
)

const (
	dmlStatementLimit = 128
	dmlSQLByteLimit   = 16 * 1024
)

// Keep the driver's cancellation, transaction and connection validity behavior.
// database/sql serializes all access to a physical connection, including Close.
type sqliteConnection interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
	sqlite.DBStatus
}

type dmlConnector struct{ driver.Connector }

func (c dmlConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	base, ok := conn.(sqliteConnection)
	if !ok {
		return nil, errors.Join(errors.New("SQLite connection interfaces unavailable"), conn.Close())
	}
	return &dmlConnection{sqliteConnection: base, statements: make(map[string]*list.Element)}, nil
}

type executableStatement interface {
	driver.Stmt
	driver.StmtExecContext
}

type dmlStatement struct {
	query string
	stmt  executableStatement
}

type dmlConnection struct {
	sqliteConnection
	statements map[string]*list.Element
	recent     list.List
	queries    map[string]*list.Element
	queryLRU   list.List
}

func openSQLiteHandle(dsn, mode string) (*sql.DB, error) {
	if mode == "ro" {
		return sql.Open("sqlite", dsn)
	}
	connector, err := sqlite.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(dmlConnector{connector}), nil
}

func reusableDML(query string) bool {
	if len(query) > dmlSQLByteLimit || strings.ContainsAny(query, ";\x00") {
		return false
	}
	trimmed := strings.TrimSpace(query)
	end := strings.IndexAny(trimmed, " \t\r\n")
	if end < 0 {
		return false
	}
	switch trimmed[:end] {
	case "INSERT", "UPDATE", "DELETE":
		return true
	default:
		return false
	}
}

func (c *dmlConnection) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !reusableDML(query) {
		return c.sqliteConnection.ExecContext(ctx, query, args)
	}
	entry := c.statements[query]
	if entry == nil {
		// Prepare on the connection already held by this operation. Borrowing
		// another DB connection here would deadlock a one-connection writer pool.
		stmt, err := c.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		executable, ok := stmt.(executableStatement)
		if !ok {
			return nil, errors.Join(errors.New("SQLite statement context execution unavailable"), stmt.Close())
		}
		if len(c.statements) == dmlStatementLimit {
			if err := c.remove(c.recent.Back()); err != nil {
				return nil, errors.Join(err, stmt.Close())
			}
		}
		entry = c.recent.PushFront(dmlStatement{query: query, stmt: executable})
		c.statements[query] = entry
	} else {
		c.recent.MoveToFront(entry)
	}
	result, err := entry.Value.(dmlStatement).stmt.ExecContext(ctx, args)
	if err != nil {
		// Never retain a statement after a binding, constraint or cancellation
		// failure. Preserve the driver's error and let database/sql retire bad connections.
		_ = c.remove(entry)
	}
	return result, err
}

func (c *dmlConnection) remove(entry *list.Element) error {
	statement := entry.Value.(dmlStatement)
	delete(c.statements, statement.query)
	c.recent.Remove(entry)
	return statement.stmt.Close()
}

func (c *dmlConnection) Close() error {
	result := c.closeQueries()
	for c.recent.Len() != 0 {
		result = errors.Join(result, c.remove(c.recent.Back()))
	}
	return errors.Join(result, c.sqliteConnection.Close())
}
