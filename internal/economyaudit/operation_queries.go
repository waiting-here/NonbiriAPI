package economyaudit

import (
	"context"
	"database/sql"
)

const operationProbeSize = 256

// The common recent-page path reads a fixed number of operations for this
// asset. Sparse filters then use a time/kind range instead of walking the rest
// of the ledger in sequence order.
func operationProbeQuery(f Filter, head int64) (string, []any) {
	query := `WITH recent AS MATERIALIZED (
 SELECT operation_id,ledger_seq FROM credit_operation_assets
 WHERE asset_type=? AND ledger_seq<=? ORDER BY ledger_seq DESC LIMIT 256
)
SELECT o.id,o.ledger_seq,o.kind,o.source_type,o.source_id,o.created_at,
(o.created_at>=? AND o.created_at<?`
	args := []any{string(f.Asset), head, f.From, f.To}
	if f.Kind != "" {
		query += ` AND o.kind=?`
		args = append(args, f.Kind)
	}
	query += operationChannelCondition(f, "o.source_id") + `)
FROM recent p JOIN credit_operations o ON o.id=p.operation_id
ORDER BY p.ledger_seq DESC`
	return query, args
}

func readOperationPage(ctx context.Context, tx *sql.Tx, f Filter, head, before int64) ([]operation, error) {
	if before > 0 {
		head = min(head, before-1)
	}
	if head <= 0 || f.From >= f.To {
		return nil, nil
	}
	page, count, last, err := readOperationProbe(ctx, tx, f, head)
	if err != nil {
		return nil, err
	}
	if len(page) == 101 || count < operationProbeSize {
		return page, nil
	}
	query, args := operationRangePageQuery(f, last-1, 101-len(page))
	tail, err := queryOperations(ctx, tx, query, args...)
	if err != nil {
		return nil, err
	}
	return append(page, tail...), nil
}

func readOperationProbe(ctx context.Context, tx *sql.Tx, f Filter, head int64) (page []operation, count int, last int64, err error) {
	query, args := operationProbeQuery(f, head)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	page = make([]operation, 0, 101)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, 0, 0, err
		}
		var o operation
		var matches bool
		if err := rows.Scan(&o.id, &o.seq, &o.kind, &o.source, &o.sourceID, &o.at, &matches); err != nil {
			return nil, 0, 0, err
		}
		count++
		last = o.seq
		if matches {
			page = append(page, o)
			if len(page) == 101 {
				return page, count, last, rows.Close()
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}
	return page, count, last, rows.Close()
}

func operationRangePageQuery(f Filter, head int64, limit int) (string, []any) {
	// Materialize narrow identifiers from the selected asset/time/kind range.
	// Keeping sequence ordering outside this range prevents LIMIT from making
	// the planner scan unrelated history to find sparse matches. Materializing
	// the result page also completes that work before QueryContext returns its
	// first row, while the driver's CPU-query cancellation is still active.
	query := `WITH candidates AS MATERIALIZED (
 SELECT operation_id,ledger_seq FROM credit_operation_assets
 WHERE asset_type=? AND created_at>=? AND created_at<?`
	args := []any{string(f.Asset), f.From, f.To}
	if f.Kind != "" {
		query += ` AND kind=?`
		args = append(args, f.Kind)
	}
	query += `
), page AS MATERIALIZED (
 SELECT o.id,o.ledger_seq,o.kind,o.source_type,o.source_id,o.created_at
 FROM candidates p CROSS JOIN credit_operations o ON o.id=p.operation_id
 WHERE p.ledger_seq<=?`
	args = append(args, head)
	query += operationChannelCondition(f, "o.source_id")
	query += ` ORDER BY p.ledger_seq DESC LIMIT ?
)
SELECT id,ledger_seq,kind,source_type,source_id,created_at FROM page ORDER BY ledger_seq DESC`
	return query, append(args, limit)
}

func queryOperations(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]operation, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	batch := []operation{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var o operation
		if err := rows.Scan(&o.id, &o.seq, &o.kind, &o.source, &o.sourceID, &o.at); err != nil {
			return nil, err
		}
		batch = append(batch, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return batch, rows.Close()
}
