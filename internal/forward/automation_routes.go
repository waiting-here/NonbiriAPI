package forward

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// WrapRouteMethods authenticates only concrete paths accepted by a finite
// control-route matcher, including canonical path encoding and method checks.
func (middleware *CallerKeyMiddleware) WrapRouteMethods(next http.Handler, methods func(string) []string) http.Handler {
	return middleware.wrap(next, func(method, path, escaped string) *wireFailure {
		if methods == nil || path == "" || path != escaped {
			failure := platformFailure(httperr.CodeNotFound, "not found")
			return &failure
		}
		allowed := methods(path)
		if len(allowed) == 0 {
			failure := platformFailure(httperr.CodeNotFound, "not found")
			return &failure
		}
		for _, value := range allowed {
			if value == method {
				return nil
			}
		}
		failure := platformFailure(httperr.CodeMethodNotAllowed, "method not allowed")
		return &failure
	}, nil)
}
