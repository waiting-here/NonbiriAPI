package donationquota

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func validateModelReservations(ctx context.Context, tx *sql.Tx) error {
	var mismatch bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_usage_reservations u
 LEFT JOIN donation_model_quotas q ON q.donation_key_id=u.donation_key_id AND q.upstream_model_id=u.upstream_model_id
 LEFT JOIN dispatch_claim_targets t ON t.claim_id=u.claim_id
 WHERE u.state='reserved' AND u.donation_key_id IS NOT NULL AND u.upstream_model_id<>''
 AND (q.donation_key_id IS NULL OR (t.claim_id IS NOT NULL AND t.upstream_model_id<>u.upstream_model_id)))`).Scan(&mismatch); err != nil {
		return err
	}
	if mismatch {
		return fmt.Errorf("%w: model receipt identity", ErrInvariant)
	}
	rows, err := tx.QueryContext(ctx, `SELECT q.donation_key_id,q.upstream_model_id,q.tokens_reserved,q.input_tokens_reserved,q.output_tokens_reserved,
 u.claim_id,u.tokens_reserved,u.input_tokens_reserved,u.output_tokens_reserved
 FROM donation_model_quotas q LEFT JOIN donation_usage_reservations u
 ON u.donation_key_id=q.donation_key_id AND u.upstream_model_id=q.upstream_model_id AND u.state='reserved'
 ORDER BY q.donation_key_id,q.upstream_model_id,u.claim_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var key int64
	var model string
	var expected, sum [3]db.U128
	check := func() error {
		if key != 0 && expected != sum {
			return fmt.Errorf("%w: model token occupancy", ErrInvariant)
		}
		return nil
	}
	for rows.Next() {
		var id int64
		var name string
		var blobs [3][]byte
		var claim *string
		var total sql.NullInt64
		var input, output *int64
		if err = rows.Scan(&id, &name, &blobs[0], &blobs[1], &blobs[2], &claim, &total, &input, &output); err != nil {
			return err
		}
		if id != key || name != model {
			if err = check(); err != nil {
				return err
			}
			key, model = id, name
			sum = [3]db.U128{}
			for i := range 3 {
				if expected[i], err = magnitude(blobs[i]); err != nil {
					return err
				}
			}
		}
		if claim == nil {
			continue
		}
		vector := TokenVector{Total: total.Int64, Input: input, Output: output}
		if !total.Valid || !vector.Valid() {
			return ErrInvariant
		}
		values := modelValues(vector)
		for i := range 3 {
			if sum[i], err = add(sum[i], values[i]); err != nil {
				return err
			}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return check()
}

type ModelExport struct {
	ModelView
	RecurringLimits []RuleView `json:"recurring_limits"`
}

// ModelExports consumes the caller's remaining account-wide model budget and
// includes retained counters for models no longer present in discovery.
func ModelExports(ctx context.Context, q Reader, keyID, now int64, limit int) ([]ModelExport, error) {
	if limit < 0 || limit > 10000 {
		return nil, ErrInvalid
	}
	rows, err := q.QueryContext(ctx, `SELECT upstream_model_id FROM donation_model_quotas WHERE donation_key_id=?
 UNION SELECT upstream_model_id FROM donation_quota_rules WHERE donation_key_id=? AND upstream_model_id<>'' AND current_epoch IS NOT NULL
 ORDER BY upstream_model_id LIMIT ?`, keyID, keyID, limit+1)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(names) > limit {
		return nil, ErrCapacity
	}
	out := make([]ModelExport, 0, len(names))
	for _, name := range names {
		usage, err := ModelUsage(ctx, q, keyID, name)
		if err != nil {
			return nil, err
		}
		rules, err := ModelViews(ctx, q, keyID, now, name)
		if err != nil {
			return nil, err
		}
		out = append(out, ModelExport{usage, rules})
	}
	return out, nil
}
