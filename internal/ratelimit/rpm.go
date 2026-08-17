package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultRPMWindow is used when RPMConfig.Window is omitted.
	DefaultRPMWindow = time.Minute
	// DefaultRPMGlobalLimit is the finite site-wide default exposed by
	// DefaultRPMConfig.
	DefaultRPMGlobalLimit = 600
	// DefaultRPMPerUserLimit is the finite per-user default exposed by
	// DefaultRPMConfig.
	DefaultRPMPerUserLimit = 60
)

// RPMLimits contains the two caps enforced by one shared RPM instance.
type RPMLimits struct {
	GlobalLimit  int
	PerUserLimit int
}

// RPMConfig configures a process-local global and per-user sliding window.
// GlobalLimit includes active reservations and committed records. A new
// reservation is rejected when either cap is already reached.
type RPMConfig struct {
	Window       time.Duration
	GlobalLimit  int
	PerUserLimit int
	MaxUserKeys  int
	MaxEvents    int
	MaxKeyBytes  int
}

// DefaultRPMConfig returns finite defaults suitable for an explicitly shared
// site-wide limiter. It is a helper only; NewRPM also accepts a fully explicit
// configuration.
func DefaultRPMConfig() RPMConfig {
	return RPMConfig{
		Window:       DefaultRPMWindow,
		GlobalLimit:  DefaultRPMGlobalLimit,
		PerUserLimit: DefaultRPMPerUserLimit,
		MaxUserKeys:  defaultMaxKeys,
		MaxEvents:    defaultMaxEvents,
		MaxKeyBytes:  defaultMaxKeyBytes,
	}
}

func (c RPMConfig) normalized() (RPMConfig, error) {
	if c.Window == 0 {
		c.Window = DefaultRPMWindow
	}
	if err := validateDuration(c.Window, false); err != nil {
		return RPMConfig{}, err
	}
	var err error
	c.MaxUserKeys, err = boundedInt(c.MaxUserKeys, defaultMaxKeys, maxConfiguredEntries)
	if err != nil {
		return RPMConfig{}, err
	}
	c.MaxEvents, err = boundedInt(c.MaxEvents, defaultMaxEvents, maxConfiguredEntries)
	if err != nil {
		return RPMConfig{}, err
	}
	c.MaxKeyBytes, err = boundedInt(c.MaxKeyBytes, defaultMaxKeyBytes, maxConfiguredBytes)
	if err != nil {
		return RPMConfig{}, err
	}
	if c.GlobalLimit < 1 || c.GlobalLimit > c.MaxEvents ||
		c.PerUserLimit < 1 || c.PerUserLimit > c.MaxEvents {
		return RPMConfig{}, ErrInvalidConfig
	}
	return c, nil
}

func (c RPMConfig) limits() RPMLimits {
	return RPMLimits{GlobalLimit: c.GlobalLimit, PerUserLimit: c.PerUserLimit}
}

func validateRPMLimits(limits RPMLimits, maxEvents int) error {
	if limits.GlobalLimit < 1 || limits.GlobalLimit > maxEvents ||
		limits.PerUserLimit < 1 || limits.PerUserLimit > maxEvents {
		return ErrInvalidConfig
	}
	return nil
}

// RPMReason explains why an RPM decision was not admitted.
type RPMReason uint8

const (
	RPMAllowed RPMReason = iota
	RPMGlobalLimit
	RPMUserLimit
	RPMCapacity
)

// RPMDecision is a consistent snapshot taken under the limiter mutex. Counts
// in an admitted Record or reservation include the newly admitted event;
// counts in Check and a denied decision describe the state before admission.
type RPMDecision struct {
	Allowed     bool
	Reason      RPMReason
	GlobalCount int
	UserCount   int
	GlobalLimit int
	UserLimit   int
	RetryAfter  time.Duration
}

type rpmEventState uint32

const (
	rpmEventActive rpmEventState = iota
	rpmEventCommitted
	rpmEventReleased
)

type rpmEvent struct {
	at    time.Time
	user  string
	state atomic.Uint32
}

// RPM is a thread-safe, bounded sliding-window limiter. One instance must be
// shared by all callers that are subject to the same global cap.
type RPM struct {
	mu sync.Mutex

	clock       Clock
	window      time.Duration
	limits      RPMLimits
	maxUserKeys int
	maxEvents   int
	maxKeyBytes int

	global []*rpmEvent
	users  map[string][]*rpmEvent
	closed bool
}

