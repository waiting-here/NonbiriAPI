package db

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"sync/atomic"
)

// ConnectionObserver receives fixed driver lifecycle classes, never SQL or
// arguments. Hold spans include nested phases; their elapsed times are not additive.
type ConnectionObserver interface {
	StartConnectionSpan(context.Context, string) func(error)
}
type connectionObservation struct{ observer ConnectionObserver }
type connectionObserverSlot struct {
	current atomic.Pointer[connectionObservation]
}
type connectionObserverKey struct{}

// SetConnectionObserver attaches an observer to existing and future writer
// connections. Reader occupancy remains separately available through pool stats.
func (s *Store) SetConnectionObserver(observer ConnectionObserver) {
	if s.connectionObserver == nil {
		return
	}
	var value *connectionObservation
	if observer != nil {
		value = &connectionObservation{observer: observer}
	}
	s.connectionObserver.current.Store(value)
}

type observedConnection struct {
	sqliteConnection
	slot          *connectionObserverSlot
	hold          func(error)
	holdError     error
	transaction   func(error)
	inTransaction bool
}

func (c *observedConnection) acquire(ctx context.Context) {
	if c.hold != nil {
		return
	}
	if value := c.slot.current.Load(); value != nil {
		c.hold = value.observer.StartConnectionSpan(ctx, "hold")
		c.holdError = nil
	}
}
func (c *observedConnection) phase(ctx context.Context, kind string) func(error) {
	c.acquire(ctx)
	if c.inTransaction {
		return nil
	}
	if value := c.slot.current.Load(); value != nil {
		return value.observer.StartConnectionSpan(ctx, kind)
	}
	return nil
}
func (c *observedConnection) outcome(err error) {
	if err != nil && err != io.EOF && err != driver.ErrSkip {
		c.holdError = err
	}
}
func finishConnectionSpan(finish func(error), err error) {
	if finish != nil {
		finish(err)
	}
}
func (c *observedConnection) release(err error) {
	c.outcome(err)
	finishConnectionSpan(c.hold, c.holdError)
	c.hold = nil
	c.holdError = nil
}

// database/sql calls ResetSession when borrowing a pooled connection and
// IsValid before returning it. A fresh connection has no ResetSession call;
// start at its first driver operation to avoid counting async opener idle time.
func (c *observedConnection) ResetSession(ctx context.Context) error {
	c.acquire(ctx)
	err := c.sqliteConnection.ResetSession(ctx)
	if err != nil {
		c.release(err)
	}
	return err
}
func (c *observedConnection) IsValid() bool {
	valid := c.sqliteConnection.IsValid()
	var err error
	if !valid {
		err = driver.ErrBadConn
	}
	c.release(err)
	return valid
}
func (c *observedConnection) Close() error {
	err := c.sqliteConnection.Close()
	finishConnectionSpan(c.transaction, err)
	c.transaction = nil
	c.inTransaction = false
	c.release(err)
	return err
}
func (c *observedConnection) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *observedConnection) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	finish := c.phase(ctx, "transaction")
	tx, err := c.sqliteConnection.BeginTx(ctx, options)
	c.outcome(err)
	if err != nil {
		finishConnectionSpan(finish, err)
		return nil, err
	}
	c.transaction = finish
	c.inTransaction = true
	return &observedTransaction{Tx: tx, conn: c}, nil
}

type observedTransaction struct {
	driver.Tx
	conn *observedConnection
}

func (t *observedTransaction) end(err error) error {
	t.conn.outcome(err)
	finishConnectionSpan(t.conn.transaction, err)
	t.conn.transaction = nil
	t.conn.inTransaction = false
	return err
}
func (t *observedTransaction) Commit() error   { return t.end(t.Tx.Commit()) }
func (t *observedTransaction) Rollback() error { return t.end(t.Tx.Rollback()) }

func (c *observedConnection) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	finish := c.phase(ctx, "exec")
	result, err := c.sqliteConnection.ExecContext(ctx, query, args)
	c.outcome(err)
	finishConnectionSpan(finish, err)
	return result, err
}
func (c *observedConnection) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	finish := c.phase(ctx, "rows")
	rows, err := c.sqliteConnection.QueryContext(ctx, query, args)
	return c.observeRows(rows, err, finish)
}
func (c *observedConnection) observeRows(rows driver.Rows, err error, finish func(error)) (driver.Rows, error) {
	c.outcome(err)
	if err != nil {
		finishConnectionSpan(finish, err)
		return nil, err
	}
	if finish == nil {
		return rows, nil
	}
	metadata, ok := rows.(sqliteRows)
	if !ok {
		err = errors.Join(errors.New("SQLite row metadata unavailable"), rows.Close())
		c.outcome(err)
		finish(err)
		return nil, err
	}
	return &observedRows{sqliteRows: metadata, conn: c, finish: finish}, nil
}

type observedRows struct {
	sqliteRows
	conn    *observedConnection
	finish  func(error)
	failure error
}

func (r *observedRows) end(err error) {
	r.conn.outcome(err)
	finishConnectionSpan(r.finish, err)
	r.finish = nil
}
func (r *observedRows) Next(dest []driver.Value) error {
	err := r.sqliteRows.Next(dest)
	if err == io.EOF {
		r.end(nil)
	} else if err != nil {
		r.failure = err
		r.end(err)
	}
	return err
}
func (r *observedRows) Close() error {
	err := r.sqliteRows.Close()
	r.end(errors.Join(r.failure, err))
	return err
}
func (c *observedConnection) Ping(ctx context.Context) error {
	finish := c.phase(ctx, "ping")
	err := c.sqliteConnection.Ping(ctx)
	c.outcome(err)
	finishConnectionSpan(finish, err)
	return err
}

type observedStatementBase interface {
	driver.Stmt
	driver.StmtExecContext
	driver.StmtQueryContext
}

func (c *observedConnection) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}
func (c *observedConnection) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	finish := c.phase(ctx, "prepare")
	stmt, err := c.sqliteConnection.PrepareContext(ctx, query)
	if err != nil {
		c.outcome(err)
		finishConnectionSpan(finish, err)
		return nil, err
	}
	base, ok := stmt.(observedStatementBase)
	if !ok {
		err = errors.Join(errors.New("SQLite statement context interfaces unavailable"), stmt.Close())
		c.outcome(err)
		finishConnectionSpan(finish, err)
		return nil, err
	}
	finishConnectionSpan(finish, nil)
	return &observedStatement{observedStatementBase: base, conn: c}, nil
}

type observedStatement struct {
	observedStatementBase
	conn *observedConnection
}

func (s *observedStatement) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	finish := s.conn.phase(ctx, "exec")
	result, err := s.observedStatementBase.ExecContext(ctx, args)
	s.conn.outcome(err)
	finishConnectionSpan(finish, err)
	return result, err
}
func (s *observedStatement) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	finish := s.conn.phase(ctx, "rows")
	rows, err := s.observedStatementBase.QueryContext(ctx, args)
	return s.conn.observeRows(rows, err, finish)
}
func namedStatementValues(args []driver.Value) []driver.NamedValue {
	named := make([]driver.NamedValue, len(args))
	for i, value := range args {
		named[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	return named
}
func (s *observedStatement) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), namedStatementValues(args))
}
func (s *observedStatement) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), namedStatementValues(args))
}
