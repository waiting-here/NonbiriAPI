package main

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

// The session runtime also serves resource mutations; translate its domain
// errors at composition so activity transactions retain their own contract.
type activityUserAuthorizer struct{ runtime *auth.Runtime }

func (a activityUserAuthorizer) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, user int64) error {
	err := a.runtime.AuthorizeUserMutation(ctx, tx, user)
	switch {
	case errors.Is(err, resources.ErrUnauthorized):
		return activities.ErrUnauthorized
	case errors.Is(err, resources.ErrForbidden):
		return activities.ErrForbidden
	case errors.Is(err, resources.ErrNotFound):
		return activities.ErrNotFound
	default:
		return err
	}
}
