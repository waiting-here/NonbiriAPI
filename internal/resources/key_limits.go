package resources

import (
	"context"
	"database/sql"
	"fmt"
)

func validKeyLimit(value int64) bool { return value >= 0 && value <= 2147483647 }

func writeKeyLimitsTx(ctx context.Context, tx *sql.Tx, keyID, concurrency, rpm int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,max_rpm)
VALUES(?,?,?) ON CONFLICT(endpoint_key_id) DO UPDATE SET max_concurrency=excluded.max_concurrency,max_rpm=excluded.max_rpm`, keyID, concurrency, rpm)
	if err != nil {
		return fmt.Errorf("resources: write endpoint key limits: %w", err)
	}
	return nil
}
