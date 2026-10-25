package donation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
)

type handlingActor struct {
	id                   int64
	hashKind, storedRole string
}

// Handling is a separate capability; it does not grant donation review or model access.
func (s *Service) beginHandlingTx(ctx context.Context, role reviewerRole, userID int64, readOnly bool) (*sql.Tx, handlingActor, error) {
	actor := handlingActor{id: userID, hashKind: string(role), storedRole: string(role)}
	if role == reviewerAdmin {
		tx, id, err := s.beginRoleTx(ctx, role, userID)
		actor.id = id
		return tx, actor, err
	}
	if s == nil || s.db == nil || ctx == nil || nilDependency(s.roleAuth) {
		return nil, actor, ErrUnavailable
	}
	if role != reviewerSteward || userID <= 0 {
		return nil, actor, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
	if err != nil {
		return nil, actor, err
	}
	rollback := func(err error) (*sql.Tx, handlingActor, error) {
		tx.Rollback()
		return nil, actor, err
	}
	var level, isAdmin int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(level,auto_level),is_admin FROM users WHERE id=?`, userID).Scan(&level, &isAdmin); errors.Is(err, sql.ErrNoRows) {
		return rollback(ErrUnauthorized)
	} else if err != nil {
		return rollback(fmt.Errorf("donation: read handling actor: %w", err))
	}
	if isAdmin != 0 || level != 5 && level != 6 {
		return rollback(ErrForbidden)
	}
	if level == 5 {
		authority, ok := s.roleAuth.(interface {
			AuthorizeTraineeMutation(context.Context, *sql.Tx, int64) error
		})
		if !ok {
			return rollback(ErrForbidden)
		}
		if err := authority.AuthorizeTraineeMutation(ctx, tx, userID); err != nil {
			return rollback(mapAuthorization(err))
		}
		actor.hashKind, actor.storedRole = "trainee5", "level5"
	} else if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, userID); err != nil {
		return rollback(mapAuthorization(err))
	}
	if err := authz.RequireStewardPermission(ctx, tx, userID, "donations.handle"); err != nil {
		return rollback(err)
	}
	if level == 5 {
		p, err := authz.ReadStewardPermissions(ctx, tx, userID)
		if err != nil {
			return rollback(err)
		}
		if p.DonationScope == authz.DonationScopeNone {
			return rollback(authz.ErrPermissionDenied)
		}
	}
	if _, err := charityscope.SessionIdentity(ctx, s.roleAuth, userID); err != nil {
		return rollback(mapAuthorization(err))
	}
	return tx, actor, nil
}
