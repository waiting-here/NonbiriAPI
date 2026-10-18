package lakenotes

import (
	"context"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type lakeOperationContextKey struct{}

func lakeOperation(ctx context.Context) string {
	if operation, _ := ctx.Value(lakeOperationContextKey{}).(string); operation != "" {
		return operation
	}
	return "lake_mutation"
}

func lakePhase(ctx context.Context, phase string) func(error) {
	return observability.DatabasePhase(ctx, lakeOperation(ctx), phase)
}
