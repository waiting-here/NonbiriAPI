package lakenotes

import (
	"context"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type checkpointContextKey struct{}

func lakeOperation(ctx context.Context) string {
	if checkpoint, _ := ctx.Value(checkpointContextKey{}).(bool); checkpoint {
		return "lake_checkpoint"
	}
	return "lake_mutation"
}

func lakePhase(ctx context.Context, phase string) func(error) {
	return observability.DatabasePhase(ctx, lakeOperation(ctx), phase)
}
