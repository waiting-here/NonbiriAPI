package ratelimit

import (
	"strings"
	"sync"
	"time"
)

const (
	// DefaultLoginFailureWindow is used when LoginThrottleConfig.Window is
	// omitted.
	DefaultLoginFailureWindow = 15 * time.Minute
	// DefaultLoginLockDuration is used when LockDuration is omitted.
	DefaultLoginLockDuration = 15 * time.Minute
	// DefaultLoginMaxFailures is the threshold exposed by the default config.
	DefaultLoginMaxFailures = 5
)

// LoginThrottleConfig bounds failed-login state for each normalized pair of
// client identity and username.
type LoginThrottleConfig struct {
	MaxFailures       int
	Window            time.Duration
	LockDuration      time.Duration
	MaxEntries        int
	MaxFailuresPerKey int
	MaxComponentBytes int
}

// DefaultLoginThrottleConfig returns a finite, in-memory login-failure
// configuration.
func DefaultLoginThrottleConfig() LoginThrottleConfig {
	return LoginThrottleConfig{
		MaxFailures:       DefaultLoginMaxFailures,
		Window:            DefaultLoginFailureWindow,
		LockDuration:      DefaultLoginLockDuration,
		MaxEntries:        defaultMaxKeys,
		MaxFailuresPerKey: 64,
		MaxComponentBytes: defaultMaxKeyBytes,
	}
}

func (c LoginThrottleConfig) normalized() (LoginThrottleConfig, error) {
	if c.MaxFailures == 0 {
		c.MaxFailures = DefaultLoginMaxFailures
	}
	if c.MaxFailures < 1 {
		return LoginThrottleConfig{}, ErrInvalidConfig
	}
	if c.Window == 0 {
		c.Window = DefaultLoginFailureWindow
	}
	if err := validateDuration(c.Window, false); err != nil {
		return LoginThrottleConfig{}, err
	}
	if c.LockDuration == 0 {
		c.LockDuration = DefaultLoginLockDuration
	}
	if err := validateDuration(c.LockDuration, false); err != nil {
		return LoginThrottleConfig{}, err
	}
	var err error
	c.MaxEntries, err = boundedInt(c.MaxEntries, defaultMaxKeys, maxConfiguredEntries)
	if err != nil {
		return LoginThrottleConfig{}, err
	}
	c.MaxFailuresPerKey, err = boundedInt(c.MaxFailuresPerKey, 64, maxConfiguredEntries)
	if err != nil {
		return LoginThrottleConfig{}, err
	}
	c.MaxComponentBytes, err = boundedInt(c.MaxComponentBytes, defaultMaxKeyBytes, maxConfiguredBytes)
	if err != nil {
		return LoginThrottleConfig{}, err
	}
	if c.MaxFailures > c.MaxFailuresPerKey {
		return LoginThrottleConfig{}, ErrInvalidConfig
	}
	return c, nil
}

// LoginReason explains a login throttle decision.
type LoginReason uint8

const (
	LoginAllowed LoginReason = iota
	LoginLocked
	LoginCapacity
)

// LoginDecision is a snapshot for one normalized identity pair. FailureCount
// counts failures still retained in the current window; after a lock is
// created, the count is reset while Locked and RetryAfterSeconds describe the
// penalty.
type LoginDecision struct {
	Allowed           bool
	Locked            bool
	Reason            LoginReason
	FailureCount      int
	RetryAfterSeconds int
}

type loginKey struct {
	identity string
	username string
}

type loginEntry struct {
	failures    []time.Time
	lockedUntil time.Time
}

// LoginThrottle tracks failed attempts by a normalized pair. The client
// identity is accepted as an opaque value; parsing addresses and trusting
// forwarding headers belongs to the caller's host layer.
type LoginThrottle struct {
	mu sync.Mutex

	clock             Clock
	window            time.Duration
	lockDuration      time.Duration
	maxFailures       int
	maxEntries        int
	maxFailuresPerKey int
	maxComponentBytes int

	entries map[loginKey]*loginEntry
	closed  bool
}

// NewLoginThrottle constructs a bounded failed-login throttle without a
// cleanup goroutine.
func NewLoginThrottle(config LoginThrottleConfig, opts ...Option) (*LoginThrottle, error) {
	config, err := config.normalized()
	if err != nil {
		return nil, err
	}
	settings, err := applyOptions(opts)
	if err != nil {
		return nil, err
	}
	return &LoginThrottle{
		clock:             settings.clock,
		window:            config.Window,
		lockDuration:      config.LockDuration,
		maxFailures:       config.MaxFailures,
		maxEntries:        config.MaxEntries,
		maxFailuresPerKey: config.MaxFailuresPerKey,
		maxComponentBytes: config.MaxComponentBytes,
		entries:           make(map[loginKey]*loginEntry),
	}, nil
}

func (t *LoginThrottle) normalize(identity, username string) (loginKey, error) {
	identity, err := normalizeLoginComponent(identity, t.maxComponentBytes)
	if err != nil {
		return loginKey{}, err
	}
	username, err = normalizeLoginComponent(username, t.maxComponentBytes)
	if err != nil {
		return loginKey{}, err
	}
	username = strings.ToLower(username)
	if len(username) > t.maxComponentBytes {
		return loginKey{}, ErrInvalidKey
	}
	return loginKey{identity: identity, username: username}, nil
}