// NewRPM constructs one shared global/per-user RPM limiter.
func NewRPM(config RPMConfig, opts ...Option) (*RPM, error) {
	config, err := config.normalized()
	if err != nil {
		return nil, err
	}
	settings, err := applyOptions(opts)
	if err != nil {
		return nil, err
	}
	return &RPM{
		clock:       settings.clock,
		window:      config.Window,
		limits:      config.limits(),
		maxUserKeys: config.MaxUserKeys,
		maxEvents:   config.MaxEvents,
		maxKeyBytes: config.MaxKeyBytes,
		users:       make(map[string][]*rpmEvent),
	}, nil
}

// Limits returns a consistent snapshot of the current caps.
func (r *RPM) Limits() RPMLimits {
	if r == nil {
		return RPMLimits{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.limits
}

// SetLimits changes the caps without replacing shared counters. Existing
// events are not discarded; if a cap is lowered, new admissions wait for the
// old events to leave the window.
func (r *RPM) SetLimits(limits RPMLimits) error {
	if r == nil {
		return ErrClosed
	}
	if err := validateRPMLimits(limits, r.maxEvents); err != nil {
		return err
	}
	now := r.clock.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	r.limits = limits
	r.pruneLocked(now)
	return nil
}

// Purge removes expired records and released reservations using the injected
// clock. Normal admission operations already perform this cleanup.
func (r *RPM) Purge() error {
	if r == nil {
		return ErrClosed
	}
	now := r.clock.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	r.pruneLocked(now)
	return nil
}

// Check reports the current state without recording an event. It is useful for
// diagnostics, but callers enforcing a limit must use Record or Reserve so a
// check cannot be separated from admission by a concurrent caller.
func (r *RPM) Check(userKey string) (RPMDecision, error) {
	if r == nil {
		return RPMDecision{}, ErrClosed
	}
	if err := validateKeyBytes(userKey, r.maxKeyBytes); err != nil {
		return RPMDecision{}, err
	}
	return r.check(userKey, 0)
}

// CheckWithLimit is Check with a per-call user cap. The cap is not persisted;
// it is intended for a caller that has already resolved a user-specific limit.
func (r *RPM) CheckWithLimit(userKey string, userLimit int) (RPMDecision, error) {
	if r == nil {
		return RPMDecision{}, ErrClosed
	}
	if err := validateKeyBytes(userKey, r.maxKeyBytes); err != nil {
		return RPMDecision{}, err
	}
	if err := r.validateUserLimit(userLimit); err != nil {
		return RPMDecision{}, err
	}
	return r.check(userKey, userLimit)
}

func (r *RPM) validateUserLimit(userLimit int) error {
	if userLimit < 1 || userLimit > r.maxEvents {
		return ErrInvalidConfig
	}
	return nil
}

func (r *RPM) check(userKey string, userLimit int) (RPMDecision, error) {
	now := r.clock.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return RPMDecision{}, ErrClosed
	}
	r.pruneLocked(now)
	if userLimit == 0 {
		userLimit = r.limits.PerUserLimit
	}
	decision := r.decisionLocked(userKey, userLimit, now)
	if decision.Allowed {
		if _, exists := r.users[userKey]; !exists && len(r.users) >= r.maxUserKeys {
			decision.Allowed = false
			decision.Reason = RPMCapacity
		}
	}
	return decision, nil
}

// Record atomically checks both windows and commits one event when admitted.
// It is the non-reservation operation for requests whose accounting is known
// at admission time.
func (r *RPM) Record(userKey string) (RPMDecision, error) {
	return r.record(userKey, 0)
}

// Allow is an alias for Record for callers that model admission as an allow
// decision rather than as usage recording.
func (r *RPM) Allow(userKey string) (RPMDecision, error) { return r.Record(userKey) }

// RecordWithLimit is Record with a per-call user cap.
func (r *RPM) RecordWithLimit(userKey string, userLimit int) (RPMDecision, error) {
	if r == nil {
		return RPMDecision{}, ErrClosed
	}
	if err := validateKeyBytes(userKey, r.maxKeyBytes); err != nil {
		return RPMDecision{}, err
	}
	if err := r.validateUserLimit(userLimit); err != nil {
		return RPMDecision{}, err
	}
	return r.record(userKey, userLimit)
}

// AllowWithLimit is Allow with a per-call user cap.
func (r *RPM) AllowWithLimit(userKey string, userLimit int) (RPMDecision, error) {
	return r.RecordWithLimit(userKey, userLimit)
}

func (r *RPM) record(userKey string, userLimit int) (RPMDecision, error) {
	if r == nil {
		return RPMDecision{}, ErrClosed
	}
	if err := validateKeyBytes(userKey, r.maxKeyBytes); err != nil {
		return RPMDecision{}, err
	}
	now := r.clock.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return RPMDecision{}, ErrClosed
	}
	r.pruneLocked(now)
	if userLimit == 0 {
		userLimit = r.limits.PerUserLimit
	}
	if err := r.validateUserLimit(userLimit); err != nil {
		return RPMDecision{}, err
	}
	decision := r.decisionLocked(userKey, userLimit, now)
	if !decision.Allowed {
		return decision, nil
	}
	if _, exists := r.users[userKey]; !exists && len(r.users) >= r.maxUserKeys {
		decision.Allowed = false
		decision.Reason = RPMCapacity
		return decision, ErrCapacity
	}
	r.newEventLocked(userKey, now, rpmEventCommitted)
	decision.GlobalCount++
	decision.UserCount++
	decision.Reason = RPMAllowed
	decision.RetryAfter = 0
	return decision, nil
}

