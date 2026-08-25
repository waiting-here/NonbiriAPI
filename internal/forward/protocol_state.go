package forward

import (
	"context"
	"sync/atomic"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type protocolStateContextKey struct{}

// ProtocolState is a request-scoped, body-free result marker. The connector
// runner sets Completed only after its protocol parser reaches a successful
// terminal condition; an HTTP 200 or clean writer return alone never sets it.
// It is intentionally independent of the caller-visible response writer.
type ProtocolState struct {
	completed atomic.Bool
}

func WithProtocolState(ctx context.Context, state *ProtocolState) context.Context {
	if ctx == nil || state == nil {
		return ctx
	}
	return context.WithValue(ctx, protocolStateContextKey{}, state)
}

func ProtocolStateFromContext(ctx context.Context) *ProtocolState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(protocolStateContextKey{}).(*ProtocolState)
	return state
}

func (s *ProtocolState) Mark(result connectorcontract.AttemptResult) {
	if s != nil && result.Success {
		s.completed.Store(true)
	}
}

func (s *ProtocolState) Completed() bool {
	return s != nil && s.completed.Load()
}
