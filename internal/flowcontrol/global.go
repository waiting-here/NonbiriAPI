package flowcontrol

import (
	"context"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"time"
)

// WaitGlobal admits administrator-owned model work against this controller's
// existing global counter. It never obtains a personal concurrency permit or
// reads personal limits, and never invokes the user-denial penalty callback.
func (c *Controller) WaitGlobal(ctx context.Context) (*ratelimit.RPMReservation, error) {
	if c == nil || c.limiter == nil {
		return nil, ErrClosed
	}
	for {
		reservation, decision, err := c.limiter.ReserveGlobal(ctx)
		if err != nil {
			if errors.Is(err, ratelimit.ErrClosed) {
				return nil, ErrClosed
			}
			return nil, err
		}
		if reservation != nil {
			return reservation, nil
		}
		wait := decision.RetryAfter
		// Reconsider lowered limits and released reservations without a busy loop.
		if wait <= 0 || wait > time.Second {
			wait = time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
