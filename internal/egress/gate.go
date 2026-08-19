package egress

import (
	"context"
	"errors"
	"sync"
)

const (
	// GlobalConcurrencyConfigKey is the administrator runtime setting for the
	// total number of outbound requests in flight across every connector.
	GlobalConcurrencyConfigKey = "egress_global_concurrency"
	// PerEndpointConcurrencyConfigKey is the administrator runtime setting for
	// requests in flight per canonical endpoint base URL.
	PerEndpointConcurrencyConfigKey = "default_per_endpoint_concurrency"

	// Defaults are deliberately finite even before runtime settings are saved.
	DefaultGlobalConcurrency      = 32
	DefaultPerEndpointConcurrency = 8
)

// ConcurrencyLimits are process-wide limits enforced atomically by one Gate.
// Existing work is allowed to finish after a limit is lowered; new work waits
// until both counters are below the new limits.
type ConcurrencyLimits struct {
	Global      int
	PerEndpoint int
}

// DefaultConcurrencyLimits returns the finite values used when the matching
// runtime settings have not yet been persisted.
func DefaultConcurrencyLimits() ConcurrencyLimits {
	return ConcurrencyLimits{
		Global:      DefaultGlobalConcurrency,
		PerEndpoint: DefaultPerEndpointConcurrency,
	}
}

func (l ConcurrencyLimits) validate() error {
	if l.Global < 1 {
		return errors.New("egress global concurrency must be positive")
	}
	if l.PerEndpoint < 1 {
		return errors.New("egress per-endpoint concurrency must be positive")
	}
	return nil
}

// Gate atomically acquires a global and per-endpoint slot. A single Gate is
// owned by Stack and therefore shared by every client and connector. Waits are
// context-cancellable and never hold one slot while waiting for the other.
type Gate struct {
	mu sync.Mutex

	limits       ConcurrencyLimits
	globalActive int
	perActive    map[string]int
	notify       chan struct{}
}

// NewGate constructs a process-wide outbound concurrency gate.
func NewGate(limits ConcurrencyLimits) (*Gate, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	return &Gate{
		limits:    limits,
		perActive: make(map[string]int),
		notify:    make(chan struct{}),
	}, nil
}

// SetLimits applies updated administrator settings without replacing the gate
// or splitting counters across callers.
func (g *Gate) SetLimits(limits ConcurrencyLimits) error {
	if err := limits.validate(); err != nil {
		return err
	}
	g.mu.Lock()
	g.limits = limits
	g.broadcastLocked()
	g.mu.Unlock()
	return nil
}

// Limits returns a consistent snapshot of the current configured limits.
func (g *Gate) Limits() ConcurrencyLimits {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.limits
}

// Acquire waits for both a global and canonical-base-URL slot. The input is
// canonicalized again at the gate boundary so equivalent default ports, host
// case, and trailing-dot forms cannot create independent counters.
func (g *Gate) Acquire(ctx context.Context, baseURL string) (*Permit, error) {
	if ctx == nil {
		return nil, errors.New("egress concurrency context is required")
	}
	canonical, _, _, _, err := canonicalizeBaseURL(baseURL, false)
	if err != nil {
		return nil, err
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		g.mu.Lock()
		if ctxErr := ctx.Err(); ctxErr != nil {
			g.mu.Unlock()
			return nil, ctxErr
		}
		if g.globalActive < g.limits.Global && g.perActive[canonical] < g.limits.PerEndpoint {
			g.globalActive++
			g.perActive[canonical]++
			g.mu.Unlock()
			return &Permit{gate: g, key: canonical}, nil
		}
		notify := g.notify
		g.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-notify:
		}
	}
}

func (g *Gate) release(key string) {
	g.mu.Lock()
	if g.globalActive <= 0 || g.perActive[key] <= 0 {
		g.mu.Unlock()
		panic("egress concurrency permit accounting underflow")
	}
	g.globalActive--
	g.perActive[key]--
	if g.perActive[key] == 0 {
		delete(g.perActive, key)
	}
	g.broadcastLocked()
	g.mu.Unlock()
}

func (g *Gate) broadcastLocked() {
	close(g.notify)
	g.notify = make(chan struct{})
}

// Permit represents one global and one per-endpoint slot. Release is
// idempotent so cancellation, EOF, and an explicit response-body Close may
// safely converge on the same cleanup path.
type Permit struct {
	gate *Gate
	key  string
	once sync.Once
}

// Release returns both slots to the shared gate.
func (p *Permit) Release() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		p.gate.release(p.key)
	})
}
