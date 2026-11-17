package observability

import (
	"context"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

// RecordDelivery attaches facts only to an already authorized request-log root.
// No root is created if the public request was not retained by the existing rail.
func (r *Repository) RecordDelivery(ctx context.Context, requestID string, value diagnostic.Delivery) error {
	route := requestattempt.RouteSnapshot(ctx)
	payload := struct {
		diagnostic.Delivery
		Route *requestattempt.RouteDecision `json:"route_decision,omitempty"`
	}{value, route}
	encoded, err := json.Marshal(payload)
	if len(encoded) > 16<<10 && route != nil {
		payload.Route = route.Essential()
		encoded, err = json.Marshal(payload)
	}
	if len(encoded) > 16<<10 && route != nil {
		encoded, err = json.Marshal(value)
	}
	if err != nil || len(encoded) > 16<<10 {
		return ErrInvalid
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO request_delivery_facts(request_log_id,details)
SELECT id,? FROM request_logs WHERE logical_request_id=? ON CONFLICT(request_log_id) DO UPDATE SET details=excluded.details`, string(encoded), requestID)
	return err
}
