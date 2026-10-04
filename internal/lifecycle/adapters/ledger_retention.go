package adapters

import (
	"context"
	"database/sql"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type LedgerRetention struct{ database *sql.DB }

func NewLedgerRetention(database *sql.DB) *LedgerRetention {
	return &LedgerRetention{database: database}
}

func (a *LedgerRetention) Retain(ctx context.Context, now int64, limit int, deadline time.Time) (lifecycle.WorkResult, error) {
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	result, err := ledger.RetainDetails(ctx, a.database, now, limit)
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, err
}
