package flowcontrol

import (
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"time"
)

// Observation is an authoritative user-admission snapshot. Active is the total
// concurrency count, independent of later routing classification.
type Observation struct {
	Event                              string
	At, AdmittedAt                     time.Time
	UserID                             int64
	RequestID                          string
	RPMLimit, ConcurrencyLimit, Active int
	Reason                             string
}

// Observer performs only bounded, nonblocking memory work. Admission locks
// are held during delivery; observers must not call back into the controller.
type Observer interface{ Observe(Observation) }

func (c *Controller) rpmObserver(userID int64, requestID string) ratelimit.RPMObserver {
	if c == nil || c.observer == nil {
		return nil
	}
	return ratelimit.RPMObserverFunc(func(v ratelimit.RPMObservation) {
		event := "rpm_" + v.Event
		if v.Event == "configuration" {
			event = "limits_changed"
		}
		reason := ""
		switch v.Decision.Reason {
		case ratelimit.RPMUserLimit:
			reason = "user_rpm"
		case ratelimit.RPMGlobalLimit:
			reason = "global_rpm"
		case ratelimit.RPMCapacity:
			reason = "capacity"
		}
		c.observer.Observe(Observation{Event: event, At: v.At, AdmittedAt: v.AdmittedAt, UserID: userID, RequestID: requestID, RPMLimit: v.Decision.UserLimit, Reason: reason})
	})
}

// NotifyUserLimitsChanged follows an authoritative account-limit commit.
func (c *Controller) NotifyUserLimitsChanged(userID int64) {
	if c != nil && c.userConcurrency != nil && userID > 0 {
		c.userConcurrency.observeBoundary("limits_changed", userID)
	}
}
func (c *Controller) NotifyConfigurationChanged() {
	if c != nil && c.userConcurrency != nil {
		c.userConcurrency.observeBoundary("limits_changed", 0)
	}
}

// ObserveAuditBoundary publishes independent fences for both admission locks.
// Consumers must use the earlier fence when completing an observation window.
func (c *Controller) ObserveAuditBoundary() {
	if c == nil {
		return
	}
	c.userConcurrency.observeBoundary("concurrency_boundary", 0)
	c.limiter.ObserveBoundary(c.rpmObserver(0, ""))
}
func (l *userConcurrencyLimiter) observeBoundary(event string, userID int64) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.observer != nil {
		l.observer.Observe(Observation{Event: event, At: l.now(), UserID: userID})
	}
}
func (l *userConcurrencyLimiter) observeLocked(event string, userID int64, requestID string, limit, active int) {
	if l.observer != nil {
		l.observer.Observe(Observation{Event: event, At: l.now(), UserID: userID, RequestID: requestID, ConcurrencyLimit: limit, Active: active})
	}
}
