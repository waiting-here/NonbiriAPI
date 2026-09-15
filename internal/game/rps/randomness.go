package rps

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (s *Service) authorizeRandomness(ctx context.Context, tx *sql.Tx, p resources.ContinuationUserPrincipal, id string, now int64) error {
	on, err := maintenanceEnabled(ctx, tx)
	if err != nil || !on {
		return err
	}
	v, found, err := loadSessionByUser(ctx, tx, p.UserID)
	if err != nil {
		return err
	}
	if found && v.ID == id {
		err = s.authorizeMaintenanceContinuation(ctx, tx, v, p.UserID, p.SessionBinding, ActionRead, now)
	} else {
		err = s.authorizeMaintenancePending(ctx, tx, p.UserID, id, p.SessionBinding, ActionRead, now)
	}
	if err != nil {
		return resources.ErrMaintenance
	}
	return nil
}