// Reserve atomically checks both windows and reserves one event. The
// reservation itself counts against both caps immediately. The caller must
// Commit it when the request is accounted for, or Release it on cancellation,
// failure, or any other path that should not consume the RPM budget.
func (r *RPM) Reserve(ctx context.Context, userKey string) (*RPMReservation, RPMDecision, error) {
	return r.reserve(ctx, userKey, 0)
}

// ReserveWithLimit is Reserve with a per-call user cap.
func (r *RPM) ReserveWithLimit(ctx context.Context, userKey string, userLimit int) (*RPMReservation, RPMDecision, error) {
	if r == nil {
		return nil, RPMDecision{}, ErrClosed
	}
	if err := validateKeyBytes(userKey, r.maxKeyBytes); err != nil {
		return nil, RPMDecision{}, err
	}
	if err := r.validateUserLimit(userLimit); err != nil {
		return nil, RPMDecision{}, err
	}
	return r.reserve(ctx, userKey, userLimit)
}

// TryReserve is the context-free convenience form of Reserve.
func (r *RPM) TryReserve(userKey string) (*RPMReservation, RPMDecision, error) {
	return r.reserve(context.Background(), userKey, 0)
}

// TryReserveWithLimit is TryReserve with a per-call user cap.
func (r *RPM) TryReserveWithLimit(userKey string, userLimit int) (*RPMReservation, RPMDecision, error) {
	if r == nil {
		return nil, RPMDecision{}, ErrClosed
	}
	if err := validateKeyBytes(userKey, r.maxKeyBytes); err != nil {
		return nil, RPMDecision{}, err
	}
	if err := r.validateUserLimit(userLimit); err != nil {
		return nil, RPMDecision{}, err
	}
	return r.reserve(context.Background(), userKey, userLimit)
}

