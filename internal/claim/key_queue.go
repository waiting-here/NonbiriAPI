package claim

import (
	"container/list"
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

const keyQueueRecheck = time.Second

type keyWaiter struct {
	input   ClaimInput
	reason  keyLimitReason
	element *list.Element
	entry   *keyWaiters
}

type keyWaiters struct {
	waiters list.List
	handoff *keyWaiter
	changed chan struct{}
}

func (entry *keyWaiters) front() *keyWaiter {
	if first := entry.waiters.Front(); first != nil {
		return first.Value.(*keyWaiter)
	}
	return nil
}

func (entry *keyWaiters) reason() keyLimitReason {
	if entry.handoff != nil {
		return entry.handoff.reason
	}
	if head := entry.front(); head != nil {
		return head.reason
	}
	return 0
}

func (entry *keyWaiters) notify() {
	close(entry.changed)
	entry.changed = make(chan struct{})
}

type keyQueue struct {
	mu     sync.Mutex
	keys   map[int64]*keyWaiters
	closed bool
}

// StopKeyWaiters closes queue admission. Durable request settlement remains
// available while the waiting callers unwind.
func (s *Service) StopKeyWaiters() {
	if s == nil {
		return
	}
	q := &s.keyQueue
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	for _, entry := range q.keys {
		entry.notify()
	}
}

// NotifyKey wakes an existing queue after a committed capacity or policy
// change. It performs no database work and creates no entry for an idle key.
func (s *Service) NotifyKey(keyID int64) {
	if s == nil {
		return
	}
	q := &s.keyQueue
	q.mu.Lock()
	defer q.mu.Unlock()
	if entry := q.keys[keyID]; entry != nil {
		entry.notify()
	}
}

func (s *Service) finishKeyClaim(ticket *keyWaiter, committed bool) {
	if ticket == nil {
		return
	}
	q := &s.keyQueue
	q.mu.Lock()
	defer q.mu.Unlock()
	entry := ticket.entry
	if entry.handoff != ticket {
		return
	}
	if committed {
		if ticket.element != nil {
			entry.waiters.Remove(ticket.element)
			ticket.element = nil
		}
	} else {
		entry.handoff = nil
	}
	entry.notify()
}

func (s *Service) removeKeyWaiter(ticket *keyWaiter) {
	q := &s.keyQueue
	q.mu.Lock()
	defer q.mu.Unlock()
	entry := ticket.entry
	if ticket.element != nil {
		entry.waiters.Remove(ticket.element)
		ticket.element = nil
	}
	if entry.handoff == ticket {
		entry.handoff = nil
	}
	entry.notify()
	if entry.waiters.Len() == 0 && entry.handoff == nil && q.keys[ticket.input.Candidate.EndpointKeyID] == entry {
		delete(q.keys, ticket.input.Candidate.EndpointKeyID)
	}
}

func (s *Service) finishKeyHandoff(handle Handle) {
	if handle.keyWaiter != nil {
		s.removeKeyWaiter(handle.keyWaiter)
	} else {
		s.NotifyKey(handle.candidate.EndpointKeyID)
	}
}

// WaitForKey tries only explicit queue candidates collected by Claim. One
// request holds at most one ticket after immediate candidates were exhausted.
func (s *Service) WaitForKey(ctx context.Context, candidates []ClaimInput) (Handle, error) {
	if s == nil || ctx == nil || len(candidates) == 0 {
		return Handle{}, ErrInvalidInput
	}
	seen := make(map[int64]bool)
	var fallback *ClaimInput
	last := error(ErrKeyRateLimited)
	var skipped requestattempt.AdmissionCause
	recordSkip := func(input ClaimInput, err error) {
		last = err
		skipped = skipped.Combine(AdmissionReason(err))
		observeCandidateAdmission(ctx, input, "queue", err)
	}
	for _, input := range candidates {
		if input.queueFallback != nil {
			fallback = input.queueFallback
		}
		if !validClaimInput(input) || len(input.BalancedCandidates) != 0 || input.Purpose == PurposeDiscovery {
			return Handle{}, ErrInvalidInput
		}
		keyID := input.Candidate.EndpointKeyID
		if seen[keyID] {
			continue
		}
		seen[keyID] = true
		state, err := s.probeKeyQueue(ctx, input)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				recordSkip(input, err)
				continue
			}
			return Handle{}, err
		}
		q := &s.keyQueue
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return Handle{}, ErrDependencyUnavailable
		}
		entry := q.keys[keyID]
		reason := state.blocked
		waiting := 0
		if entry != nil {
			reason |= state.enabled(entry.reason())
			waiting = entry.waiters.Len()
		}
		var cause requestattempt.AdmissionCause
		switch {
		case !state.queueAllowed(reason):
			cause = state.blocked.admissionCause()
		case !state.queueFits(reason, waiting):
			cause = requestattempt.AdmissionQueueFull
		}
		if cause != "" {
			q.mu.Unlock()
			recordSkip(input, cause.Wrap(ErrKeyRateLimited))
			continue
		}
		if entry == nil {
			entry = &keyWaiters{changed: make(chan struct{})}
			if q.keys == nil {
				q.keys = make(map[int64]*keyWaiters)
			}
			q.keys[keyID] = entry
		}
		ticket := &keyWaiter{input: input, reason: reason, entry: entry}
		ticket.element = entry.waiters.PushBack(ticket)
		entry.notify()
		q.mu.Unlock()
		handle, err := s.awaitKey(ctx, ticket)
		if err == nil {
			return handle, nil
		}
		s.removeKeyWaiter(ticket)
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrKeyRateLimited) {
			recordSkip(input, err)
			continue
		}
		observeCandidateAdmission(ctx, input, "queue", err)
		return Handle{}, err
	}
	if fallback != nil {
		handle, err := s.Claim(ctx, *fallback)
		if queued := KeyQueueCandidates(err); len(queued) > 0 {
			prior := KeyQueueSkippedReason(err)
			handle, err = s.WaitForKey(ctx, queued)
			if errors.Is(err, ErrKeyRateLimited) || errors.Is(err, ErrNotFound) {
				err = prior.Combine(AdmissionReason(err)).Wrap(err)
			}
		}
		return handle, err
	}
	return Handle{}, skipped.Wrap(last)
}

