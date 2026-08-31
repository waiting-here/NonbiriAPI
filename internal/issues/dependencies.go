package issues

import (
	"net/http"
)

type CursorKeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

type UserPrincipal struct {
	UserID int64
}

type AuthorizedUserHandler func(http.ResponseWriter, *http.Request, UserPrincipal)

// UserRouteRegistrar owns user authentication and the maintenance gate.
type UserRouteRegistrar interface {
	RegisterUserRoute(method, pattern string, handler AuthorizedUserHandler) error
}
