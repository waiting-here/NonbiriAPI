package auth

import (
	"net/http"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const maxAuthorizationBytes = 512

// CallerKeyMiddleware authenticates Authorization: Bearer nbk_<secret> on the
// user station. It never accepts a user/admin session cookie as a platform
// caller credential and checks ban state in the repository lookup.
func CallerKeyMiddleware(store *db.Store, next http.Handler) http.Handler {
	return CallerKeyMiddlewareWithGate(store, next, nil)
}

// CallerKeyMiddlewareWithGate is the production caller rail. The optional
// gate repeats the exact bearer-key lookup under a user lifecycle lease and
// replaces the request context with the lease's cancellation-aware context.
func CallerKeyMiddlewareWithGate(store *db.Store, next http.Handler, gate UserRequestGate) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireStation(w, r, host.StationUser) {
			return
		}
		if store == nil || next == nil {
			writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
			return
		}
		key, ok := bearerCallerKey(r)
		if !ok {
			writeStableError(w, httperr.CodeUnauthorized, "authentication required")
			return
		}
		user, err := store.GetUserByCallerKey(key)
		if err != nil || user == nil || user.IsAdmin || user.IsBanned {
			writeStableError(w, httperr.CodeUnauthorized, "authentication required")
			return
		}
		requestContext := r.Context()
		release := func() {}
		if gate != nil {
			var gateErr error
			requestContext, release, gateErr = gate(requestContext, user.ID, key)
			if release == nil {
				release = func() {}
			}
			if gateErr != nil || requestContext == nil {
				release()
				if r.Context().Err() != nil {
					return
				}
				writeStableError(w, httperr.CodeUnauthorized, "authentication required")
				return
			}
		}
		defer release()
		request := r.WithContext(withPrincipal(requestContext, Principal{User: user, Kind: PrincipalCallerKey}))
		next.ServeHTTP(w, request)
	})
}

func bearerCallerKey(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 || len(values[0]) > maxAuthorizationBytes || strings.ContainsAny(values[0], "\x00\r\n\t") {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	key := parts[1]
	if !strings.HasPrefix(key, db.CallerKeyPrefix) || len(key) > maxAuthorizationBytes {
		return "", false
	}
	return key, true
}

// UserSessionMiddleware and AdminSessionMiddleware are functional aliases for
// callers that prefer a package-level mount helper.
func UserSessionMiddleware(service *UserAuth, next http.Handler) http.Handler {
	if service == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		})
	}
	return service.Middleware(next)
}

func AdminSessionMiddleware(service *AdminAuth, next http.Handler) http.Handler {
	if service == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		})
	}
	return service.Middleware(next)
}