// Check reports whether authentication for the pair is currently locked. It
// also lazily removes expired failure state.
func (t *LoginThrottle) Check(identity, username string) (LoginDecision, error) {
	if t == nil {
		return LoginDecision{}, ErrClosed
	}
	key, err := t.normalize(identity, username)
	if err != nil {
		return LoginDecision{}, err
	}
	now := t.clock.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return LoginDecision{}, ErrClosed
	}
	t.purgeLocked(now)
	entry := t.entries[key]
	if entry != nil && now.Before(entry.lockedUntil) {
		return t.lockedDecision(entry, now), nil
	}
	if entry == nil && len(t.entries) >= t.maxEntries {
		return LoginDecision{Allowed: false, Locked: true, Reason: LoginCapacity}, ErrCapacity
	}
	decision := LoginDecision{Allowed: true, Reason: LoginAllowed}
	if entry != nil {
		decision.FailureCount = len(entry.failures)
	}
	return decision, nil
}

// Locked is a convenience view of Check.
func (t *LoginThrottle) Locked(identity, username string) (bool, error) {
	decision, err := t.Check(identity, username)
	return decision.Locked, err
}

// Failure atomically records one failed authentication. The threshold failure
// creates a lock; failures arriving during an existing lock do not extend it.
func (t *LoginThrottle) Failure(identity, username string) (LoginDecision, error) {
	if t == nil {
		return LoginDecision{}, ErrClosed
	}
	key, err := t.normalize(identity, username)
	if err != nil {
		return LoginDecision{}, err
	}
	now := t.clock.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return LoginDecision{}, ErrClosed
	}
	t.purgeLocked(now)
	entry := t.entries[key]
	if entry != nil && now.Before(entry.lockedUntil) {
		return t.lockedDecision(entry, now), nil
	}
	if entry == nil {
		if len(t.entries) >= t.maxEntries {
			return LoginDecision{Allowed: false, Locked: true, Reason: LoginCapacity}, ErrCapacity
		}
		entry = &loginEntry{}
		t.entries[key] = entry
	}
	entry.failures = append(entry.failures, now)
	if len(entry.failures) >= t.maxFailures {
		for i := range entry.failures {
			entry.failures[i] = time.Time{}
		}
		entry.failures = nil
		entry.lockedUntil = now.Add(t.lockDuration)
		return t.lockedDecision(entry, now), nil
	}
	return LoginDecision{
		Allowed:      true,
		Reason:       LoginAllowed,
		FailureCount: len(entry.failures),
	}, nil
}

// Success clears all failure and lock state for the normalized pair. It is
// safe to call after a missing entry and is idempotent.
func (t *LoginThrottle) Success(identity, username string) error {
	if t == nil {
		return ErrClosed
	}
	key, err := t.normalize(identity, username)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	if entry := t.entries[key]; entry != nil {
		for i := range entry.failures {
			entry.failures[i] = time.Time{}
		}
	}
	delete(t.entries, key)
	return nil
}

// RetryAfterSeconds returns the conservative whole-second lock duration for a
// pair, or zero when it is not locked.
func (t *LoginThrottle) RetryAfterSeconds(identity, username string) (int, error) {
	decision, err := t.Check(identity, username)
	if err != nil {
		return 0, err
	}
	return decision.RetryAfterSeconds, nil
}

func (t *LoginThrottle) lockedDecision(entry *loginEntry, now time.Time) LoginDecision {
	return LoginDecision{
		Allowed:           false,
		Locked:            true,
		Reason:            LoginLocked,
		RetryAfterSeconds: retryAfterSeconds(entry.lockedUntil, now),
	}
}

func (t *LoginThrottle) purgeLocked(now time.Time) {
	cutoff := now.Add(-t.window)
	for key, entry := range t.entries {
		if !entry.lockedUntil.IsZero() {
			if now.Before(entry.lockedUntil) {
				continue
			}
			entry.lockedUntil = time.Time{}
		}
		write := 0
		for _, failure := range entry.failures {
			if failure.After(cutoff) {
				entry.failures[write] = failure
				write++
			}
		}
		for i := write; i < len(entry.failures); i++ {
			entry.failures[i] = time.Time{}
		}
		if write == 0 {
			delete(t.entries, key)
		} else {
			entry.failures = entry.failures[:write]
		}
	}
}

// Purge removes expired lock and failure state using the injected clock.
func (t *LoginThrottle) Purge() error {
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

// Close clears all state and prevents future admissions. It is idempotent and
// does not start or wait for a background goroutine.
func (t *LoginThrottle) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	for key, entry := range t.entries {
		for i := range entry.failures {
			entry.failures[i] = time.Time{}
		}
		delete(t.entries, key)
	}
	return nil
}

// Shutdown is an alias for Close.
func (t *LoginThrottle) Shutdown() error { return t.Close() }
