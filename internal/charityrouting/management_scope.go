package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func managementScopeError(err error) error {
	if errors.Is(err, charityscope.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, charityscope.ErrInvalid) {
		return ErrInvalidRequest
	}
	return mapAuthorization(err)
}

func (s *Service) managementScope(ctx context.Context, tx *sql.Tx, role roleKind, actorID, modelID int64, listing bool) (charityscope.Scope, error) {
	if role != roleAdmin && role != roleSteward {
		return charityscope.Scope{}, ErrInvalidRequest
	}
	scope, err := charityscope.Authorize(ctx, tx, s.roleAuth, role == roleAdmin, actorID, modelID, listing)
	return scope, managementScopeError(err)
}

func (s *Service) beginManagementTx(ctx context.Context, role roleKind, actorID, modelID int64, listing, readOnly bool) (*sql.Tx, charityscope.Scope, error) {
	if s == nil || s.db == nil || nilDependency(s.roleAuth) {
		return nil, charityscope.Scope{}, ErrUnavailable
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
	if err != nil {
		return nil, charityscope.Scope{}, err
	}
	scope, err := s.managementScope(ctx, tx, role, actorID, modelID, listing)
	if err != nil {
		tx.Rollback()
		return nil, charityscope.Scope{}, err
	}
	return tx, scope, nil
}

func replayScopedBindings(ctx context.Context, tx *sql.Tx, decision idempotency.Decision, scope charityscope.Scope, now int64) (resources.MutationResult[AdminBindings], error) {
	result, err := replay[AdminBindings](decision)
	if err != nil || !scope.Trainee {
		return result, err
	}
	for _, binding := range result.Value.Bindings {
		donationID, err := strconv.ParseInt(binding.DonationID, 10, 64)
		if err != nil {
			return resources.MutationResult[AdminBindings]{}, ErrInvariant
		}
		keyID, err := strconv.ParseInt(binding.DonationKeyID, 10, 64)
		if err != nil {
			return resources.MutationResult[AdminBindings]{}, ErrInvariant
		}
		if err := scope.RequireKey(ctx, tx, donationID, keyID, now, false); err != nil {
			return resources.MutationResult[AdminBindings]{}, managementScopeError(err)
		}
	}
	return result, nil
}

func (s *Service) managementCursorOwner(ctx context.Context, role roleKind, actorID, modelID int64, suffix string) (string, error) {
	tx, scope, err := s.beginManagementTx(ctx, role, actorID, modelID, modelID == 0, true)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	owner, err := scope.CursorOwner(ctx, s.roleAuth, suffix)
	if err != nil {
		return "", managementScopeError(err)
	}
	return owner, tx.Commit()
}
