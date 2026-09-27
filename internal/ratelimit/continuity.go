package ratelimit

import "time"

// WindowFact is a committed event's minimum recovery material. Importing it
// affects only its user window; the process-wide budget is never fabricated.
type WindowFact struct {
	ID      string
	At      time.Time
	Expires time.Time
}

func (r *RPM) SnapshotUser(userKey string) (time.Time, []WindowFact, error) {
	if r == nil {
		return time.Time{}, nil, ErrClosed
	}
	if err := validateKeyBytes(userKey, r.maxKeyBytes); err != nil {
		return time.Time{}, nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return time.Time{}, nil, ErrClosed
	}
	now := r.clock.Now()
	r.pruneLocked(now)
	facts := []WindowFact{}
	for _, event := range r.users[userKey] {
		if event.state.Load() != uint32(rpmEventCommitted) {
			return time.Time{}, nil, ErrCapacity
		}
		facts = append(facts, WindowFact{ID: event.id, At: event.at, Expires: event.expires})
	}
	return now, facts, nil
}

// MergeUser is bounded and atomic. Existing process events and repeatedly
// loaded persisted facts are identified by the same opaque ID, never summed
// twice. Expiry remains the original event deadline across restarts.
func (r *RPM) MergeUser(userKey string, facts []WindowFact) error {
	if r == nil {
		return ErrClosed
	}
	if err := validateKeyBytes(userKey, r.maxKeyBytes); err != nil {
		return err
	}
	if len(facts) > r.maxEvents {
		return ErrCapacity
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	now := r.clock.Now()
	r.pruneLocked(now)
	seen := make(map[string]WindowFact, len(facts)+len(r.users[userKey]))
	for _, event := range r.users[userKey] {
		seen[event.id] = WindowFact{ID: event.id, At: event.at, Expires: event.expires}
	}
	added := []*rpmEvent{}
	for _, fact := range facts {
		if len(fact.ID) == 0 || len(fact.ID) > 128 || fact.At.After(now) || !fact.Expires.After(fact.At) {
			return ErrInvalidConfig
		}
		if !fact.Expires.After(now) {
			continue
		}
		if prior, found := seen[fact.ID]; found {
			// Persistence has millisecond resolution and rounds expiry up.
			if prior.At.UnixMilli() != fact.At.UnixMilli() || prior.Expires.Add(time.Millisecond-1).UnixMilli() != fact.Expires.Add(time.Millisecond-1).UnixMilli() {
				return ErrInvalidConfig
			}
			continue
		}
		seen[fact.ID] = fact
		event := &rpmEvent{id: fact.ID, at: fact.At, expires: fact.Expires, user: userKey}
		event.state.Store(uint32(rpmEventCommitted))
		added = append(added, event)
	}
	if len(added) == 0 {
		return nil
	}
	if len(r.users[userKey]) == 0 && len(r.users) >= r.maxUserKeys || len(added) > r.maxEvents-r.eventCount {
		return ErrCapacity
	}
	r.users[userKey] = append(r.users[userKey], added...)
	r.eventCount += len(added)
	return nil
}
