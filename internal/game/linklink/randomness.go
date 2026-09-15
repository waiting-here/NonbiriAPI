package linklink

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/game/randomness"
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
	if !found || v.ID != id {
		return resources.ErrMaintenance
	}
	if err := s.authorizeMaintenanceContinuation(ctx, tx, v, p.SessionBinding, ActionRead, now); err != nil {
		return resources.ErrMaintenance
	}
	return nil
}

func (service *Service) randomSource(ctx context.Context, tx *sql.Tx, id, label string) (IntSource, func() error, error) {
	proof, err := randomness.Load(ctx, tx, "linklink", id)
	if err != nil {
		return nil, nil, err
	}
	source := service.random
	if proof != nil {
		source, err = proof.Stream(label)
		if err != nil {
			return nil, nil, err
		}
	}
	return source, func() error { return randomness.Save(ctx, tx, proof) }, nil
}
