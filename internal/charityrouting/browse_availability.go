package charityrouting

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/rolepolicy"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

// callerAvailabilityTx evaluates all selected models in the caller's existing
// read snapshot. It never advances donation lifecycle state. Expiry and quota
// windows use the same decision time as the rest of the response.
func callerAvailabilityTx(ctx context.Context, tx *sql.Tx, userID int64, level int, now int64, ids []int64) (map[int64]bool, error) {
	available := make(map[int64]bool, len(ids))
	if len(ids) == 0 {
		return available, nil
	}
	args := []any{now, userID, level}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, `SELECT cm.id,cm.transport_rule,cm.role_policy,cm.pricing_mode,
COALESCE((SELECT amount_milli FROM charity_model_token_reserves WHERE model_id=cm.id),cx.token_reserve),`+availableCandidateCountSQL("", true)+`
FROM charity_models cm CROSS JOIN (SELECT ? AS decision_now,1 AS charity_enabled,? AS caller_id,? AS caller_level,
(SELECT value FROM site_config WHERE key='charity_token_reserve_milli') AS token_reserve) cx
WHERE cm.id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("charity routing: read model availability: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var count int
		var transport transportpolicy.Rule
		var policy, pricing string
		var reserve sql.NullString
		if err := rows.Scan(&id, &transport, &policy, &pricing, &reserve, &count); err != nil {
			return nil, fmt.Errorf("charity routing: scan model availability: %w", err)
		}
		if !transport.Valid() {
			return nil, ErrInvariant
		}
		if _, err := rolepolicy.Decode(policy); err != nil {
			return nil, ErrInvariant
		}
		if pricing == "per_token" || pricing == "per_search_unit" {
			value, err := strconv.ParseInt(reserve.String, 10, 64)
			if !reserve.Valid || err != nil || value < 1 || value > db.MaxMoneyMilli {
				return nil, ErrInvariant
			}
		} else if pricing != "per_request" {
			return nil, ErrInvariant
		}
		if count > MaxRuntimeCandidates {
			return nil, ErrResourceLimit
		}
		available[id] = count > 0
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("charity routing: iterate model availability: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("charity routing: close model availability: %w", err)
	}
	return available, nil
}
