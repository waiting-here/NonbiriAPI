package duel

import (
	"context"
	"database/sql"
)

// adminRead reuses the finite set of query shapes in one history request.
// Statements and results never cross the transaction or authorization boundary.
type adminRead struct {
	tx         *sql.Tx
	statements map[string]*sql.Stmt
}

func (r *adminRead) prepare(ctx context.Context, query string) (*sql.Stmt, error) {
	if stmt := r.statements[query]; stmt != nil {
		return stmt, nil
	}
	stmt, err := r.tx.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	if r.statements == nil {
		r.statements = make(map[string]*sql.Stmt)
	}
	r.statements[query] = stmt
	return stmt, nil
}

func (r *adminRead) close() {
	for _, stmt := range r.statements {
		stmt.Close()
	}
}

type adminReadError struct{ err error }

func (e adminReadError) Scan(...any) error { return e.err }

func (r *adminRead) row(ctx context.Context, query string, args ...any) scanner {
	stmt, err := r.prepare(ctx, query)
	if err != nil {
		return adminReadError{err}
	}
	return stmt.QueryRowContext(ctx, args...)
}

func (r *adminRead) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	stmt, err := r.prepare(ctx, query)
	if err != nil {
		return nil, err
	}
	return stmt.QueryContext(ctx, args...)
}
