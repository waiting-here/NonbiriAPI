package observability

import (
	"context"
	"database/sql"
	"strconv"
)

type RawCapacity struct {
	BudgetBytes       int64 `json:"budget_bytes"`
	UsedBytes         int64 `json:"used_bytes"`
	CapacityOmissions int64 `json:"capacity_omissions"`
	Unavailable       int64 `json:"unavailable"`
}

// RawCapacityTx is a management-only projection of logical payload bytes. It
// deliberately does not present SQLite pages, indices or WAL as blob usage.
func RawCapacityTx(ctx context.Context, tx *sql.Tx) (RawCapacity, error) {
	var value RawCapacity
	var configured string
	err := tx.QueryRowContext(ctx, `SELECT (SELECT value FROM site_config WHERE key='request_error_body_budget_mib'),raw_body_bytes,raw_capacity_omissions,raw_unavailable FROM observability_state WHERE id=1`).Scan(&configured, &value.UsedBytes, &value.CapacityOmissions, &value.Unavailable)
	if err != nil {
		return value, err
	}
	budget, err := strconv.ParseInt(configured, 10, 64)
	if err != nil || budget < 1 || budget > 65536 {
		return value, ErrUnavailable
	}
	value.BudgetBytes = budget * (1 << 20)
	return value, nil
}
