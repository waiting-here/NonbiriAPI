package worker

import (
	"sync"
	"sync/atomic"
)

// LeaseRegistry prevents two goroutines in this process from running the same
// worker key concurrently. It is intentionally not persisted and makes no
// distributed-lease claim for the single-instance deployment.
type LeaseRegistry struct {
	mu   sync.Mutex
	held map[string]uint64
	next atomic.Uint64
}

// Lease is a generation-tagged local ownership token. Release is idempotent;
// a stale token cannot release a later acquisition of the same key.
type Lease struct {
	registry *LeaseRegistry
	key      string
	token    uint64
	once     sync.Once
}

func (r *LeaseRegistry) TryAcquire(workerKey string) (*Lease, bool) {
	if r == nil || workerKey == "" {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.held == nil {
		r.held = make(map[string]uint64)
	}
	if _, exists := r.held[workerKey]; exists {
		return nil, false
	}
	token := r.next.Add(1)
	r.held[workerKey] = token
	return &Lease{registry: r, key: workerKey, token: token}, true
}

func (l *Lease) Release() {
	if l == nil || l.registry == nil {
		return
	}
	l.once.Do(func() {
		l.registry.mu.Lock()
		defer l.registry.mu.Unlock()
		if l.registry.held[l.key] == l.token {
			delete(l.registry.held, l.key)
		}
	})
}
