package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type startupRankings interface {
	Advance(context.Context, int64) (bool, error)
}
type startupLifecycle interface {
	RecoverBeforeListenerAt(context.Context, int64) error
}

// Every ranking batch owns its transaction. A game settlement must never be
// responsible for committing prerequisite catch-up: its readiness failure would
// roll that progress back. Both phases use one decision time before workers run.
func recoverRankingsAndLifecycleBeforeListener(ctx context.Context, rankings startupRankings, lifecycle startupLifecycle, now int64) error {
	if ctx == nil || rankings == nil || lifecycle == nil || now < 0 || now > 253402300799 {
		return errors.New("startup recovery dependencies are invalid")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, cancel := context.WithTimeout(ctx, 2*time.Second)
		ready, err := rankings.Advance(batch, now)
		cancel()
		if err != nil {
			return fmt.Errorf("advance rankings before game recovery: %w", err)
		}
		if ready {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return lifecycle.RecoverBeforeListenerAt(ctx, now)
}
