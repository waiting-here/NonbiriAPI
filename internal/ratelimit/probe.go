package ratelimit

import (
	"sync"
	"time"
)

const (
	// DefaultProbeWindow is used when ProbeLimiterConfig.Window is omitted.
	DefaultProbeWindow = time.Minute
	// DefaultProbeLimitPerUser is the default attempt cap for one user.
	DefaultProbeLimitPerUser = 5
)

// ProbeLimiterConfig configures a per-user sliding-window attempt limiter.
// Every admitted Allow call consumes one attempt before the caller performs
// the probe, so a later probe failure is still counted.
type ProbeLimiterConfig struct {
	Window             time.Duration
	DefaultLimit       int
	MaxUsers           int
	MaxAttemptsPerUser int
}

// DefaultProbeLimiterConfig returns the finite default probe policy.
func DefaultProbeLimiterConfig() ProbeLimiterConfig {
	return ProbeLimiterConfig{
		Window:             DefaultProbeWindow,
		DefaultLimit:       DefaultProbeLimitPerUser,
		MaxUsers:           defaultMaxKeys,
		MaxAttemptsPerUser: defaultMaxHits,
	}
}

func (c ProbeLimiterConfig) normalized() (ProbeLimiterConfig, error) {
	if c.Window == 0 {
		c.Window = DefaultProbeWindow
	}
	if err := validateDuration(c.Window, false); err != nil {
		return ProbeLimiterConfig{}, err
	}
	if c.DefaultLimit == 0 {
		c.DefaultLimit = DefaultProbeLimitPerUser
	}
	if c.DefaultLimit < 1 {
		return ProbeLimiterConfig{}, ErrInvalidConfig
	}
	var err error
	c.MaxUsers, err = boundedInt(c.MaxUsers, defaultMaxKeys, maxConfiguredEntries)
	if err != nil {
		return ProbeLimiterConfig{}, err
	}
	c.MaxAttemptsPerUser, err = boundedInt(c.MaxAttemptsPerUser, defaultMaxHits, maxConfiguredEntries)
	if err != nil {
		return ProbeLimiterConfig{}, err
	}
	if c.DefaultLimit > c.MaxAttemptsPerUser {
		return ProbeLimiterConfig{}, ErrInvalidConfig
	}
	return c, nil
}

// ProbeReason explains a probe decision.
type ProbeReason uint8

const (
	ProbeAllowed ProbeReason = iota
	ProbeLimit
	ProbeCapacity
)

// ProbeDecision is a consistent snapshot of one user's probe budget.
type ProbeDecision struct {
	Allowed    bool
	Reason     ProbeReason
	Count      int
	Limit      int
	RetryAfter time.Duration
}

// ProbeLimiter is a bounded per-user sliding-window limiter. It does not use a
// global counter, so one user's probes cannot consume another user's budget.
type ProbeLimiter struct {
	mu sync.Mutex

	clock              Clock
	window             time.Duration
	defaultLimit       int
	maxUsers           int
	maxAttemptsPerUser int

	hits   map[int64][]time.Time
	closed bool
}

// NewProbeLimiter constructs a per-user probe limiter without a background
// cleanup goroutine.
func NewProbeLimiter(config ProbeLimiterConfig, opts ...Option) (*ProbeLimiter, error) {
	config, err := config.normalized()
	if err != nil {
		return nil, err
	}
	settings, err := applyOptions(opts)
	if err != nil {
		return nil, err
	}
	return &ProbeLimiter{
		clock:              settings.clock,
		window:             config.Window,
		defaultLimit:       config.DefaultLimit,
		maxUsers:           config.MaxUsers,
		maxAttemptsPerUser: config.MaxAttemptsPerUser,
		hits:               make(map[int64][]time.Time),
	}, nil
}

func validateUserID(userID int64) error {
	if userID <= 0 {
		return ErrInvalidKey
	}
	return nil
}

func (p *ProbeLimiter) limitOrDefault(limit int) (int, error) {
	if limit <= 0 {
		limit = p.defaultLimit
	}
	if limit < 1 || limit > p.maxAttemptsPerUser {
		return 0, ErrInvalidConfig
	}
	return limit, nil
}

