package ratelimit

import (
	"context"
	"time"
)

// ReserveModelObserved admits all model calls against the global counters and
// charity calls against their additional cap. Charity is checked first, under
// the same lock, so simultaneous exhaustion has one unambiguous reason.
func (r *RPM) ReserveModelObserved(ctx context.Context, userKey string, charity bool, charityLimit int, observer RPMObserver) (*RPMReservation, RPMDecision, error) {
	return r.reserveModelObserved(ctx, userKey, 0, charity, false, charityLimit, observer)
}

func (r *RPM) charityDecisionLocked(userKey string, limit int, light bool, now time.Time, decision RPMDecision) RPMDecision {
	if limit == 0 {
		limit = r.limits.CharityPerUserLimit
		if light {
			limit = r.limits.LightCharityPerUserLimit
		}
	}
	if limit == 0 {
		limit = DefaultRPMPerUserLimit
		if light {
			limit = DefaultRPMLightPerUserLimit
		}
	}
	decision.CharityLimit = limit
	var firstExpiry time.Time
	for _, event := range r.users[userKey] {
		if event.charity && event.light == light {
			decision.CharityCount++
			if firstExpiry.IsZero() || event.expires.Before(firstExpiry) {
				firstExpiry = event.expires
			}
		}
	}
	if decision.CharityCount >= limit {
		decision.Allowed, decision.Reason = false, RPMCharityUserLimit
		decision.RetryAfter = firstExpiry.Sub(now)
	}
	return decision
}

// ReserveClassifiedObserved applies an optional combined user cap and the selected charity class cap atomically.
func (r *RPM) ReserveClassifiedObserved(ctx context.Context, userKey string, userLimit int, charity, light bool, charityLimit int, observer RPMObserver) (*RPMReservation, RPMDecision, error) {
	return r.reserveModelObserved(ctx, userKey, userLimit, charity, light, charityLimit, observer)
}
