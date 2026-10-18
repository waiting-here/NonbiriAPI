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
	case "session", "catalog", "personal_models", "charity_models", "charity_capability", "fishing_cleanup", "donation_expiry",
		"lake_checkpoint", "lake_mutation", "lake_profile", "lake_recovery", "lake_retention",
		"claim_accept", "claim_prepare", "claim_release", "claim_attempt", "claim_complete",
		"fishing_settle", "reward_delivery", "activities_projection", "activities_publish",
		"lifecycle_recovery", "lifecycle_retention":
		return true
	default:
		return false
	}
}

// DatabasePhase measures a fixed subphase within an operation. Subphases may
// overlap their parent transaction and must not be added to transaction time.
func DatabasePhase(ctx context.Context, operation, phase string) func(error) {
	switch phase {
	case "prepare", "authorize", "config", "read", "rules", "encode", "write", "commit", "projection", "idempotency", "activity":
		return databaseTimer(ctx, "phase."+phase, operation)
	default:
		return func(error) {}
	}
}

// DatabaseOperation measures a whole operation, including any pool waits and
// multiple transactions. It is distinct from connection-holding time.
func DatabaseOperation(ctx context.Context, operation string) func(error) {
	return databaseTimer(ctx, "operation", operation)
}

func databaseTimer(ctx context.Context, phase, operation string) func(error) {
	if ctx == nil {
		return func(error) {}
	}
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
