// Package smartbackoff owns bounded quota classification and per-key recovery.
package smartbackoff

import (
	"context"
	"errors"
)

var ErrInvalid = errors.New("invalid smart backoff state")
var ErrUnavailable = errors.New("smart backoff is waiting for recovery")

type Status struct {
	State   string `json:"state"`
	Reason  string `json:"reason"`
	Source  string `json:"source"`
	RetryAt int64  `json:"retry_at"`
}
type Projection struct {
	Backoff *Status `json:"smart_backoff"`
}

type charityCallKey struct{}

// WithCharityCall scopes quota classification to an actual charity dispatch.
func WithCharityCall(ctx context.Context, charity bool) context.Context {
	return context.WithValue(ctx, charityCallKey{}, charity)
}
func IsCharityCall(ctx context.Context) bool {
	value, _ := ctx.Value(charityCallKey{}).(bool)
	return value
}
