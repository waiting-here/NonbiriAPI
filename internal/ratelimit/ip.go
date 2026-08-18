package ratelimit

import (
	"sync"
	"time"
)

const (
	// DefaultIPWindow is used when IPThrottleConfig.Window is omitted.
	DefaultIPWindow = time.Minute
	// DefaultIPPenalty is used for an enabled throttle when Penalty is omitted.
	DefaultIPPenalty = time.Minute
	// DefaultOAuthStartRateLimit is the default per-client-IP admission limit
	// for the unauthenticated OAuth start endpoints (login start and
	// elevation start). Zero disables application-level admission; the
	// configured reverse-proxy limit remains the outer boundary. It is the
	// single source for the matching site_config default.
	DefaultOAuthStartRateLimit = 10
	// DefaultOAuthStartRateWindowSeconds is the default sliding-window length,
	// in whole seconds, for the OAuth start admission throttle.
	DefaultOAuthStartRateWindowSeconds = 60
	// DefaultOAuthStartRatePenaltySeconds is the default penalty, in whole
	// seconds, applied once the OAuth start admission limit is exceeded.
	DefaultOAuthStartRatePenaltySeconds = 60
)

// IPThrottleConfig configures an opaque-identity sliding-window throttle.
// Limit == 0 explicitly disables admission accounting while still requiring a
// bounded, valid identity at the API boundary. Negative limits are invalid.
type IPThrottleConfig struct {
	Limit         int
	Window        time.Duration
	Penalty       time.Duration
	MaxKeys       int
	MaxHitsPerKey int
	MaxKeyBytes   int
}

// DefaultIPThrottleConfig returns a disabled, bounded configuration. A caller
// must opt in to a positive Limit to apply a penalty.
func DefaultIPThrottleConfig() IPThrottleConfig {
	return IPThrottleConfig{
		Limit:         0,
		Window:        DefaultIPWindow,
		Penalty:       DefaultIPPenalty,
		MaxKeys:       defaultMaxKeys,
		MaxHitsPerKey: defaultMaxHits,
		MaxKeyBytes:   defaultMaxKeyBytes,
	}
}

func (c IPThrottleConfig) normalized() (IPThrottleConfig, error) {
	if c.Limit < 0 {
		return IPThrottleConfig{}, ErrInvalidConfig
	}
	if c.Window == 0 {
		c.Window = DefaultIPWindow
	}
	if err := validateDuration(c.Window, false); err != nil {
		return IPThrottleConfig{}, err
	}
	if c.Penalty == 0 {
		c.Penalty = DefaultIPPenalty
	}
	if err := validateDuration(c.Penalty, false); err != nil {
		return IPThrottleConfig{}, err
	}
	var err error
	c.MaxKeys, err = boundedInt(c.MaxKeys, defaultMaxKeys, maxConfiguredEntries)
	if err != nil {
		return IPThrottleConfig{}, err
	}
	c.MaxHitsPerKey, err = boundedInt(c.MaxHitsPerKey, defaultMaxHits, maxConfiguredEntries)
	if err != nil {
		return IPThrottleConfig{}, err
	}
	c.MaxKeyBytes, err = boundedInt(c.MaxKeyBytes, defaultMaxKeyBytes, maxConfiguredBytes)
	if err != nil {
		return IPThrottleConfig{}, err
	}
	if c.Limit > c.MaxHitsPerKey {
		return IPThrottleConfig{}, ErrInvalidConfig
	}
	return c, nil
}

// IPReason explains an IP throttle decision.
type IPReason uint8

const (
	IPAllowed IPReason = iota
	IPDisabled
	IPPenalty
	IPCapacity
)

// IPDecision is the result of one atomic Allow call.
type IPDecision struct {
	Allowed           bool
	Reason            IPReason
	Count             int
	Limit             int
	RetryAfterSeconds int
}

type ipEntry struct {
	hits       []time.Time
	blockUntil time.Time
}