// Check reports whether a user may start another probe without recording one.
// Enforcing callers should use Allow for an atomic check-and-record.
func (p *ProbeLimiter) Check(userID int64, limit int) (ProbeDecision, error) {
	if p == nil {
		return ProbeDecision{}, ErrClosed
	}
	if err := validateUserID(userID); err != nil {
		return ProbeDecision{}, err
	}
	limit, err := p.limitOrDefault(limit)
	if err != nil {
		return ProbeDecision{}, err
	}
	now := p.clock.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ProbeDecision{}, ErrClosed
	}
	p.purgeLocked(now)
	count := len(p.hits[userID])
	if count >= limit {
		return ProbeDecision{
			Allowed:    false,
			Reason:     ProbeLimit,
			Count:      count,
			Limit:      limit,
			RetryAfter: p.earliestExpiryLocked(p.hits[userID], now),
		}, nil
	}
	if _, exists := p.hits[userID]; !exists && len(p.hits) >= p.maxUsers {
		return ProbeDecision{Allowed: false, Reason: ProbeCapacity, Limit: limit}, ErrCapacity
	}
	return ProbeDecision{Allowed: true, Reason: ProbeAllowed, Count: count, Limit: limit}, nil
}

// Allow atomically admits and records one attempt. The attempt is counted even
// if the caller's later network probe fails. A denied attempt is not recorded.
// A non-positive per-call limit selects the configured default.
func (p *ProbeLimiter) Allow(userID int64, limit int) (ProbeDecision, error) {
	if p == nil {
		return ProbeDecision{}, ErrClosed
	}
	if err := validateUserID(userID); err != nil {
		return ProbeDecision{}, err
	}
	limit, err := p.limitOrDefault(limit)
	if err != nil {
		return ProbeDecision{}, err
	}
	now := p.clock.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ProbeDecision{}, ErrClosed
	}
	p.purgeLocked(now)
	records := p.hits[userID]
	if len(records) >= limit {
		return ProbeDecision{
			Allowed:    false,
			Reason:     ProbeLimit,
			Count:      len(records),
			Limit:      limit,
			RetryAfter: p.earliestExpiryLocked(records, now),
		}, nil
	}
	if records == nil {
		if len(p.hits) >= p.maxUsers {
			return ProbeDecision{Allowed: false, Reason: ProbeCapacity, Limit: limit}, ErrCapacity
		}
	}
	records = append(records, now)
	p.hits[userID] = records
	return ProbeDecision{
		Allowed: true,
		Reason:  ProbeAllowed,
		Count:   len(records),
		Limit:   limit,
	}, nil
}

func (p *ProbeLimiter) earliestExpiryLocked(records []time.Time, now time.Time) time.Duration {
	var earliest time.Time
	for _, record := range records {
		expires := record.Add(p.window)
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

func (p *ProbeLimiter) purgeLocked(now time.Time) {
	cutoff := now.Add(-p.window)
	for userID, records := range p.hits {
		write := 0
		for _, record := range records {
			if record.After(cutoff) {
				records[write] = record
				write++
			}
		}
		for i := write; i < len(records); i++ {
			records[i] = time.Time{}
		}
		if write == 0 {
			delete(p.hits, userID)
		} else {
			p.hits[userID] = records[:write]
		}
	}
}

// Purge removes expired attempts using the injected clock.
func (p *ProbeLimiter) Purge() error {
	if p == nil {
		return ErrClosed
	}
	now := p.clock.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	p.purgeLocked(now)
	return nil
}

// Close clears all state and prevents future admissions. It is idempotent and
// does not start or wait for a background goroutine.
func (p *ProbeLimiter) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	for userID, records := range p.hits {
		for i := range records {
			records[i] = time.Time{}
		}
		delete(p.hits, userID)
	}
	return nil
}

// Shutdown is an alias for Close.
func (p *ProbeLimiter) Shutdown() error { return p.Close() }
