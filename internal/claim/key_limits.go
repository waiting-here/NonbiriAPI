package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrKeyRateLimited means no attempt was admitted. Callers may try another
// binding without treating this as an upstream failure.
var ErrKeyRateLimited = errors.New("claim: endpoint key rate limited")

// Pending claims reserve both limits. Dispatch transfers the RPM slot into
// the rolling window; completion frees concurrency and keeps the dispatch time.
func checkKeyLimitsTx(ctx context.Context, tx *sql.Tx, keyID, at int64, purpose Purpose) error {
	if purpose == PurposeDiscovery {
		return nil
	}
	var concurrency, rpm int64
	err := tx.QueryRowContext(ctx, `SELECT max_concurrency,max_rpm FROM endpoint_key_limits WHERE endpoint_key_id=?`, keyID).Scan(&concurrency, &rpm)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim: read endpoint key limits: %w", err)
	}
	if concurrency == 0 && rpm == 0 {
		return nil
	}
	var active, pending int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(state='claimed'),0)
FROM dispatch_claims WHERE endpoint_key_id=? AND purpose<>'discovery' AND state IN ('claimed','dispatched')`, keyID).Scan(&active, &pending); err != nil {
		return fmt.Errorf("claim: count endpoint key reservations: %w", err)
	}
	if concurrency > 0 && active >= concurrency {
		return ErrKeyRateLimited
	}
	if rpm > 0 {
		var recent int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM dispatch_claims
WHERE endpoint_key_id=? AND purpose<>'discovery' AND dispatched_at IS NOT NULL AND dispatched_at>?`, keyID, at-60).Scan(&recent); err != nil {
			return fmt.Errorf("claim: count endpoint key dispatches: %w", err)
		}
		if pending >= rpm || recent >= rpm-pending {
			return ErrKeyRateLimited
		}
	}
	return nil
}
