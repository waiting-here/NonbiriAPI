package auth

import (
	"context"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

// WithSessionActor carries a previously recorded session identity for a
// continuation. It grants no authority: domain reads and final writes still
// revalidate the session, generation and current permissions through authz.
func WithSessionActor(ctx context.Context, actor authz.Actor) context.Context {
	return withActor(ctx, actor)
}
