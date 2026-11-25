package logapi

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"time"
)

const (
	maxLogSearches       = 8
	maxActorLogSearches  = 2
	logSearchIdleTimeout = 5 * time.Minute
)

var errSearchBusy = errors.New("log search queue is full")

type logSearchOwner struct {
	role    string
	actorID int64
	session string
}

type logSearchJob struct {
	id      string
	owner   logSearchOwner
	ctx     context.Context
	cancel  context.CancelFunc
	expires time.Time
	timer   *time.Timer
	readers int
	matches *searchMatches
	err     error
}

type logSearchRegistry struct {
	mu     sync.Mutex
	jobs   map[string]*logSearchJob
	worker chan struct{}
	closed bool
	wg     sync.WaitGroup
}

// Only the long path takes a worker slot. Keep a reader available for ordinary
// site traffic and authorization while the background snapshot is open.
func (s *logSearchRegistry) start(ctx context.Context, owner logSearchOwner, collect func(context.Context, func(int64, int64) error) error) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.jobs == nil {
		s.jobs = make(map[string]*logSearchJob)
		s.worker = make(chan struct{}, 1)
	}
	owned := 0
	var oldest, oldestOwned *logSearchJob
	for _, job := range s.jobs {
		sameActor := job.owner.actorID == owner.actorID
		if sameActor {
			owned++
		}
		if job.readers > 0 || (job.matches == nil && job.err == nil) {
			continue
		}
		if oldest == nil || job.expires.Before(oldest.expires) {
			oldest = job
		}
		if sameActor && (oldestOwned == nil || job.expires.Before(oldestOwned.expires)) {
			oldestOwned = job
		}
	}
	if owned >= maxActorLogSearches {
		if oldestOwned == nil {
			return "", errSearchBusy
		}
		s.removeLocked(oldestOwned)
	} else if len(s.jobs) >= maxLogSearches {
		if oldest == nil {
			return "", errSearchBusy
		}
		s.removeLocked(oldest)
	}
	id := rand.Text()
	lifetime, cancel := context.WithCancel(context.Background())
	job := &logSearchJob{id: id, owner: owner, ctx: lifetime, cancel: cancel, expires: time.Now().Add(logSearchIdleTimeout)}
	s.jobs[id] = job
	job.timer = time.AfterFunc(logSearchIdleTimeout, func() { s.expire(job) })
	s.wg.Add(1)
	go s.run(context.WithoutCancel(ctx), job, collect)
	return id, nil
}

func (s *logSearchRegistry) run(original context.Context, job *logSearchJob, collect func(context.Context, func(int64, int64) error) error) {
	defer s.wg.Done()
	select {
	case s.worker <- struct{}{}:
		defer func() { <-s.worker }()
	case <-job.ctx.Done():
		return
	}
	ctx, cancel := context.WithCancel(original)
	stop := context.AfterFunc(job.ctx, cancel)
	defer stop()
	defer cancel()
	result, err := newSearchMatches()
	if err == nil {
		err = collect(ctx, result.add)
		if err == nil {
			err = result.flush()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil || s.jobs[job.id] != job {
		if result != nil {
			result.close()
		}
		job.err = err
		return
	}
	result.tail = nil
	job.matches = result
}

type logSearchLease struct {
	matches *searchMatches
	ctx     context.Context
	release func()
}

func (s *logSearchRegistry) lookup(id string, owner logSearchOwner) (logSearchLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[id]
	if job == nil || job.owner != owner {
		return logSearchLease{}, ErrNotFound
	}
	if job.readers == 0 && !time.Now().Before(job.expires) {
		s.removeLocked(job)
		return logSearchLease{}, ErrNotFound
	}
	if job.err != nil {
		return logSearchLease{}, job.err
	}
	job.readers++
	job.expires = time.Now().Add(logSearchIdleTimeout)
	job.timer.Reset(logSearchIdleTimeout)
	return logSearchLease{matches: job.matches, ctx: job.ctx, release: func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		job.readers--
		if s.jobs[id] == job {
			job.expires = time.Now().Add(logSearchIdleTimeout)
			job.timer.Reset(logSearchIdleTimeout)
		}
	}}, nil
}

func (s *logSearchRegistry) forget(id string, owner logSearchOwner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job := s.jobs[id]; job != nil && job.owner == owner {
		s.removeLocked(job)
	}
}

func (s *logSearchRegistry) expire(job *logSearchJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs[job.id] != job {
		return
	}
	if job.readers > 0 {
		job.expires = time.Now().Add(logSearchIdleTimeout)
	}
	if left := time.Until(job.expires); left > 0 {
		job.timer.Reset(left)
		return
	}
	s.removeLocked(job)
}

func (s *logSearchRegistry) removeLocked(job *logSearchJob) {
	delete(s.jobs, job.id)
	job.timer.Stop()
	job.cancel()
	if job.matches != nil {
		job.matches.close()
	}
}

// CloseSearches releases snapshots and scratch files before the application
// closes authorization and database services.
func (r *Repository) CloseSearches() {
	s := &r.searches
	s.mu.Lock()
	s.closed = true
	for _, job := range s.jobs {
		s.removeLocked(job)
	}
	s.mu.Unlock()
	s.wg.Wait()
}
