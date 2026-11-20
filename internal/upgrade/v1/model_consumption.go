package v1

import (
	"context"
	"database/sql"
)

// Retention can remove terminal receipts. Attribute only surviving receipts
// with an exact model and retain all established token counters in place.
func backfillModelConsumption(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "UPDATE donation_model_quotas SET historical_costs_partial=1"); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `WITH amounts AS MATERIALIZED (
 SELECT donation_key_id,upstream_model_id,
 nbi_u128_sum(nbi_u128(CASE WHEN state='committed' THEN calls_actual ELSE 0 END)) AS calls_used,
 nbi_u128_sum(nbi_u128(CASE WHEN state='committed' THEN price_actual_milli ELSE 0 END)) AS price_used,
 nbi_u128_sum(nbi_u128(CASE WHEN state='reserved' THEN calls_reserved ELSE 0 END)) AS calls_reserved,
 nbi_u128_sum(nbi_u128(CASE WHEN state='reserved' THEN price_reserved_milli ELSE 0 END)) AS price_reserved
 FROM donation_usage_reservations WHERE donation_key_id IS NOT NULL AND upstream_model_id<>'' AND state IN ('reserved','committed')
 GROUP BY donation_key_id,upstream_model_id)
 UPDATE donation_model_quotas AS q SET calls_used=a.calls_used,price_used_mag=a.price_used,calls_reserved=a.calls_reserved,price_reserved_mag=a.price_reserved
 FROM amounts a WHERE a.donation_key_id=q.donation_key_id AND a.upstream_model_id=q.upstream_model_id`)
	return err
}
