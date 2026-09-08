package forward

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

type deniedChatScopeKey struct{}

// Denied requests have already released their normal admission permits.
// Keep their optional body classification bounded across all callers as well.
const maxRPMDenialReads = 16

type deniedChatScope struct {
	request  *http.Request
	writer   http.ResponseWriter
	userID   int64
	readGate chan struct{}
	once     sync.Once
	charity  bool
}

// WithRPMDenialScope carries an authenticated request to the denial observer.
// It never reads a body on admission: concurrency and RPM remain ahead of all
// parsing. Only a user-RPM denial may consume the body via CharityRPMDenial.
func WithRPMDenialScope(next http.Handler) http.Handler {
	readGate := make(chan struct{}, maxRPMDenialReads)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, err := CallerIdentity(r)
		if err != nil || r.Method != http.MethodPost || r.URL == nil || r.URL.Path != "/v1/chat/completions" {
			next.ServeHTTP(w, r)
			return
		}
		scope := &deniedChatScope{request: r, writer: w, userID: userID, readGate: readGate}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), deniedChatScopeKey{}, scope)))
	})
}

// CharityRPMDenial resolves the same reserved model namespace as preflight,
// using the same strict bounded JSON decoder. Input whose namespace cannot be
// decoded never becomes a user violation. The request is not forwarded or replayed.
// A transport that cannot enforce the short read deadline keeps the rate-limit
// response but cannot safely attribute an automatic-ban event.
func CharityRPMDenial(ctx context.Context, userID int64) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	scope, ok := ctx.Value(deniedChatScopeKey{}).(*deniedChatScope)
	if !ok || scope.userID != userID {
		return false
	}
	scope.once.Do(func() {
		r := scope.request
		if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength > openai.MaxRequestBodyBytes || r.Body == nil {
			return
		}
		if _, ok := validateChatMedia(r); !ok {
			return
		}
		select {
		case scope.readGate <- struct{}{}:
			defer func() { <-scope.readGate }()
		default:
			return
		}
		deadline := time.Now().Add(2 * time.Second)
		if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
			deadline = until
		}
		control := http.NewResponseController(scope.writer)
		if err := control.SetReadDeadline(deadline); err != nil {
			return
		}
		defer func() { _ = control.SetReadDeadline(time.Time{}) }()
		defer r.Body.Close()
		request, err := openai.DecodeChatRequest(r.Body, openai.MaxRequestBodyBytes)
		if err != nil {
			return
		}
		defer request.Clear()
		if ctx.Err() == nil {
			scope.charity = strings.HasPrefix(request.Model, charityModelPrefix)
		}
	})
	return scope.charity && ctx.Err() == nil
}
