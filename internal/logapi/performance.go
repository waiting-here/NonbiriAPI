package logapi

import (
	"context"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type logReadRoleKey struct{}

func logReadContext(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, logReadRoleKey{}, role)
}

func observeLogRead(ctx context.Context, phase string) func(error) {
	role, _ := ctx.Value(logReadRoleKey{}).(string)
	return observability.LogReadPhase(ctx, role, phase)
}
