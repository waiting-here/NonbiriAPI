package blackjack

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (s *Service) authorizeRandomness(ctx context.Context, tx *sql.Tx, p resources.ContinuationUserPrincipal, id string, now int64) error {
	on, err := maintenanceOn(ctx, tx)
	if err != nil || !on {
		return err
	}
	v, err := readSession(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := s.authorizeExisting(ctx, tx, v, Identity{UserID: p.UserID, SessionBinding: p.SessionBinding}, "read", now); err != nil {
		return resources.ErrMaintenance
	}
	return nil
}
