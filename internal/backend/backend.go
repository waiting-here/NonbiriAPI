// Package backend defines the narrow execution boundary between protocol
// connectors, model discovery, and the process-wide outbound egress boundary.
//
// The only production implementation is LocalBackend, which wraps the single
// shared *egress.Stack. There is deliberately no production constructor that
// yields an EndpointClient or Backend without going through that stack, so
// SSRF policy, DNS pinning, redirect refusal, proxy isolation, timeouts,
// shared concurrency gates, cumulative response bounds, and secret hygiene
// cannot silently differ between consumers. Narrow fakes of these interfaces
// are reserved for tests.
package backend

import (
	"errors"
	"net/http"
	"reflect"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

// EndpointClient is one validated upstream endpoint. BaseURL returns the
// canonical base URL used as the concurrency key; Do performs exactly one
// origin-bound request through the shared outbound boundary.
type EndpointClient interface {
	BaseURL() string
	Do(*http.Request) (*http.Response, error)
}

// Backend opens EndpointClients for configured endpoints and reports the
// cumulative response ceiling that every consumer must honor.
type Backend interface {
	Open(baseURL string) (EndpointClient, error)
	MaxResponseBytes() int64
}

// The egress stack's protected client satisfies the endpoint contract
// directly; LocalBackend hands out exactly these clients.
var _ EndpointClient = (*egress.Client)(nil)

// LocalBackend is the only production Backend. Every Open delegates to the
// one process-wide egress Stack: client creation, transport caching, the
// concurrency gate, and the response ceiling all remain owned by that stack,
// never copied or bypassed.
type LocalBackend struct {
	stack *egress.Stack
}

// NewLocal wraps the shared egress Stack. Callers must pass the same Stack
// instance that every other consumer uses; a nil Stack is rejected.
func NewLocal(stack *egress.Stack) (*LocalBackend, error) {
	if stack == nil {
		return nil, errors.New("backend: egress stack is required")
	}
	return &LocalBackend{stack: stack}, nil
}

// Open validates the base URL against the shared policy and returns a client
// bound to its canonical origin through the shared Stack.
func (b *LocalBackend) Open(baseURL string) (EndpointClient, error) {
	if b == nil || b.stack == nil {
		return nil, errors.New("backend: local backend is unavailable")
	}
	return b.stack.NewClient(baseURL)
}

// MaxResponseBytes reports the shared cumulative response ceiling enforced by
// every client the Stack created.
func (b *LocalBackend) MaxResponseBytes() int64 {
	if b == nil || b.stack == nil {
		return 0
	}
	return b.stack.MaxResponseBytes()
}

var _ Backend = (*LocalBackend)(nil)

// IsNil reports whether a Backend value is unusable because it is nil or
// holds a nil pointer, so constructors can reject it like any other missing
// dependency instead of failing later on first use.
func IsNil(value Backend) bool {
	if value == nil {
		return true
	}
	switch v := reflect.ValueOf(value); v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
