package fatfish

import (
	"context"
	"time"
)

const (
	maxQueuedVerification = 8 // Two running and six waiting.
	maxQueuedBytes        = 32 << 20
)

type verifyJob struct {
	id           string
	inputs       []byte
	sizeBytes    int
	terminalTick int
	ctx          context.Context
	cancel       context.CancelFunc
	started      bool
	done         chan struct{}
}

func (s *Service) reserveJob(id string, raw []byte, tick int) (*verifyJob, bool, error) {
	if len(raw) == 0 || len(raw) > 4<<20 || tick < 0 {
		return nil, false, ErrInvalid
	}
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if s.closed {
		return nil, false, ErrUnavailable
	}
	if existing := s.jobs[id]; existing != nil {
		if !existing.started {
			return nil, false, ErrConflict
		}
		return existing, true, nil
	}
	if len(s.jobs) >= maxQueuedVerification || len(raw) > maxQueuedBytes-s.jobBytes {
		return nil, false, ErrCapacity
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &verifyJob{id: id, inputs: append([]byte(nil), raw...), sizeBytes: len(raw), terminalTick: tick, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	s.jobs[id] = job
	s.jobBytes += len(job.inputs)
	return job, false, nil
}

func (s *Service) releaseJob(job *verifyJob) {
	if job == nil {
		return
	}
	s.jobsMu.Lock()
	if s.jobs[job.id] == job {
		delete(s.jobs, job.id)
		s.jobBytes -= job.sizeBytes
		clear(job.inputs)
		job.inputs = nil
		close(job.done)
	}
	s.jobsMu.Unlock()
	job.cancel()
}

func (s *Service) startJob(job *verifyJob) error {
	s.jobsMu.Lock()
	if s.closed {
		s.jobsMu.Unlock()
		s.releaseJob(job)
		return ErrUnavailable
	}
	if s.jobs[job.id] != job {
		s.jobsMu.Unlock()
		return ErrConflict
	}
	job.started = true
	s.workers.Add(1)
	s.jobsMu.Unlock()
	go func() {
		defer s.workers.Done()
		defer s.releaseJob(job)
		select {
		case s.workerSlots <- struct{}{}:
			defer func() { <-s.workerSlots }()
		case <-job.ctx.Done():
			return
		}
		ctx, cancel := context.WithTimeout(job.ctx, 5*time.Second)
		defer cancel()
		// The receiver owns the complete input only for this bounded replay.
		// Process failure is reconciled from the durable digest and original tab.
		_ = s.processVerification(ctx, job)
	}()
	return nil
}

func (s *Service) cancelJob(id string) {
	s.cancelJobAndWait(id)
}

func (s *Service) cancelJobAndWait(id string) {
	s.jobsMu.Lock()
	job := s.jobs[id]
	if job != nil {
		job.cancel()
	}
	started := job != nil && job.started
	s.jobsMu.Unlock()
	if job == nil {
		return
	}
	if !started {
		s.releaseJob(job)
		return
	}
	<-job.done
}

func (s *Service) isClosed() bool {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	return s.closed
}

// Close cancels every in-memory replay and waits for its byte buffers to be
// cleared. Durable verifying receipts remain recoverable after restart.
func (s *Service) Close() {
	if s == nil {
		return
	}
	s.jobsMu.Lock()
	unstarted := []*verifyJob{}
	if !s.closed {
		s.closed = true
		for _, job := range s.jobs {
			job.cancel()
			if !job.started {
				unstarted = append(unstarted, job)
			}
		}
	}
	s.jobsMu.Unlock()
	for _, job := range unstarted {
		s.releaseJob(job)
	}
	s.workers.Wait()
}
