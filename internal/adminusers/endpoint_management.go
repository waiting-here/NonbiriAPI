package adminusers

import (
	"context"
	"database/sql"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func (s *Service) beginEndpointManagement(ctx context.Context, actorID int64, role managementRole, readOnly bool, permission string) (*sql.Tx, error) {
	if role == roleSteward && ctx != nil && authz.StewardPermissionFromContext(ctx) == "" {
		ctx = authz.WithStewardPermission(ctx, permission)
	}
	tx, err := s.beginManagement(ctx, actorID, role, readOnly)
	if err != nil {
		return nil, err
	}
	if role == roleSteward {
		if err := authz.RequireStewardPermission(ctx, tx, actorID, permission); err != nil {
			tx.Rollback()
			return nil, classifyAuthorizationError(err)
		}
	}
	return tx, nil
}
