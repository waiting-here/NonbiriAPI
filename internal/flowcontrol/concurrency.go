package flowcontrol

import (
	"errors"
	"sync"
	"sync/atomic"
)

const (
	// DefaultMaxTrackedConcurrencyUsers is the frozen process-local capacity.
	// The map contains only users with at least one active permit.
	DefaultMaxTrackedConcurrencyUsers = 100000
)

var errConcurrencyClosed = errors.New("flowcontrol: user concurrency limiter is closed")

// userConcurrencyLimiter is a non-blocking, per-user in-flight gate. A state
// object remains stable until its final permit releases; ForgetUser therefore
// cannot delete and recreate a second counter for the same id while an old
// request is still active.
type userConcurrencyLimiter struct {
	mu         sync.Mutex
	users      map[int64]*userConcurrencyState
	maxTracked int
	closed     bool
}

const userAdmissionGateStripes = 4096

// userAdmissionGate linearizes the live-account read and concurrency acquire
// against ban/deletion state changes without retaining one lock per user. A
// fixed stripe may briefly serialize unrelated users on a hash collision, but
// it cannot grow with attacker-controlled identities.
type userAdmissionGate struct {
	stripes [userAdmissionGateStripes]sync.RWMutex
}

func (g *userAdmissionGate) stripe(userID int64) *sync.RWMutex {
	// userID is positive at every call site. Multiplication mixes sequential
	// ids before the power-of-two mask while remaining deterministic.
	index := (uint64(userID) * 11400714819323198485) & (userAdmissionGateStripes - 1)
	return &g.stripes[index]
}

type userAdmissionReadGuard struct {
	lock     *sync.RWMutex
	released atomic.Bool
}

func (g *userAdmissionGate) beginRead(userID int64) *userAdmissionReadGuard {
	lock := g.stripe(userID)
	lock.RLock()
	return &userAdmissionReadGuard{lock: lock}
}

func (g *userAdmissionGate) beginWrite(userID int64) *userAdmissionWriteGuard {
	lock := g.stripe(userID)
	lock.Lock()
	return &userAdmissionWriteGuard{lock: lock}
}

func (g *userAdmissionReadGuard) release() {
	if g != nil && g.lock != nil && g.released.CompareAndSwap(false, true) {
		g.lock.RUnlock()
	}
}

type userAdmissionWriteGuard struct {
	lock     *sync.RWMutex
	released atomic.Bool
}

func (g *userAdmissionWriteGuard) release() {
	if g != nil && g.lock != nil && g.released.CompareAndSwap(false, true) {
		g.lock.Unlock()
	}
}

type userConcurrencyState struct {
	active   int
	retiring bool
}

type userConcurrencyPermit struct {
	limiter  *userConcurrencyLimiter
	userID   int64
	state    *userConcurrencyState
	released atomic.Bool
}

func newUserConcurrencyLimiter(maxTracked int) (*userConcurrencyLimiter, error) {
	if maxTracked == 0 {
		maxTracked = DefaultMaxTrackedConcurrencyUsers
	}
	if maxTracked < 1 || maxTracked > DefaultMaxTrackedConcurrencyUsers {
		return nil, errors.New("flowcontrol: invalid tracked user capacity")
	}
	return &userConcurrencyLimiter{
		users:      make(map[int64]*userConcurrencyState),
		maxTracked: maxTracked,
	}, nil
}

func (l *userConcurrencyLimiter) tryAcquire(userID int64, limit int) (*userConcurrencyPermit, error) {
	if l == nil {
		return nil, errConcurrencyClosed
	}
	if userID <= 0 || limit < 1 || limit > maxUserConcurrencyLimit {
		return nil, ErrInvalidUser
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, errConcurrencyClosed
	}
	state := l.users[userID]
	if state == nil {
		if len(l.users) >= l.maxTracked {
			return nil, ErrConcurrencyLimited
		}
		state = &userConcurrencyState{}
		l.users[userID] = state
	}
	if state.retiring || state.active >= limit {
		if state.active == 0 {
			delete(l.users, userID)
		}
		return nil, ErrConcurrencyLimited
	}
	state.active++
	return &userConcurrencyPermit{limiter: l, userID: userID, state: state}, nil
}

func (p *userConcurrencyPermit) Release() bool {
	if p == nil || p.limiter == nil || p.state == nil || !p.released.CompareAndSwap(false, true) {
		return false
	}
	l := p.limiter
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return true
	}
	// Pointer identity is the split-counter backstop. A permit may only
	// decrement the exact state object from which it was acquired.
	if current := l.users[p.userID]; current != p.state {
		return true
	}
	if p.state.active <= 0 {
		panic("flowcontrol: user concurrency permit accounting underflow")
	}
	p.state.active--
	if p.state.active == 0 {
		delete(l.users, p.userID)
	}
	return true
}

func (p *userConcurrencyPermit) Active() bool {
	if p == nil || p.limiter == nil || p.state == nil || p.released.Load() {
		return false
	}
	p.limiter.mu.Lock()
	defer p.limiter.mu.Unlock()
	return !p.limiter.closed && p.limiter.users[p.userID] == p.state && p.state.active > 0 && !p.released.Load()
}

// forgetUser prevents new acquisitions through an existing active counter,
// but deliberately retains that exact object until all old permits release.
// With no active state there is nothing to retain; request-time DB resolution
// is the authoritative ban/deletion gate for any later acquisition attempt.
func (l *userConcurrencyLimiter) forgetUser(userID int64) {
	if l == nil || userID <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.users[userID]
	if state == nil {
		return
	}
	if state.active == 0 {
		delete(l.users, userID)
		return
	}
	state.retiring = true
}

func (l *userConcurrencyLimiter) close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.closed = true
	l.users = make(map[int64]*userConcurrencyState)
	l.mu.Unlock()
}
