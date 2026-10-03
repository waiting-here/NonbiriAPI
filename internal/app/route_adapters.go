package app

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type authorizedHandler[P any] interface {
	~func(http.ResponseWriter, *http.Request, P)
}

// The domain retains its principal type and live transaction authorizer.
// Adaptation forwards the original request, including cancellation and actor.
func registerUserAdapter[P any, H authorizedHandler[P]](runtime *auth.Runtime, method, pattern string, handler H, principal func(int64) P) error {
	if runtime == nil || handler == nil || principal == nil {
		return auth.ErrInvalidRoute
	}
	return runtime.RegisterUserRoute(method, pattern, adaptUserPrincipal(handler, principal))
}

func adaptUserPrincipal[P any, H authorizedHandler[P]](handler H, principal func(int64) P) resources.AuthorizedUserHandler {
	return func(w http.ResponseWriter, r *http.Request, user resources.UserPrincipal) {
		handler(w, r, principal(user.UserID))
	}
}

func registerAdminAdapter[P any, H authorizedHandler[P]](runtime *auth.Runtime, method, pattern string, handler H, principal func(int64) P) error {
	if runtime == nil || handler == nil || principal == nil {
		return auth.ErrInvalidRoute
	}
	return runtime.RegisterAdminRoute(method, pattern, adaptAdminPrincipal(handler, principal))
}

func adaptAdminPrincipal[P any, H authorizedHandler[P]](handler H, principal func(int64) P) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, ok := auth.ActorFromContext(r.Context())
		if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
			httperr.WriteError(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		handler(w, r, principal(actor.UserID))
	})
}
