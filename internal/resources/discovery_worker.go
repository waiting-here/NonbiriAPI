package resources

import (
	"context"
	"errors"
	"sync"
	"time"
)

// DiscoveryWorkerPool bounds both concurrent discovery work and total
// admitted work, including jobs waiting for a concurrency slot.
type DiscoveryWorkerPool struct {
	context     context.Context
	cancel      context.CancelFunc
	timeout     time.Duration
	workers     chan struct{}
	admission   chan struct{}
	mu          sync.Mutex
	closed      bool
	outstanding sync.WaitGroup
}

func NewDiscoveryWorkerPool(maxConcurrent, maxAdmitted int, timeout time.Duration) (*DiscoveryWorkerPool, error) {
	if maxConcurrent < 1 || maxAdmitted < maxConcurrent || timeout <= 0 {
		return nil, errors.New("resources: valid discovery worker bounds are required")
	}
	workerContext, cancel := context.WithCancel(context.Background())
	return &DiscoveryWorkerPool{
		context:   workerContext,
		cancel:    cancel,
		timeout:   timeout,
		workers:   make(chan struct{}, maxConcurrent),
		admission: make(chan struct{}, maxAdmitted),
	}, nil
}

func (pool *DiscoveryWorkerPool) ReserveDiscovery() (DiscoveryReservation, bool) {
	if pool == nil {
		return nil, false
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.closed {
		return nil, false
	}
	select {
	case pool.admission <- struct{}{}:
		pool.outstanding.Add(1)
		return &discoveryPoolReservation{pool: pool}, true
	default:
		return nil, false
	}
}

func (pool *DiscoveryWorkerPool) Close() {
	if pool == nil {
		return
	}
	pool.mu.Lock()
	if !pool.closed {
		pool.closed = true
		pool.cancel()
	}
	pool.mu.Unlock()
	pool.outstanding.Wait()
}

func (pool *DiscoveryWorkerPool) run(work func(context.Context)) {
	defer pool.finish()
	workerContext, cancel := context.WithTimeout(pool.context, pool.timeout)
	defer cancel()
	acquired := false
	select {
	case pool.workers <- struct{}{}:
		acquired = true
	case <-workerContext.Done():
	}
	if acquired {
		defer func() { <-pool.workers }()
	}
	work(workerContext)
}

func (pool *DiscoveryWorkerPool) finish() {
	<-pool.admission
	pool.outstanding.Done()
}

type discoveryPoolReservation struct {
	pool *DiscoveryWorkerPool
	once sync.Once
}

func (reservation *discoveryPoolReservation) Start(work func(context.Context)) {
	if reservation == nil || reservation.pool == nil {
		return
	}
	reservation.once.Do(func() {
		if work == nil {
			reservation.pool.finish()
			return
		}
		go reservation.pool.run(work)
	})
}

func (reservation *discoveryPoolReservation) Release() {
	if reservation == nil || reservation.pool == nil {
		return
	}
	reservation.once.Do(reservation.pool.finish)
}
