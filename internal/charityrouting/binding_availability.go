package charityrouting

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donationstate"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
)

// Availability is a read-time snapshot, independent of whether a connection
// can be configured. A temporarily paused service remains editable.
type BindingAvailability struct {
	State           string                        `json:"state"`
	ServiceStatuses []donationstate.ServiceStatus `json:"service_statuses"`
}

type KeyModelTarget struct {
	TargetID        string              `json:"target_id"`
	UpstreamModelID string              `json:"upstream_model_id"`
	Availability    BindingAvailability `json:"availability"`
}

type bindingAvailabilityRow struct {
	KeyModelTarget
	bindingID  string
	keyID      int64
	mask       int64
	modelTypes modeltype.Set
}

// The selected bindings have already passed the caller's management scope.
// Read all their targets in one snapshot; do not add these queries to write
// receipts, whose contents must remain stable for idempotent replay.
func readBindingAvailabilityTx(ctx context.Context, tx *sql.Tx, modelID, keyID, now int64, bindingIDs []string) ([]bindingAvailabilityRow, error) {
	out := []bindingAvailabilityRow{}
	if len(bindingIDs) == 0 {
		return out, nil
	}
	if len(bindingIDs) > maxBindingBatch {
		return nil, ErrInvariant
	}
	args := []any{now, modelID, keyID, keyID}
	placeholders := make([]string, len(bindingIDs))
	for i, id := range bindingIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := `SELECT rb.id,t.id,t.upstream_model_id,dk.id,t.native_service_mask,cm.model_types,
CASE WHEN cx.charity_enabled=0 THEN 'feature_disabled'
WHEN cm.enabled=0 THEN 'model_disabled'
WHEN ` + catalogAvailableBindingSQL("rb.id", "t.donation_key_id", "t.id") + ` THEN 'available'
ELSE CASE WHEN (` + donationstate.SQLAt("cx.decision_now") + `)='available' THEN 'unavailable'
ELSE ` + donationstate.SQLAt("cx.decision_now") + ` END END
FROM charity_models cm JOIN charity_model_bindings rb ON rb.charity_model_id=cm.id
JOIN charity_binding_targets t ON t.binding_id=rb.id
JOIN donation_keys dk ON dk.id=t.donation_key_id
JOIN donations d ON d.id=dk.donation_id
LEFT JOIN endpoint_keys k ON k.id=dk.endpoint_key_id
LEFT JOIN endpoints e ON e.id=k.endpoint_id
CROSS JOIN (SELECT ? AS decision_now,
COALESCE((SELECT value FROM site_config WHERE key='charity_token_reserve_milli'),'0') AS token_reserve,
COALESCE((SELECT value FROM site_config WHERE key='charity_enabled'),'0')='1' AS charity_enabled) cx
WHERE cm.id=? AND (?=0 OR dk.id=?) AND rb.id IN (` + strings.Join(placeholders, ",") + `)
ORDER BY rb.ord,rb.id,t.ord,t.id`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []int64{}
	for rows.Next() {
		var row bindingAvailabilityRow
		var bindingID, targetID int64
		if err := rows.Scan(&bindingID, &targetID, &row.UpstreamModelID, &row.keyID, &row.mask, &row.modelTypes, &row.Availability.State); err != nil {
			return nil, err
		}
		row.bindingID, row.TargetID = strconv.FormatInt(bindingID, 10), strconv.FormatInt(targetID, 10)
		row.Availability.ServiceStatuses = []donationstate.ServiceStatus{}
		out = append(out, row)
		keys = append(keys, row.keyID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	statuses, err := donationstate.LoadServiceStatuses(ctx, tx, keys, now)
	if err != nil {
		return nil, err
	}
	for i := range out {
		row := &out[i]
		selected, err := contract.NativeServicesFromMask(row.mask)
		if err != nil {
			return nil, ErrInvariant
		}
		for _, status := range statuses[row.keyID] {
			if !selected.Has(status.NativeService) {
				continue
			}
			services, _ := contract.ParseNativeServices([]contract.NativeService{status.NativeService})
			for _, op := range row.modelTypes {
				if routes, err := connector.ResolveServiceRoutes(status.ConfiguredType, services, status.RerankFormat, op); err == nil && len(routes) > 0 {
					row.Availability.ServiceStatuses = append(row.Availability.ServiceStatuses, status)
					break
				}
			}
		}
		if row.Availability.State == "rate_limited" {
			canRecover := false
			for _, status := range row.Availability.ServiceStatuses {
				canRecover = canRecover || status.State == "available" || status.State == "rate_limited"
			}
			// Key state includes every approved service. An unrelated service's
			// cooldown must not suggest waiting will fix this target's lost grant
			// or manual pause.
			if !canRecover {
				row.Availability.State = "unavailable"
			}
		}
	}
	return out, nil
}

func annotateBindingAvailabilityTx(ctx context.Context, tx *sql.Tx, modelID, now int64, value *AdminBindings) error {
	ids := make([]string, 0, len(value.Bindings))
	for _, binding := range value.Bindings {
		ids = append(ids, binding.ID)
	}
	rows, err := readBindingAvailabilityTx(ctx, tx, modelID, 0, now, ids)
	if err != nil {
		return err
	}
	byTarget := make(map[string]BindingAvailability, len(rows))
	for _, row := range rows {
		byTarget[row.TargetID] = row.Availability
	}
	for i := range value.Bindings {
		for j := range value.Bindings[i].Targets {
			target := &value.Bindings[i].Targets[j]
			if availability, ok := byTarget[target.ID]; ok {
				target.Availability = &availability
			}
		}
	}
	return nil
}
