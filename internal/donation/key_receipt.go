package donation

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func replayManagedKeyReceipt(ctx context.Context, tx *sql.Tx, decision idempotency.Decision, scope charityscope.Scope, value ManagedKeyReceipt) (roleDonationMutation, error) {
	value.Key.EndpointKeyID = nil
	if scope.Trainee && len(value.Key.VisibleModels) > 0 {
		// Preserve the recorded binding snapshot, omitting targets no longer visible.
		visible := value.Key.VisibleModels[:0]
		for _, model := range value.Key.VisibleModels {
			var allowed bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM charity_models WHERE id=? AND is_mainstream=1)`, model.ModelID).Scan(&allowed); err != nil {
				return roleDonationMutation{}, err
			}
			if allowed {
				visible = append(visible, model)
			}
		}
		value.Key.VisibleModels = visible
	}
	body, err := json.Marshal(value)
	if err != nil {
		return roleDonationMutation{}, ErrInvariant
	}
	return roleDonationMutation{trainee: &value, status: decision.HTTPStatus, body: body, replayed: true}, nil
}