func (s *Service) awaitKey(ctx context.Context, ticket *keyWaiter) (Handle, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Handle{}, err
		}
		q := &s.keyQueue
		q.mu.Lock()
		entry := ticket.entry
		closed := q.closed
		head := entry.handoff == nil && entry.front() == ticket
		changed := entry.changed
		q.mu.Unlock()
		if closed {
			return Handle{}, ErrDependencyUnavailable
		}
		if !head {
			select {
			case <-ctx.Done():
				return Handle{}, queueWaitEnded(ctx)
			case <-changed:
				continue
			}
		}
		state, err := s.probeKeyQueue(ctx, ticket.input)
		if err != nil {
			return Handle{}, err
		}
		if !state.queueAllowed(state.blocked) {
			return Handle{}, state.blocked.admissionCause().Wrap(ErrKeyRateLimited)
		}
		if state.blocked == 0 {
			input := ticket.input
			input.queueTicket = ticket
			handle, err := s.Claim(ctx, input)
			if err == nil || len(KeyQueueCandidates(err)) == 0 {
				return handle, err
			}
		}
		// Only the head probes. This finite reader fallback also observes
		// low-frequency authority edits without a dedicated key event.
		wait := keyQueueRecheck
		if state.nextRPM > 0 {
			until := time.Unix(state.nextRPM, 0).Sub(s.now())
			if until > 0 && until < wait {
				wait = until
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Handle{}, queueWaitEnded(ctx)
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// Only a deadline reached in a queue wait is reported as a queue timeout.
func queueWaitEnded(ctx context.Context) error {
	err := ctx.Err()
	if errors.Is(err, context.DeadlineExceeded) {
		return requestattempt.AdmissionQueueTimeout.Wrap(err)
	}
	return err
}

func (s *Service) probeKeyQueue(ctx context.Context, input ClaimInput) (keyLimitState, error) {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return keyLimitState{}, err
	}
	defer tx.Rollback()
	at, err := s.nowUnix()
	if err != nil {
		return keyLimitState{}, err
	}
	var route RouteKind
	var requestState RequestState
	if err := tx.QueryRowContext(ctx, `SELECT route_kind,state FROM logical_requests WHERE id=? AND user_id=?`, input.RequestID, input.ActorUserID).Scan(&route, &requestState); err != nil {
		return keyLimitState{}, authorizationError(err)
	}
	if requestState == RequestTerminal {
		return keyLimitState{}, ErrNotFound
	}
	if err := requireActiveUser(ctx, tx, input.ActorUserID, at, input.Purpose == PurposeCharity); err != nil {
		return keyLimitState{}, err
	}
	if _, err := resolveInferenceTargetTx(ctx, tx, input, route.Operation()); err != nil {
		return keyLimitState{}, err
	}
	return readKeyLimitsTx(ctx, tx, input.Candidate.EndpointKeyID, at)
}
