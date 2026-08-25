// Package lifecyclegate coordinates process-local user request admission with
// account retirement. A lease owns a cancellation context for one already
// authenticated request; retirement closes admission, cancels existing leases,
// and waits for every lease except an explicitly retiring request to leave.
package lifecyclegate

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrClosed   = errors.New("lifecycle gate is closed")
	ErrInvalid  = errors.New("lifecycle gate user is invalid")
	ErrRetiring = errors.New("lifecycle gate user is retiring")
	ErrCapacity = errors.New("lifecycle gate capacity reached")
)

const DefaultMaxUsers = 100000

// Validator is run while the request owns its user read lease. It must repeat
// the exact credential check used by the preceding authentication lookup and
// must not renew, delete, or otherwise mutate credentials.
type Validator func(context.Context, int64, string) (bool, error)

// Config controls the bounded process-local state table.
type Config struct {
	MaxUsers int
}

// Gate is one process-wide lifecycle admission boundary shared by browser
// sessions and CallerKeys. It never stores raw credentials.
type Gate struct {
	mu       sync.Mutex
	users    map[int64]*userState
	maxUsers int
	closed   bool
}

type userState struct {
	active     map[*lease]struct{}
	retiring   bool
	retireWait *retirementWait
}

type retirementWait struct {
	remaining int
	done      chan struct{}
	closed    bool
	excluded  *lease
}

type lease struct {
	gate     *Gate
	state    *userState
	ctx      context.Context
	cancel   context.CancelFunc
	userID   int64
	released atomic.Bool
}

// New creates a bounded lifecycle gate.
func New(config Config) (*Gate, error) {
	maxUsers := config.MaxUsers
	if maxUsers == 0 {
		maxUsers = DefaultMaxUsers
	}
	if maxUsers < 1 || maxUsers > DefaultMaxUsers {
		return nil, ErrCapacity
	}
	return &Gate{users: make(map[int64]*userState), maxUsers: maxUsers}, nil
}

// Admit establishes a cancellable user request context and then repeats the
// exact credential check inside the read lease. A false validation result is
// indistinguishable from an invalid credential to callers.
func (g *Gate) Admit(ctx context.Context, userID int64, binding string, validate Validator) (context.Context, func(), error) {
	if g == nil || ctx == nil || userID <= 0 || binding == "" || validate == nil {
		return nil, nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	lease, err := g.acquire(ctx, userID, binding, validate)
	if err != nil {
		return nil, nil, err
	}
	return lease.ctx, lease.Release, nil
}

func (g *Gate) acquire(parent context.Context, userID int64, binding string, validate Validator) (*lease, error) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, ErrClosed
	}
	state := g.users[userID]
	if state == nil {
		if len(g.users) >= g.maxUsers {
			g.mu.Unlock()
			return nil, ErrCapacity
		}
		state = &userState{active: make(map[*lease]struct{})}
		g.users[userID] = state
	}
	if state.retiring {
		g.mu.Unlock()
		return nil, ErrRetiring
	}
	cancelCtx, cancel := context.WithCancel(parent)
	l := &lease{gate: g, state: state, ctx: cancelCtx, cancel: cancel, userID: userID}
	// The private marker lets a context-aware retirement exclude the request
	// that is itself applying an automatic ban after its admission decision.
	l.ctx = context.WithValue(cancelCtx, leaseContextKey{}, l)
	state.active[l] = struct{}{}
	g.mu.Unlock()

	ok, err := validate(l.ctx, userID, binding)
	if err != nil {
		l.Release()
		return nil, err
	}
	if !ok {
		l.Release()
		return nil, ErrInvalid
	}
	if err := l.ctx.Err(); err != nil {
		l.Release()
		return nil, err
	}
	return l, nil
}

type leaseContextKey struct{}

func leaseFromContext(ctx context.Context) *lease {
	if ctx == nil {
		return nil
	}
	l, _ := ctx.Value(leaseContextKey{}).(*lease)
	return l
}

