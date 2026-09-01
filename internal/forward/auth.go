package forward

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const maxAuthorizationBytes = 512

type callerIdentityContextKey struct{}

// CallerKeyMiddleware authenticates exactly the two public ingress routes,
// repeats the same key verification inside a lifecycle lease, and installs
// only the safe user/generation identity in context. It must wrap flowcontrol.
type CallerKeyMiddleware struct {
	resolver  CallerKeyResolver
	lifecycle *lifecyclegate.Gate
}

func NewCallerKeyMiddleware(resolver CallerKeyResolver, lifecycle *lifecyclegate.Gate) (*CallerKeyMiddleware, error) {
	if resolver == nil || lifecycle == nil {
		return nil, ErrInvalidConfiguration
	}
	return &CallerKeyMiddleware{resolver: resolver, lifecycle: lifecycle}, nil
}

func (middleware *CallerKeyMiddleware) Wrap(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if request == nil || request.URL == nil {
			writeFailure(writer, platformFailure(httperr.CodeNotFound, "not found"))
			return
		}
		if failure := exactIngressFailure(request.Method, request.URL.Path, request.URL.EscapedPath()); failure != nil {
			writeFailure(writer, *failure)
			return
		}
		if middleware == nil || middleware.resolver == nil || middleware.lifecycle == nil {
			writeFailure(writer, platformFailure(httperr.CodeServiceUnavailable, "authentication service unavailable"))
			return
		}
		presented, ok := bearerCallerKey(request)
		if !ok {
			writeFailure(writer, platformFailure(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		identity, err := middleware.resolver.ResolveCallerKey(request.Context(), presented)
		if err != nil || !validCallerIdentity(identity) {
			if request.Context().Err() != nil {
				return
			}
			if errors.Is(err, resources.ErrNotFound) || err == nil {
				writeFailure(writer, platformFailure(httperr.CodeUnauthorized, "authentication required"))
			} else {
				writeFailure(writer, platformFailure(httperr.CodeServiceUnavailable, "authentication service unavailable"))
			}
			return
		}

		leaseContext, release, err := middleware.lifecycle.Admit(
			request.Context(), identity.UserID, presented,
			func(ctx context.Context, expectedUserID int64, key string) (bool, error) {
				current, resolveErr := middleware.resolver.ResolveCallerKey(ctx, key)
				if errors.Is(resolveErr, resources.ErrNotFound) {
					return false, nil
				}
				if resolveErr != nil {
					return false, resolveErr
				}
				return validCallerIdentity(current) && current.UserID == expectedUserID && current.Generation == identity.Generation, nil
			},
		)
		if release == nil {
			release = func() {}
		}
		if err != nil || leaseContext == nil {
			release()
			if request.Context().Err() != nil {
				return
			}
			if errors.Is(err, lifecyclegate.ErrInvalid) || errors.Is(err, lifecyclegate.ErrRetiring) {
				writeFailure(writer, platformFailure(httperr.CodeUnauthorized, "authentication required"))
			} else {
				writeFailure(writer, platformFailure(httperr.CodeServiceUnavailable, "authentication service unavailable"))
			}
			return
		}
		defer release()
		ctx := context.WithValue(leaseContext, callerIdentityContextKey{}, identity)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

// CallerIdentity is the flowcontrol identity resolver. It accepts only an
// identity installed by CallerKeyMiddleware and never browser/admin state.
func CallerIdentity(request *http.Request) (int64, error) {
	if request == nil {
		return 0, errors.New("forward: caller request is required")
	}
	identity, ok := request.Context().Value(callerIdentityContextKey{}).(resources.CallerIdentity)
	if !ok || !validCallerIdentity(identity) {
		return 0, errors.New("forward: CallerKey identity is required")
	}
	return identity.UserID, nil
}

func validCallerIdentity(identity resources.CallerIdentity) bool {
	return identity.UserID > 0 && identity.Generation >= 0
}

func bearerCallerKey(request *http.Request) (string, bool) {
	if request == nil {
		return "", false
	}
	values := request.Header.Values("Authorization")
	if len(values) != 1 || len(values[0]) > maxAuthorizationBytes || strings.ContainsAny(values[0], "\x00\r\n\t") {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > maxAuthorizationBytes {
		return "", false
	}
	return parts[1], true
}

func exactIngressFailure(method, path, escapedPath string) *wireFailure {
	if path == "" || escapedPath != path {
		failure := platformFailure(httperr.CodeNotFound, "not found")
		return &failure
	}
	want := ""
	switch path {
	case "/v1/models":
		want = http.MethodGet
	case "/v1/chat/completions":
		want = http.MethodPost
	default:
		failure := platformFailure(httperr.CodeNotFound, "not found")
		return &failure
	}
	if method != want {
		failure := platformFailure(httperr.CodeMethodNotAllowed, "method not allowed")
		return &failure
	}
	return nil
}
