package checkin

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

// FinalTxAuthorizer revalidates the request-bound user identity and current
// account authority in the exact transaction that decides the check-in.
type FinalTxAuthorizer interface {
	AuthorizeUserMutation(context.Context, *sql.Tx, int64) error
}

// MaintenanceAuthorizer reads the committed maintenance singleton through
// the caller-owned transaction. The existing maintenance service implements
// this seam; its historical method name does not widen this domain's powers.
type MaintenanceAuthorizer interface {
	AuthorizeChatAcceptance(context.Context, *sql.Tx, int64, int64) error
}

// Use the central authenticated user registrar directly. Root composition
// therefore needs no second identity type or request-context convention.
type UserPrincipal = resources.UserPrincipal
type AuthorizedUserHandler = resources.AuthorizedUserHandler
type UserRouteRegistrar = resources.UserRouteRegistrar
