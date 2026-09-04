package adapters

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

// AuthDeleteAdapter owns exactly the first account-deletion slot. Its
// finalizer remains under coordinator control until every later slot and the
// shared transaction have succeeded.
type AuthDeleteAdapter struct {
	identity identityLifecycle
}

func NewAuthDeleteAdapter(runtime *auth.Runtime) (*AuthDeleteAdapter, error) {
	if runtime == nil {
		return nil, lifecycle.ErrInvalid
	}
	return &AuthDeleteAdapter{identity: runtime}, nil
}

func (adapter *AuthDeleteAdapter) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
	if adapter == nil || adapter.identity == nil || !validDeleteCall(ctx, tx, request) {
		return nil, lifecycle.ErrInvalid
	}
	finalizer, err := adapter.identity.PrepareLifecycleAccountDeletion(
		ctx, tx, request.UserID, request.DecisionNow,
	)
	if err != nil {
		return nil, translateError(err)
	}
	return finalizer, nil
}

// ResourceDeleteAdapter owns exactly the second account-deletion slot.
type ResourceDeleteAdapter struct {
	resources resourceLifecycle
}

func NewResourceDeleteAdapter(repository *resources.Repository) (*ResourceDeleteAdapter, error) {
	if repository == nil {
		return nil, lifecycle.ErrInvalid
	}
	return &ResourceDeleteAdapter{resources: repository}, nil
}

func (adapter *ResourceDeleteAdapter) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
	if adapter == nil || adapter.resources == nil || !validDeleteCall(ctx, tx, request) {
		return nil, lifecycle.ErrInvalid
	}
	if err := adapter.resources.PrepareLifecycleAccountDeletion(
		ctx, tx, request.UserID, request.DecisionNow,
	); err != nil {
		return nil, translateError(err)
	}
	return nil, nil
}

var (
	_ lifecycle.DeleteAdapter = (*AuthDeleteAdapter)(nil)
	_ lifecycle.DeleteAdapter = (*ResourceDeleteAdapter)(nil)
)
