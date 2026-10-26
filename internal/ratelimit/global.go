package ratelimit

import (
	"context"
	"crypto/rand"
)

// ReserveGlobal participates in the same global window as inference without
// applying or consuming a personal window. A waiting global reservation stays
// active until dispatch or release; its counting window starts at dispatch.
func (r *RPM) ReserveGlobal(ctx context.Context) (*RPMReservation, RPMDecision, error) {
	if r == nil || ctx == nil {
		return nil, RPMDecision{}, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, RPMDecision{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, RPMDecision{}, ErrClosed
	}
	now := r.clock.Now()
	r.pruneLocked(now)
	decision := RPMDecision{Allowed: true, Reason: RPMAllowed, GlobalCount: len(r.global), GlobalLimit: r.limits.GlobalLimit}
	if len(r.global) >= r.limits.GlobalLimit {
		decision.Allowed = false
		decision.Reason = RPMGlobalLimit
		decision.RetryAfter = r.earliestExpiryLocked(r.global, now)
		return nil, decision, nil
	}
	if r.eventCount >= r.maxEvents {
		decision.Allowed = false
		decision.Reason = RPMCapacity
		return nil, decision, ErrCapacity
	}
	event := &rpmEvent{id: rand.Text(), at: now, expires: now.Add(r.window), globalOnly: true}
	event.state.Store(uint32(rpmEventActive))
	r.global = append(r.global, event)
	r.globalPeak = max(r.globalPeak, len(r.global))
	r.eventCount++
	decision.GlobalCount++
	event.decision = decision
	return &RPMReservation{limiter: r, event: event}, decision, nil
}
