package lifecyclegate

import (
	"context"
	"sync/atomic"
)

// IdentityResolver returns an internal stable key for an extant account.
// Authentication is rechecked inside the subsequent identity and user lease.
type IdentityResolver func(context.Context, int64) ([32]byte, error)

type identityState struct {
	active   map[*lease]struct{}
	changing bool
	wait     *retirementWait
}

// IdentityChange serializes registration and retirement across account IDs.
// Commit releases the stable identity so a later registration can proceed;
// it never resurrects credentials or permanently retires the provider identity.
type IdentityChange struct {
	gate  *Gate
	key   [32]byte
	state *identityState
	done  atomic.Bool
}

func (g *Gate) BeginIdentityChange(ctx context.Context, key [32]byte) (*IdentityChange, error) {
	if g == nil || ctx == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, ErrClosed
	}
	state := g.identities[key]
	if state != nil && state.changing {
		g.mu.Unlock()
		return nil, ErrRetiring
	}
	if state == nil {
		if len(g.identities) >= g.maxUsers {
			g.mu.Unlock()
			return nil, ErrCapacity
		}
		state = &identityState{active: make(map[*lease]struct{})}
		g.identities[key] = state
	}
	excluded := leaseFromContext(ctx)
	if excluded != nil && (excluded.gate != g || excluded.identity != state || excluded.released.Load()) {
		excluded = nil
	}
	wait := &retirementWait{done: make(chan struct{}), excluded: excluded}
	cancels := make([]context.CancelFunc, 0, len(state.active))
	for l := range state.active {
		if l != excluded {
			wait.remaining++
			cancels = append(cancels, l.cancel)
		}
	}
	state.changing = true
	state.wait = wait
	if wait.remaining == 0 {
		wait.closed = true
		close(wait.done)
	}
	change := &IdentityChange{gate: g, key: key, state: state}
	g.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	select {
	case <-wait.done:
		if err := ctx.Err(); err != nil {
			change.Abort()
			return nil, err
		}
		return change, nil
	case <-ctx.Done():
		change.Abort()
		return nil, ctx.Err()
	}
}

func (change *IdentityChange) Commit() bool { return change.release() }
func (change *IdentityChange) Abort() bool  { return change.release() }

func (change *IdentityChange) release() bool {
	if change == nil || change.gate == nil || !change.done.CompareAndSwap(false, true) {
		return false
	}
	g := change.gate
	g.mu.Lock()
	if g.identities[change.key] == change.state {
		change.state.changing = false
		change.state.wait = nil
		if len(change.state.active) == 0 {
			delete(g.identities, change.key)
		}
	}
	g.mu.Unlock()
	return true
}
