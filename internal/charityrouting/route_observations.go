package charityrouting

import (
	"context"
	"database/sql"
	"strings"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

// A target missing from the eligibility query may still be configured. Read
// just its identifiers, service mask and key-wide failure flag in the same
// transaction. Diagnostic collection never changes an admission result.
func observeUnlistedTargets(ctx context.Context, tx *sql.Tx, modelID int64, listed []int64) {
	// The eligibility query already accounted for listed targets. Look only
	// at other bindings of this model, never global donations or past alerts.
	if len(listed) >= maxBindingBatch+1 {
		requestattempt.TruncateRoute(ctx)
		return
	}
	args := []any{modelID}
	exclude := ""
	if len(listed) > 0 {
		exclude = " AND t.id NOT IN (" + strings.TrimSuffix(strings.Repeat("?,", len(listed)), ",") + ")"
		for _, id := range listed {
			args = append(args, id)
		}
	}
	const limit = 8
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, `SELECT b.id,t.native_service_mask,dk.failure_disabled
FROM charity_model_bindings b JOIN charity_binding_targets t ON t.binding_id=b.id
JOIN donation_keys dk ON dk.id=t.donation_key_id
WHERE b.charity_model_id=?`+exclude+` ORDER BY b.ord,b.id,t.ord,t.id LIMIT ?`, args...)
	if err != nil {
		requestattempt.TruncateRoute(ctx)
		return
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		if seen == limit {
			requestattempt.TruncateRoute(ctx)
			return
		}
		var bindingID, mask int64
		var disabled bool
		if err := rows.Scan(&bindingID, &mask, &disabled); err != nil {
			requestattempt.TruncateRoute(ctx)
			return
		}
		seen++
		services, err := connectorcontract.NativeServicesFromMask(mask)
		if err != nil {
			continue
		}
		reason := "unavailable"
		if disabled {
			reason = "failure_disabled"
		}
		observeBinding(ctx, bindingID, services, reason)
	}
	if rows.Err() != nil {
		requestattempt.TruncateRoute(ctx)
		return
	}
	if seen == 0 && len(listed) == 0 {
		requestattempt.ObserveRoute(ctx, 0, "", "no_bindings", "", "")
	}
}

func observeBinding(ctx context.Context, bindingID int64, services connectorcontract.NativeServiceSet, reason string) {
	if services.Empty() || requestattempt.CurrentID(ctx) == "" {
		return
	}
	for _, service := range services.Services() {
		requestattempt.ObserveRoute(ctx, bindingID, service, reason, "", "")
	}
}
