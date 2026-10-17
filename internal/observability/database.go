package observability

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// Context attaches the same bounded observer to background work.
func (p *Performance) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, performanceContextKey{}, p)
}

func databaseOperation(operation string) bool {
	switch operation {
	case "session", "catalog", "personal_models", "charity_models", "charity_capability", "fishing_cleanup", "donation_expiry":
		return true
	default:
		return false
	}
}

func databaseTimer(ctx context.Context, phase, operation string) func(error) {
	p, _ := ctx.Value(performanceContextKey{}).(*Performance)
	if p == nil || !databaseOperation(operation) {
		return func(error) {}
	}
	started := time.Now()
	finish := p.begin(performanceKey{phase + "." + operation, ""})
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			canceled := ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
			finish(time.Since(started), 0, err != nil && !canceled, canceled)
		})
	}
}

// BeginMeasuredTx measures pool acquisition independently of transaction work.
// Call finish with the operation's result after commit, or defer it on errors.
// It rolls back an unfinished transaction and always returns its connection.
func BeginMeasuredTx(ctx context.Context, database *sql.DB, options *sql.TxOptions, operation string) (*sql.Tx, func(error), error) {
	acquired := databaseTimer(ctx, "connection", operation)
	conn, err := database.Conn(ctx)
	acquired(err)
	if err != nil {
		return nil, func(error) {}, err
	}
	worked := databaseTimer(ctx, "transaction", operation)
	tx, err := conn.BeginTx(ctx, options)
	if err != nil {
		_ = conn.Close()
		worked(err)
		return nil, func(error) {}, err
	}
	var once sync.Once
	finish := func(result error) {
		once.Do(func() {
			_ = tx.Rollback()
			_ = conn.Close()
			worked(result)
		})
	}
	return tx, finish, nil
}
