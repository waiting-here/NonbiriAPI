package announcements

import (
	"context"
	"database/sql"
	"net/http"
)

type CursorKeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

// AdminFinalTxAuthorizer revalidates the live administrator session and role
// in the caller's final read or write transaction. It is invoked before any
// domain read, lazy expiry, or idempotency lookup, including replay.
type AdminFinalTxAuthorizer interface {
	AuthorizeAdminFinalTx(context.Context, *sql.Tx, int64) error
}

type UserPrincipal struct {
	UserID int64
}

type AdminPrincipal struct {
	UserID int64
}

type AuthorizedUserHandler func(http.ResponseWriter, *http.Request, UserPrincipal)
type AuthorizedAdminHandler func(http.ResponseWriter, *http.Request, AdminPrincipal)

// UserRouteRegistrar owns user-session authentication and the maintenance
// gate. Announcement handlers never duplicate either concern.
type UserRouteRegistrar interface {
	RegisterUserRoute(method, pattern string, handler AuthorizedUserHandler) error
}

// AdminRouteRegistrar owns administrator authentication. Admin announcement
// management deliberately remains available while user maintenance is on.
type AdminRouteRegistrar interface {
	RegisterAdminRoute(method, pattern string, handler AuthorizedAdminHandler) error
}
