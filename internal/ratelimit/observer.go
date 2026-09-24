package ratelimit

import (
	"context"
	"time"
)

// RPMObservation is captured while holding the admission mutex. Terminal
// observations retain the original admission instant and effective user cap.
type RPMObservation struct {
	Event      string
	At         time.Time
	AdmittedAt time.Time
	Decision   RPMDecision
}

// RPMObserver must return immediately, without database or network work or
// calls back into the limiter. Observers must report their own queue losses.
type RPMObserver interface{ ObserveRPM(RPMObservation) }

type RPMObserverFunc func(RPMObservation)

func (f RPMObserverFunc) ObserveRPM(value RPMObservation) {
	if f != nil {
		f(value)
	}
}

// ReserveObserved uses the current default user cap when userLimit is zero.
func (r *RPM) ReserveObserved(ctx context.Context, userKey string, userLimit int, observer RPMObserver) (*RPMReservation, RPMDecision, error) {
	return r.reserveObserved(ctx, userKey, userLimit, observer)
}

func (r *RPM) observeLocked(observer RPMObserver, event string, at, admitted time.Time, decision RPMDecision) {
	if observer != nil {
		observer.ObserveRPM(RPMObservation{Event: event, At: at, AdmittedAt: admitted, Decision: decision})
	}
}

// ObserveBoundary fences all earlier admission and terminal observations. It
// remains available after Close so a lifecycle owner can flush final state.
func (r *RPM) ObserveBoundary(observer RPMObserver) {
	if r == nil || observer == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observeLocked(observer, "boundary", r.clock.Now(), time.Time{}, RPMDecision{})
}

func (r *RPM) transition(event *rpmEvent, state rpmEventState) bool {
	if r == nil || event == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !event.state.CompareAndSwap(uint32(rpmEventActive), uint32(state)) {
		return false
	}
	now := r.clock.Now()
	kind := "commit"
	if state == rpmEventReleased {
		kind = "release"
	}
	r.observeLocked(event.observer, kind, now, event.at, event.decision)
	if state == rpmEventReleased && !r.closed {
		r.pruneLocked(now)
	}
	return true
}

// SetLimitsObserved publishes the configuration boundary under the same mutex
// that protects admission. Existing reservations preserve their original cap.
func (r *RPM) SetLimitsObserved(limits RPMLimits, observer RPMObserver) error {
	if r == nil {
		return ErrClosed
	}
	if err := validateRPMLimits(limits, r.maxEvents); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	now := r.clock.Now()
	changed := r.limits != limits
	r.limits = limits
	r.pruneLocked(now)
	if changed {
		r.observeLocked(observer, "configuration", now, time.Time{}, RPMDecision{UserLimit: limits.PerUserLimit, GlobalLimit: limits.GlobalLimit})
	}
	return nil
}
