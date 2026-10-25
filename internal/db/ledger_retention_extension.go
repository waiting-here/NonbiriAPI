package db

import (
	"context"
	"database/sql"
)

// LedgerCompactionState requires the current schema established at startup.
func LedgerCompactionState(ctx context.Context, tx *sql.Tx) (through, before int64, present bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT through_seq,details_before FROM credit_compaction WHERE id=1`).Scan(&through, &before)
	present = err == nil
	return
}

// Historical storage validation runs before the retention migration exists.
func historicalLedgerCompactionState(ctx context.Context, tx *sql.Tx) (through, before int64, present bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='credit_compaction')`).Scan(&present)
	if err != nil || !present {
		return
	}
	return LedgerCompactionState(ctx, tx)
}