func (r *RPM) reserve(ctx context.Context, userKey string, userLimit int) (*RPMReservation, RPMDecision, error) {
	if r == nil {
		return nil, RPMDecision{}, ErrClosed
	}
	if ctx == nil {
		return nil, RPMDecision{}, ErrContextRequired
	}
	if err := validateKeyBytes(userKey, r.maxKeyBytes); err != nil {
		return nil, RPMDecision{}, err
	}
	if userLimit != 0 {
		if err := r.validateUserLimit(userLimit); err != nil {
			return nil, RPMDecision{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, RPMDecision{}, err
	}
	now := r.clock.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, RPMDecision{}, err
	}
	if r.closed {
		return nil, RPMDecision{}, ErrClosed
	}
	r.pruneLocked(now)
	if userLimit == 0 {
		userLimit = r.limits.PerUserLimit
	}
	decision := r.decisionLocked(userKey, userLimit, now)
	if !decision.Allowed {
		return nil, decision, nil
	}
	if _, exists := r.users[userKey]; !exists && len(r.users) >= r.maxUserKeys {
		decision.Allowed = false
		decision.Reason = RPMCapacity
		return nil, decision, ErrCapacity
	}
	event := r.newEventLocked(userKey, now, rpmEventActive)
	decision.GlobalCount++
	decision.UserCount++
	decision.Reason = RPMAllowed
	decision.RetryAfter = 0
	return &RPMReservation{limiter: r, event: event}, decision, nil
}

func (r *RPM) decisionLocked(userKey string, userLimit int, now time.Time) RPMDecision {
	userCount := len(r.users[userKey])
	decision := RPMDecision{
		Allowed:     true,
		Reason:      RPMAllowed,
		GlobalCount: len(r.global),
		UserCount:   userCount,
		GlobalLimit: r.limits.GlobalLimit,
		UserLimit:   userLimit,
	}
	if decision.GlobalCount >= decision.GlobalLimit {
		decision.Allowed = false
		decision.Reason = RPMGlobalLimit
		decision.RetryAfter = r.earliestExpiryLocked(r.global, now)
		return decision
	}
	if userCount >= decision.UserLimit {
		decision.Allowed = false
		decision.Reason = RPMUserLimit
		decision.RetryAfter = r.earliestExpiryLocked(r.users[userKey], now)
	}
	return decision
}

func (r *RPM) newEventLocked(userKey string, now time.Time, state rpmEventState) *rpmEvent {
	event := &rpmEvent{at: now, user: userKey}
	event.state.Store(uint32(state))
	r.global = append(r.global, event)
	r.users[userKey] = append(r.users[userKey], event)
	return event
}

func (r *RPM) earliestExpiryLocked(events []*rpmEvent, now time.Time) time.Duration {
	var earliest time.Time
	for _, event := range events {
		if event == nil || event.state.Load() == uint32(rpmEventReleased) {
			continue
		}
		expires := event.at.Add(r.window)
		if !expires.After(now) {
			continue
		}
		if earliest.IsZero() || expires.Before(earliest) {
			earliest = expires
		}
	}
	if earliest.IsZero() {
		return 0
	}
	return earliest.Sub(now)
}

func (r *RPM) pruneLocked(now time.Time) {
	cutoff := now.Add(-r.window)
	globalWrite := 0
	for _, event := range r.global {
		if event != nil && event.at.After(cutoff) && event.state.Load() != uint32(rpmEventReleased) {
			r.global[globalWrite] = event
			globalWrite++
		}
	}
	for i := globalWrite; i < len(r.global); i++ {
		r.global[i] = nil
	}
	r.global = r.global[:globalWrite]

	for userKey, events := range r.users {
		write := 0
		for _, event := range events {
			if event != nil && event.at.After(cutoff) && event.state.Load() != uint32(rpmEventReleased) {
				events[write] = event
				write++
			}
		}
		for i := write; i < len(events); i++ {
			events[i] = nil
		}
		if write == 0 {
			delete(r.users, userKey)
		} else {
			r.users[userKey] = events[:write]
		}
	}
}

func (r *RPM) releaseEvent(event *rpmEvent) {
	if r == nil || event == nil {
		return
	}
	now := r.clock.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.pruneLocked(now)
}

// RPMReservation is an idempotent, concurrency-safe reservation handle.
// Commit and Release race safely; exactly one state transition wins.
type RPMReservation struct {
	limiter *RPM
	event   *rpmEvent
}

// Commit converts an active reservation into a committed hit. It returns true
// only for the first successful state transition.
func (p *RPMReservation) Commit() bool {
	if p == nil || p.event == nil {
		return false
	}
	return p.event.state.CompareAndSwap(uint32(rpmEventActive), uint32(rpmEventCommitted))
}

// Release removes an active reservation without consuming the window budget.
// It returns true only for the first successful state transition.
func (p *RPMReservation) Release() bool {
	if p == nil || p.event == nil {
		return false
	}
	if !p.event.state.CompareAndSwap(uint32(rpmEventActive), uint32(rpmEventReleased)) {
		return false
	}
	p.limiter.releaseEvent(p.event)
	return true
}

// Cancel is an alias for Release for callers that model a failed or
// cancelled request explicitly.
func (p *RPMReservation) Cancel() bool { return p.Release() }

// Active reports whether the reservation still needs a terminal action.
func (p *RPMReservation) Active() bool {
	return p != nil && p.event != nil && p.event.state.Load() == uint32(rpmEventActive)
}

// Close releases all stored state and prevents future admissions. It is
// idempotent and does not start or wait for a background goroutine.
func (r *RPM) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	for i, event := range r.global {
		if event != nil {
			event.state.Store(uint32(rpmEventReleased))
		}
		r.global[i] = nil
	}
	r.global = nil
	for userKey, events := range r.users {
		for i := range events {
			events[i] = nil
		}
		delete(r.users, userKey)
	}
	return nil
}

// Shutdown is an alias for Close for callers that use lifecycle terminology.
func (r *RPM) Shutdown() error { return r.Close() }