// IPThrottle is a bounded per-opaque-identity sliding-window throttle. It has
// no cleanup goroutine: every operation prunes expired entries while holding
// the same mutex used for admission.
type IPThrottle struct {
	mu sync.Mutex

	clock         Clock
	limit         int
	window        time.Duration
	penalty       time.Duration
	maxKeys       int
	maxHitsPerKey int
	maxKeyBytes   int

	entries map[string]*ipEntry
	closed  bool
}

// NewIPThrottle constructs an opaque-identity throttle. It does not parse IP
// addresses and does not inspect forwarding headers.
func NewIPThrottle(config IPThrottleConfig, opts ...Option) (*IPThrottle, error) {
	config, err := config.normalized()
	if err != nil {
		return nil, err
	}
	settings, err := applyOptions(opts)
	if err != nil {
		return nil, err
	}
	return &IPThrottle{
		clock:         settings.clock,
		limit:         config.Limit,
		window:        config.Window,
		penalty:       config.Penalty,
		maxKeys:       config.MaxKeys,
		maxHitsPerKey: config.MaxHitsPerKey,
		maxKeyBytes:   config.MaxKeyBytes,
		entries:       make(map[string]*ipEntry),
	}, nil
}

// Allow atomically records one accepted attempt. The first Limit attempts in a
// window are allowed; the next attempt starts the penalty and is denied. Calls
// made during the penalty are denied without extending it.
func (t *IPThrottle) Allow(identity string) (IPDecision, error) {
	if t == nil {
		return IPDecision{}, ErrClosed
	}
	if err := validateKeyBytes(identity, t.maxKeyBytes); err != nil {
		return IPDecision{}, err
	}
	now := t.clock.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return IPDecision{}, ErrClosed
	}
	if t.limit == 0 {
		return IPDecision{Allowed: true, Reason: IPDisabled}, nil
	}
	t.purgeLocked(now)
	entry := t.entries[identity]
	if entry != nil && now.Before(entry.blockUntil) {
		return IPDecision{
			Allowed:           false,
			Reason:            IPPenalty,
			Limit:             t.limit,
			RetryAfterSeconds: retryAfterSeconds(entry.blockUntil, now),
		}, nil
	}
	if entry == nil {
		if len(t.entries) >= t.maxKeys {
			return IPDecision{Allowed: false, Reason: IPCapacity, Limit: t.limit}, ErrCapacity
		}
		entry = &ipEntry{}
		t.entries[identity] = entry
	}
	if len(entry.hits) >= t.limit {
		entry.hits = nil
		entry.blockUntil = now.Add(t.penalty)
		return IPDecision{
			Allowed:           false,
			Reason:            IPPenalty,
			Count:             t.limit + 1,
			Limit:             t.limit,
			RetryAfterSeconds: retryAfterSeconds(entry.blockUntil, now),
		}, nil
	}
	entry.hits = append(entry.hits, now)
	return IPDecision{
		Allowed: true,
		Reason:  IPAllowed,
		Count:   len(entry.hits),
		Limit:   t.limit,
	}, nil
}

// RetryAfterSeconds returns the current penalty's conservative retry value.
// It returns zero when the identity is not penalized and never returns less
// than one while a penalty is active.
func (t *IPThrottle) RetryAfterSeconds(identity string) (int, error) {
	if t == nil {
		return 0, ErrClosed
	}
	if err := validateKeyBytes(identity, t.maxKeyBytes); err != nil {
		return 0, err
	}
	now := t.clock.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, ErrClosed
	}
	if t.limit == 0 {
		return 0, nil
	}
	t.purgeLocked(now)
	if entry := t.entries[identity]; entry != nil && now.Before(entry.blockUntil) {
		return retryAfterSeconds(entry.blockUntil, now), nil
	}
	return 0, nil
}

// RetryAfter returns the retry value as a duration rounded up to whole
// seconds, suitable for an HTTP Retry-After header.
func (t *IPThrottle) RetryAfter(identity string) (time.Duration, error) {
	seconds, err := t.RetryAfterSeconds(identity)
	if err != nil || seconds == 0 {
		return 0, err
	}
	return time.Duration(seconds) * time.Second, nil
}

