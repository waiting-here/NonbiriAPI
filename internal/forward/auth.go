package forward

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const maxAuthorizationBytes = 512

type callerIdentityContextKey struct{}

// CallerKeyMiddleware authenticates exactly the public ingress routes,
// repeats the same key verification inside a lifecycle lease, and installs
// only the safe user/generation identity in context. It must wrap flowcontrol.
type CallerKeyMiddleware struct {
	resolver   CallerKeyResolver
	lifecycle  *lifecyclegate.Gate
	rejections requestattempt.Recorder
}

func NewCallerKeyMiddleware(resolver CallerKeyResolver, lifecycle *lifecyclegate.Gate, record ...requestattempt.Recorder) (*CallerKeyMiddleware, error) {
	if resolver == nil || lifecycle == nil {
		return nil, ErrInvalidConfiguration
	}
	if len(record) > 1 {
		return nil, ErrInvalidConfiguration
	}
	var recorder requestattempt.Recorder
	if len(record) == 1 {
		recorder = record[0]
	}
	return &CallerKeyMiddleware{resolver: resolver, lifecycle: lifecycle, rejections: recorder}, nil
}

func (middleware *CallerKeyMiddleware) Wrap(next http.Handler) http.Handler {
	return middleware.wrap(next, exactIngressFailure, nil)
}

// WrapWithUnavailable preserves the site's unavailable response for requests
// that cannot establish an identity. Valid identities still reach the inner
// gate, where a durable authenticated refusal can be recorded.
func (middleware *CallerKeyMiddleware) WrapWithUnavailable(next http.Handler, unavailable func(http.ResponseWriter, *http.Request) bool) http.Handler {
	return middleware.wrap(next, exactIngressFailure, unavailable)
}

// WrapExact authenticates an explicitly mounted set of control routes without
// extending the public model ingress or any browser-session route. The route
// table is copied so it cannot be changed after the handler is constructed.
func (middleware *CallerKeyMiddleware) WrapExact(next http.Handler, routes map[string]string) http.Handler {
	methods := make(map[string][]string, len(routes))
	for path, method := range routes {
		methods[path] = []string{method}
	}
	return middleware.WrapExactMethods(next, methods)
}

// WrapExactMethods permits only the listed methods at each exact control path.
func (middleware *CallerKeyMiddleware) WrapExactMethods(next http.Handler, routes map[string][]string) http.Handler {
	allowed := make(map[string]map[string]bool, len(routes))
	for path, methods := range routes {
		allowed[path] = make(map[string]bool, len(methods))
		for _, method := range methods {
			if method != "" {
				allowed[path][method] = true
			}
		}
	}
	return middleware.wrap(next, func(method, path, escapedPath string) *wireFailure {
		want, exists := allowed[path]
		if !exists || path == "" || escapedPath != path || len(want) == 0 {
			failure := platformFailure(httperr.CodeNotFound, "not found")
			return &failure
		}
		if !want[method] {
			failure := platformFailure(httperr.CodeMethodNotAllowed, "method not allowed")
			return &failure
		}
		return nil
	}, nil)
}

func (middleware *CallerKeyMiddleware) wrap(next http.Handler, checkRoute func(string, string, string) *wireFailure, unavailable func(http.ResponseWriter, *http.Request) bool) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		deny := func(failure wireFailure) {
			if unavailable != nil && unavailable(writer, request) {
				return
			}
			writeFailure(writer, failure)
		}
		writer.Header().Set("Cache-Control", "no-store")
		if request == nil || request.URL == nil {
			deny(platformFailure(httperr.CodeNotFound, "not found"))
			return
		}
		observability.MarkResponseCategory(request.Context(), "api_json")
		if failure := checkRoute(request.Method, request.URL.Path, request.URL.EscapedPath()); failure != nil {
			deny(*failure)
			return
		}
		if middleware == nil || middleware.resolver == nil || middleware.lifecycle == nil {
			deny(platformFailure(httperr.CodeServiceUnavailable, "authentication service unavailable"))
			return
		}
		presented, ok := bearerCallerKey(request)
		if !ok {
			deny(platformFailure(httperr.CodeUnauthorized, "authentication required"))
			return
		}
		identity, err := middleware.resolver.ResolveCallerKey(request.Context(), presented)
		if err != nil || !validCallerIdentity(identity) {
			if request.Context().Err() != nil {
				return
			}
			if errors.Is(err, resources.ErrNotFound) || err == nil {
				deny(platformFailure(httperr.CodeUnauthorized, "authentication required"))
			} else {
				deny(platformFailure(httperr.CodeServiceUnavailable, "authentication service unavailable"))
			}
			return
		}

		observability.MarkAccessIdentity(request.Context(), identity.UserID, identity.Generation)
		if middleware.rejections != nil && requestattempt.ValidRoute(request.Method, request.URL.Path) {
			ctx, id, err := requestattempt.New(request.Context(), identity.UserID, request.Method, request.URL.Path)
			if err != nil {
				writeFailure(writer, platformFailure(httperr.CodeServiceUnavailable, "service unavailable"))
				return
			}
			request = request.WithContext(ctx)
			if request.URL.Path == "/v1/models" {
				requestattempt.Classify(ctx, "discovery")
			}
			writer.Header().Set("X-Request-ID", id)
			writer = requestattempt.Wrap(writer, ctx, middleware.rejections)
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
				deny(platformFailure(httperr.CodeUnauthorized, "authentication required"))
			} else {
				deny(platformFailure(httperr.CodeServiceUnavailable, "authentication service unavailable"))
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
	identity, err := CallerKeyIdentity(request)
	return identity.UserID, err
}

// CallerKeyIdentity returns the authenticated generation as well as the user.
// It never accepts browser state or unverified headers.
func CallerKeyIdentity(request *http.Request) (resources.CallerIdentity, error) {
	if request == nil {
		return resources.CallerIdentity{}, errors.New("forward: caller request is required")
	}
	identity, ok := request.Context().Value(callerIdentityContextKey{}).(resources.CallerIdentity)
	if !ok || !validCallerIdentity(identity) {
		return resources.CallerIdentity{}, errors.New("forward: CallerKey identity is required")
	}
	return identity, nil
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
	want := exactIngressMethod(path, escapedPath)
	if want == "" {
		failure := platformFailure(httperr.CodeNotFound, "not found")
		return &failure
	}
	if method != want {
		failure := platformFailure(httperr.CodeMethodNotAllowed, "method not allowed")
		return &failure
	}
	return nil
}

// Authentication and browser preflights share the same exact route table.
func exactIngressMethod(path, escapedPath string) string {
	if path == "" || escapedPath != path {
		return ""
	}
	switch path {
	case "/v1/models":
		return http.MethodGet
	case "/v1/chat/completions", "/v1/embeddings":
		return http.MethodPost
	default:
		return ""
	}
}
