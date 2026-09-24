package claim

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

func (s *Service) recordObservationsTx(ctx context.Context, tx *sql.Tx, request Request, result ResultClass, dispatched bool, at int64) error {
	if s.observations == nil {
		return nil
	}
	if err := observability.FinalizeRequestTx(ctx, tx, request.ID, at); err != nil {
		return err
	}
	if !request.Route.IsCharity() {
		return nil
	}
	var model sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT charity_model_id FROM charity_reservations WHERE logical_request_id=?`, request.ID).Scan(&model); err != nil {
		return err
	}
	if !model.Valid {
		return nil
	}
	value := "failure"
	if result == ResultSuccess {
		value = "success"
	} else if result == ResultCancelled {
		value = "cancelled"
	}
	return s.observations.RecordOutcomeTx(ctx, tx, request.ID, model.Int64, value, dispatched, at)
}
