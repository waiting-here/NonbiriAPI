package flowcontrol

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// IdentityResolver extracts a CallerKey-established user id from a request.
// Production passes forward.CallerIdentity; the resolver contract is repeated
// here so the flow-control layer never imports the forward package. A
// resolution failure (missing, invalid, banned, or browser-session identity)
// fails closed with a service-unavailable response; a forwarding handler is
// never allowed to run unmetered.
type IdentityResolver func(*http.Request) (int64, error)

// Middleware mounts the shared Controller in front of the forwarding exit.
// Only POST /v1/chat/completions is metered; /v1/models and any other path
// pass through without RPM accounting, and health/admin/user APIs never share
// this limiter. CallerKey-only authentication is unchanged: the middleware
// only reads the identity the auth layer already installed.
type Middleware struct {
	controller *Controller
	identity   IdentityResolver
	next       http.Handler
}

// NewMiddleware validates the wiring. The returned Middleware is mounted with
// Wrap (or registered directly once next is set via Wrap).
func NewMiddleware(controller *Controller, identity IdentityResolver) (*Middleware, error) {
	if controller == nil {
		return nil, errors.New("flowcontrol: controller is required")
	}
	if identity == nil {
		return nil, errors.New("flowcontrol: identity resolver is required")
	}
	return &Middleware{controller: controller, identity: identity}, nil
}

// Wrap returns a handler that meters next. It must be mounted between the
// CallerKey auth middleware and the forward handler at the same path root.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		m.serveHTTP(writer, request, next)
	})
}

// ServeHTTP meters the pre-registered next handler (set through Wrap).
func (m *Middleware) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	m.serveHTTP(writer, request, m.next)
}

func (m *Middleware) serveHTTP(writer http.ResponseWriter, request *http.Request, next http.Handler) {
	if m == nil || m.controller == nil || m.identity == nil {
		writeUnavailable(writer, request)
		return
	}
	if next == nil {
		writeUnavailable(writer, request)
		return
	}
	if !meteredRequest(request) {
		next.ServeHTTP(writer, request)
		return
	}
	userID, err := m.identity(request)
	if err != nil || userID <= 0 {
		writeUnavailable(writer, request)
		return
	}

	reservation, retryAfter, err := m.controller.Admit(request.Context(), userID)
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		if errors.Is(err, ErrRateLimited) {
			writeRateLimited(writer, retryAfter)
			return
		}
		writeUnavailable(writer, request)
		return
	}

	tracker := &responseTracker{ResponseWriter: writer}
	// The deferred terminal action guarantees no reservation leaks on panic
	// or early return. The byte rule decides the terminal state: once any
	// response body byte has been written (including mid-stream errors and
	// client disconnects after the first byte) the request is committed; a
	// pre-commit failure, cancellation, or panic without output releases.
	defer func() {
		if !reservation.Active() {
			return
		}
		if tracker.wroteBody {
			reservation.Commit()
		} else {
			reservation.Release()
		}
	}()
	next.ServeHTTP(tracker, request)
}

// meteredRequest scopes RPM accounting to the chat-completion exit. The path
// mirrors the forward handler's mux registration exactly; anything else
// (model listing, health, admin/user stations) never touches this limiter.
func meteredRequest(request *http.Request) bool {
	return request != nil && request.Method == http.MethodPost &&
		request.URL != nil && request.URL.Path == "/v1/chat/completions"
}

// writeRateLimited emits the stable rate_limited code with a bounded
// Retry-After header. It reveals no user counts, keys, IPs, or internal
// state: the body is the standard envelope with message only.
func writeRateLimited(writer http.ResponseWriter, retryAfter time.Duration) {
	writer.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
	httperr.WriteError(writer, httperr.New(httperr.CodeRateLimited, "rate limit exceeded"))
}

// writeUnavailable fails closed when the limiter is closed or unavailable:
// a request that cannot be metered must not proceed unmetered.
func writeUnavailable(writer http.ResponseWriter, request *http.Request) {
	if request != nil && request.Context().Err() != nil {
		return
	}
	httperr.WriteError(writer, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
}

// retryAfterSeconds rounds a duration up to a whole second and keeps the
// header in [1, 3600].
func retryAfterSeconds(duration time.Duration) int {
	seconds := int((duration + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return seconds
}

// responseTracker observes the commit boundary: any response body byte marks
// the request as committed. All writes pass through to the underlying
// writer, and the optional-interface surface (Flush, Unwrap) is preserved so
// the streaming connector's direct http.Flusher assertion and
// http.NewResponseController calls keep working through the tracker.
type responseTracker struct {
	http.ResponseWriter
	wroteBody bool
}

// Write forwards every body byte and records the commit boundary. io.ReaderFrom
// is deliberately not implemented: a sendfile fast path would bypass this
// observation, so net/http falls back to Write, which is exactly what the
// streaming/non-streaming connectors already use.
func (t *responseTracker) Write(p []byte) (int, error) {
	n, err := t.ResponseWriter.Write(p)
	if n > 0 {
		t.wroteBody = true
	}
	return n, err
}

// Flush keeps the direct http.Flusher type assertion used by the openai
// streaming adapter working through the tracker.
func (t *responseTracker) Flush() {
	if flusher, ok := t.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.NewResponseController reach the underlying writer's
// optional interfaces (Flusher and beyond).
func (t *responseTracker) Unwrap() http.ResponseWriter { return t.ResponseWriter }
