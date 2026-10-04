package db

import (
	"context"
	"database/sql"
)

// LedgerCompactionState also supports the historical schemas validated within
// an upgrade, before the retention extension has been applied.
func LedgerCompactionState(ctx context.Context, tx *sql.Tx) (through, before int64, present bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='credit_compaction')`).Scan(&present)
	if err != nil || !present {
		return
	}
	err = tx.QueryRowContext(ctx, `SELECT through_seq,details_before FROM credit_compaction WHERE id=1`).Scan(&through, &before)
	return
}