// Release relinquishes one user read lease. It is safe to call more than once.
func (l *lease) Release() {
	if l == nil || !l.released.CompareAndSwap(false, true) {
		return
	}
	l.cancel()
	g := l.gate
	if g == nil {
		return
	}
	g.mu.Lock()
	delete(l.state.active, l)
	if wait := l.state.retireWait; wait != nil && !wait.closed {
		// A context-aware retirement does not wait for its excluded lease.
		if wait.excluded != l && wait.remaining > 0 {
			wait.remaining--
			if wait.remaining == 0 {
				wait.closed = true
				close(wait.done)
			}
		}
	}
	if len(l.state.active) == 0 && !l.state.retiring && g.users[l.userID] == l.state {
		delete(g.users, l.userID)
	}
	g.mu.Unlock()
}

// UserRetirement is a one-shot write barrier. BeginUserRetirement waits for
// all active requests to drain; BeginUserRetirementContext excludes the
// request represented by the supplied context, which is needed when an
// automatic ban is triggered from that request's own denial callback.
type UserRetirement struct {
	gate   *Gate
	userID int64
	state  *userState
	wait   *retirementWait
	done   atomic.Bool
}

// BeginUserRetirement closes new admission, cancels active user contexts, and
// waits for all active leases to release.
func (g *Gate) BeginUserRetirement(userID int64) (*UserRetirement, error) {
	return g.beginUserRetirement(userID, nil)
}

// BeginUserRetirementContext is the context-aware form used by request-driven
// automatic bans. The request carrying ctx is cancelled but not waited on by
// the barrier, preventing the denial callback from waiting on itself.
func (g *Gate) BeginUserRetirementContext(ctx context.Context, userID int64) (*UserRetirement, error) {
	return g.beginUserRetirement(userID, leaseFromContext(ctx))
}

func (g *Gate) beginUserRetirement(userID int64, excluded *lease) (*UserRetirement, error) {
	if g == nil || userID <= 0 {
		return nil, ErrInvalid
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, ErrClosed
	}
	state := g.users[userID]
	if state == nil {
		if len(g.users) >= g.maxUsers {
			g.mu.Unlock()
			return nil, ErrCapacity
		}
		state = &userState{active: make(map[*lease]struct{})}
		g.users[userID] = state
	}
	if state.retiring {
		g.mu.Unlock()
		return nil, ErrRetiring
	}
	if excluded != nil {
		if excluded.userID != userID {
			excluded = nil
		} else if _, exists := state.active[excluded]; !exists {
			excluded = nil
		}
	}
	state.retiring = true
	wait := &retirementWait{done: make(chan struct{}), excluded: excluded}
	for l := range state.active {
		if l == excluded {
			continue
		}
		wait.remaining++
	}
	state.retireWait = wait
	if wait.remaining == 0 {
		wait.closed = true
		close(wait.done)
	}
	cancels := make([]context.CancelFunc, 0, len(state.active))
	for l := range state.active {
		cancels = append(cancels, l.cancel)
	}
	g.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	<-wait.done
	return &UserRetirement{gate: g, userID: userID, state: state, wait: wait}, nil
}

// Commit permanently retires the state after the authoritative DB mutation.
func (r *UserRetirement) Commit() bool {
	if r == nil || r.gate == nil || !r.done.CompareAndSwap(false, true) {
		return false
	}
	r.gate.mu.Lock()
	r.state.retiring = true
	r.state.retireWait = nil
	if r.gate.users[r.userID] == r.state {
		delete(r.gate.users, r.userID)
	}
	r.gate.mu.Unlock()
	return true
}

// Abort reopens admission after a failed authoritative mutation. Existing
// requests were cancelled and are not resurrected; subsequent requests can
// acquire a fresh lease.
func (r *UserRetirement) Abort() bool {
	if r == nil || r.gate == nil || !r.done.CompareAndSwap(false, true) {
		return false
	}
	r.gate.mu.Lock()
	if r.gate.users[r.userID] == r.state {
		r.state.retiring = false
		r.state.retireWait = nil
		if len(r.state.active) == 0 {
			delete(r.gate.users, r.userID)
		}
	}
	r.gate.mu.Unlock()
	return true
}

// Close rejects new admission and cancels all active contexts. It does not
// wait for arbitrary handlers during process shutdown.
func (g *Gate) Close() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.closed = true
	cancels := make([]context.CancelFunc, 0)
	for _, state := range g.users {
		state.retiring = true
		for l := range state.active {
			cancels = append(cancels, l.cancel)
		}
	}
	g.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return nil
}
