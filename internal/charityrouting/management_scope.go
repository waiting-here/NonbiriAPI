package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
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
	if err != nil || !scope.RestrictedKeys() {
		return result, err
	}
	for _, binding := range result.Value.Bindings {
		targets := binding.Targets
		if len(targets) == 0 {
			targets = []BindingTarget{{DonationID: binding.DonationID, DonationKeyID: binding.DonationKeyID}}
		}
		for _, target := range targets {
			donationID, err := strconv.ParseInt(target.DonationID, 10, 64)
			if err != nil {
				return resources.MutationResult[AdminBindings]{}, ErrInvariant
			}
			keyID, err := strconv.ParseInt(target.DonationKeyID, 10, 64)
			if err != nil {
				return resources.MutationResult[AdminBindings]{}, ErrInvariant
			}
			if err := scope.RequireKey(ctx, tx, donationID, keyID, now, false); err != nil {
				return resources.MutationResult[AdminBindings]{}, managementScopeError(err)
			}
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

func requireBindingSourceScope(ctx context.Context, tx *sql.Tx, scope charityscope.Scope, modelID, bindingID, now int64) error {
	if !scope.RestrictedKeys() {
		return nil
	}
	if scope.DonationScope == "none" {
		return authz.ErrPermissionDenied
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.id,dk.id FROM charity_model_bindings b JOIN charity_binding_targets t ON t.binding_id=b.id JOIN donation_keys dk ON dk.id=t.donation_key_id JOIN donations d ON d.id=dk.donation_id WHERE b.charity_model_id=? AND (?=0 OR b.id=?)`, modelID, bindingID, bindingID)
	if err != nil {
		return err
	}
	var keys [][2]int64
	for rows.Next() {
		var item [2]int64
		if err := rows.Scan(&item[0], &item[1]); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := scope.RequireKey(ctx, tx, key[0], key[1], now, false); err != nil {
			return managementScopeError(err)
		}
	}
	return nil
}

func scopedBindingsTx(ctx context.Context, tx *sql.Tx, scope charityscope.Scope, value AdminBindings, now int64) (AdminBindings, error) {
	if !scope.RestrictedKeys() {
		return value, nil
	}
	if scope.DonationScope == "none" {
		return AdminBindings{}, authz.ErrPermissionDenied
	}
	visible := value.Bindings[:0]
	for _, binding := range value.Bindings {
		id, err := strconv.ParseInt(binding.ID, 10, 64)
		if err != nil {
			return AdminBindings{}, ErrInvariant
		}
		err = scope.RequireBinding(ctx, tx, id, now)
		if errors.Is(err, charityscope.ErrNotFound) {
			continue
		}
		if err != nil {
			return AdminBindings{}, managementScopeError(err)
		}
		visible = append(visible, binding)
	}
	value.Bindings = visible
	return value, nil
}