// Purge removes expired state using the injected clock. It is safe to call
// concurrently with Allow and is mainly useful for an explicit maintenance
// hook; ordinary operations already perform the same cleanup.
func (t *IPThrottle) Purge() error {
	if t == nil {
		return ErrClosed
	}
	now := t.clock.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	t.purgeLocked(now)
	return nil
}

func (t *IPThrottle) purgeLocked(now time.Time) {
	cutoff := now.Add(-t.window)
	for identity, entry := range t.entries {
		if !entry.blockUntil.IsZero() {
			if now.Before(entry.blockUntil) {
				continue
			}
			entry.blockUntil = time.Time{}
			entry.hits = nil
		}
		write := 0
		for _, hit := range entry.hits {
			if hit.After(cutoff) {
				entry.hits[write] = hit
				write++
			}
		}
		for i := write; i < len(entry.hits); i++ {
			entry.hits[i] = time.Time{}
		}
		if write == 0 {
			delete(t.entries, identity)
		} else {
			entry.hits = entry.hits[:write]
		}
	}
}

// Close clears all state and prevents future admissions. It is idempotent and
// does not own a background goroutine.
func (t *IPThrottle) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	for identity, entry := range t.entries {
		for i := range entry.hits {
			entry.hits[i] = time.Time{}
		}
		delete(t.entries, identity)
	}
	return nil
}

// Config returns a consistent snapshot of the active rate parameters
// (limit, window, penalty). It is the live-apply read companion to
// Reconfigure: a runtime applier reads the current parameters, mutates the
// single key being applied, and calls Reconfigure so the other two are
// preserved. The bounded-store parameters (MaxKeys/MaxHitsPerKey/MaxKeyBytes)
// are construction-time only and are intentionally not exposed here.
func (t *IPThrottle) Config() IPThrottleConfig {
	if t == nil {
		return IPThrottleConfig{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return IPThrottleConfig{
		Limit:   t.limit,
		Window:  t.window,
		Penalty: t.penalty,
	}
}

// Reconfigure atomically changes the limit, window, and penalty of an
// existing throttle without resetting admission state. It is the live-apply
// hook used by the site-config runtime applier: a validated change takes
// effect for the next Allow call while in-flight penalties and existing
// window counters are preserved (mirroring the SetLimits contract of the RPM
// limiter: a lowered cap waits for old hits to leave the window rather than
// discarding them). The bounded-store parameters stay at their construction
// values; only the operator-tunable rate parameters are changed.
//
// As with NewIPThrottle, Limit == 0 disables admission accounting (every
// Allow returns IPDisabled), a zero Window falls back to DefaultIPWindow, and
// a zero Penalty falls back to DefaultIPPenalty. A Limit larger than the
// construction-time MaxHitsPerKey is rejected so a live update can never
// exceed the bounded per-key hit store.
func (t *IPThrottle) Reconfigure(config IPThrottleConfig) error {
	if t == nil {
		return ErrClosed
	}
	if config.Limit < 0 {
		return ErrInvalidConfig
	}
	if config.Window == 0 {
		config.Window = DefaultIPWindow
	}
	if err := validateDuration(config.Window, false); err != nil {
		return err
	}
	if config.Penalty == 0 {
		config.Penalty = DefaultIPPenalty
	}
	if err := validateDuration(config.Penalty, false); err != nil {
		return err
	}
	now := t.clock.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	if config.Limit > t.maxHitsPerKey {
		return ErrInvalidConfig
	}
	t.limit = config.Limit
	t.window = config.Window
	t.penalty = config.Penalty
	t.purgeLocked(now)
	return nil
}

// SetConfig is an alias for Reconfigure for callers that model the live update
// as a configuration assignment.
func (t *IPThrottle) SetConfig(config IPThrottleConfig) error { return t.Reconfigure(config) }

// Shutdown is an alias for Close.
func (t *IPThrottle) Shutdown() error { return t.Close() }
