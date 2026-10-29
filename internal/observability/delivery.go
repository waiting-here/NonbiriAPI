package observability

import (
	"context"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

// RecordDelivery attaches facts only to an already authorized request-log root.
// No root is created if the public request was not retained by the existing rail.
func (r *Repository) RecordDelivery(ctx context.Context, requestID string, value diagnostic.Delivery) error {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > 16<<10 {
		return ErrInvalid
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO request_delivery_facts(request_log_id,details)
SELECT id,? FROM request_logs WHERE logical_request_id=? ON CONFLICT(request_log_id) DO UPDATE SET details=excluded.details`, string(encoded), requestID)
	return err
}
