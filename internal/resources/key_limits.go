package resources

import (
	"context"
	"database/sql"
	"fmt"
)

func validKeyLimit(value int64) bool     { return value >= 0 && value <= 2147483647 }
func validKeyOverflow(value string) bool { return value == "reject" || value == "queue" }
func keyOverflowDefault(value string) string {
	if value == "" {
		return "reject"
	}
	return value
}
func validPatchKeyQueue(input PatchEndpointKeyInput) bool {
	for _, value := range []*string{input.ConcurrencyOverflow, input.RPMOverflow} {
		if value != nil && !validKeyOverflow(*value) {
			return false
		}
	}
	for _, value := range []*int64{input.ConcurrencyQueueLimit, input.RPMQueueLimit} {
		if value != nil && !validKeyLimit(*value) {
			return false
		}
	}
	return true
}

func writeKeyLimitsTx(ctx context.Context, tx *sql.Tx, keyID, concurrency, rpm int64, concurrencyOverflow, rpmOverflow string, concurrencyQueueLimit, rpmQueueLimit int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,max_rpm,concurrency_overflow,rpm_overflow,concurrency_queue_limit,rpm_queue_limit)
VALUES(?,?,?,?,?,?,?) ON CONFLICT(endpoint_key_id) DO UPDATE SET max_concurrency=excluded.max_concurrency,max_rpm=excluded.max_rpm,
concurrency_overflow=excluded.concurrency_overflow,rpm_overflow=excluded.rpm_overflow,
concurrency_queue_limit=excluded.concurrency_queue_limit,rpm_queue_limit=excluded.rpm_queue_limit`, keyID, concurrency, rpm, concurrencyOverflow, rpmOverflow, concurrencyQueueLimit, rpmQueueLimit)
	if err != nil {
		return fmt.Errorf("resources: write endpoint key limits: %w", err)
	}
	return nil
}
