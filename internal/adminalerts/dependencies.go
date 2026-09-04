package adminalerts

import (
	"context"
	"database/sql"
	"net/http"
)

// CursorKeyDeriver supplies the canonical Generation 2 pagination key.
type CursorKeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

// AdminFinalAuthorizer revalidates the live administrator session, credential
// generation, and role in the transaction that performs the alert read or
// state change.
type AdminFinalAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
}

type AdminPrincipal struct {
	UserID int64
}

type AuthorizedAdminHandler func(http.ResponseWriter, *http.Request, AdminPrincipal)

// AdminRouteRegistrar owns the administrator host, password-session boundary,
// and entry credential-generation check. Administrator routes intentionally do
// not pass through the user maintenance gate.
type AdminRouteRegistrar interface {
	RegisterAdminRoute(method, pattern string, handler AuthorizedAdminHandler) error
}
