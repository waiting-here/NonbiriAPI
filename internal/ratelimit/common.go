package ratelimit

import (
	"errors"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrClosed indicates that the limiter has been closed and no new state can
	// be admitted.
	ErrClosed = errors.New("ratelimit is closed")
	// ErrInvalidKey indicates an empty, malformed, or overlong identity.
	ErrInvalidKey = errors.New("ratelimit key is invalid")
	// ErrCapacity indicates that the bounded state store cannot admit another
	// distinct identity. Callers should fail closed rather than evicting a
	// live identity.
	ErrCapacity = errors.New("ratelimit capacity exhausted")
	// ErrInvalidConfig indicates a non-finite or otherwise unsupported limit
	// configuration.
	ErrInvalidConfig = errors.New("ratelimit configuration is invalid")
	// ErrContextRequired indicates that a reservation was called without a
	// context.
	ErrContextRequired = errors.New("ratelimit context is required")
)

const (
	defaultMaxKeys       = 4096
	defaultMaxEvents     = 4096
	defaultMaxKeyBytes   = 256
	defaultMaxHits       = 4096
	maxConfiguredEntries = 1 << 20
	maxConfiguredBytes   = 4096
	maxRetryAfterSeconds = int64(math.MaxInt)
)

// Clock supplies the current time to a limiter. A clock is injected primarily
// so callers can test exact window boundaries without sleeping.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

// Now implements Clock.
func (f ClockFunc) Now() time.Time { return f() }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type options struct {
	clock Clock
}

// Option customizes the clock used by a limiter.
type Option func(*options) error

// WithClock injects a clock. The clock must be non-nil and safe for concurrent
// calls when the limiter itself is used concurrently.
func WithClock(clock Clock) Option {
	return func(o *options) error {
		if clock == nil {
			return ErrInvalidConfig
		}
		if function, ok := clock.(ClockFunc); ok && function == nil {
			return ErrInvalidConfig
		}
		o.clock = clock
		return nil
	}
}

// WithClockFunc injects a function-backed clock.
func WithClockFunc(now func() time.Time) Option {
	if now == nil {
		return func(*options) error { return ErrInvalidConfig }
	}
	return WithClock(ClockFunc(now))
}

func applyOptions(opts []Option) (options, error) {
	out := options{clock: realClock{}}
	for _, opt := range opts {
		if opt == nil {
			return options{}, ErrInvalidConfig
		}
		if err := opt(&out); err != nil {
			return options{}, err
		}
	}
	return out, nil
}

func validateDuration(value time.Duration, allowZero bool) error {
	if allowZero {
		if value < 0 {
			return ErrInvalidConfig
		}
		return nil
	}
	if value <= 0 {
		return ErrInvalidConfig
	}
	return nil
}

func boundedInt(value, fallback, maximum int) (int, error) {
	if value == 0 {
		value = fallback
	}
	if value < 1 || value > maximum {
		return 0, ErrInvalidConfig
	}
	return value, nil
}

func validateKeyBytes(key string, maxBytes int) error {
	if key == "" || len(key) > maxBytes || !utf8.ValidString(key) {
		return ErrInvalidKey
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return ErrInvalidKey
		}
	}
	return nil
}

func normalizeLoginComponent(raw string, maxBytes int) (string, error) {
	if raw == "" || len(raw) > maxBytes || !utf8.ValidString(raw) {
		return "", ErrInvalidKey
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", ErrInvalidKey
		}
	}
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > maxBytes {
		return "", ErrInvalidKey
	}
	return value, nil
}

func retryAfterSeconds(until, now time.Time) int {
	remaining := until.Sub(now)
	if remaining <= 0 {
		return 0
	}
	seconds := int64(remaining / time.Second)
	if remaining%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	if seconds > maxRetryAfterSeconds {
		return int(maxRetryAfterSeconds)
	}
	return int(seconds)
}

func retryAfterDuration(until, now time.Time) time.Duration {
	seconds := retryAfterSeconds(until, now)
	if seconds == 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
