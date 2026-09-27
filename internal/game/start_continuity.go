package game

import (
	"context"
	"database/sql"
	"time"
)

type StartWindowFact struct {
	ID            string
	AtMillis      int64
	ExpiresMillis int64
}

// StartContinuity keeps the game contract independent of storage and keys.
// Implementations resolve only the extant account inside the caller's TX.
type StartContinuity interface {
	Identity(context.Context, int64) (string, error)
	IdentityTx(context.Context, *sql.Tx, int64) (string, error)
	LoadTx(context.Context, *sql.Tx, int64, int64) (string, []StartWindowFact, error)
	SaveTx(context.Context, *sql.Tx, int64, int64, []StartWindowFact) error
}

// AttachContinuity is called once, before listeners and game workers start.
func (l *StartLimiter) AttachContinuity(service StartContinuity) error {
	if l == nil || service == nil {
		return ErrStartClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.continuity != nil || len(l.users) != 0 || len(l.deleting) != 0 {
		return ErrStartClosed
	}
	l.continuity = service
	return nil
}

// ReserveTx resolves the stable identity and its persisted window using the
// already-open game transaction, so it never opens a nested SQLite writer.
func (l *StartLimiter) ReserveTx(ctx context.Context, tx *sql.Tx, userID int64) (*StartReservation, time.Duration, error) {
	if l == nil {
		return nil, 0, ErrStartClosed
	}
	if l.continuity == nil {
		return l.Reserve(userID)
	}
	userKey, events, err := l.continuity.LoadTx(ctx, tx, userID, l.now().UnixMilli())
	if err != nil {
		return nil, 0, err
	}
	if err := l.mergeWindow(userKey, events); err != nil {
		return nil, 0, err
	}
	return l.reserve(userKey)
}

func (l *StartLimiter) mergeWindow(userKey string, events []StartWindowFact) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrStartClosed
	}
	now := l.now()
	l.purgeUserLocked(userKey, now)
	user := l.users[userKey]
	seen := map[string]*startEvent{}
	if user != nil {
		for _, event := range user.events {
			seen[event.id] = event
		}
	}
	added := []*startEvent{}
	for _, event := range events {
		if len(event.ID) == 0 || len(event.ID) > 128 || event.AtMillis > now.UnixMilli() || event.ExpiresMillis <= event.AtMillis {
			return ErrStartCapacity
		}
		if event.ExpiresMillis <= now.UnixMilli() {
			continue
		}
		if prior := seen[event.ID]; prior != nil {
			if prior.at.UnixMilli() != event.AtMillis || prior.expires.Add(time.Millisecond-1).UnixMilli() != event.ExpiresMillis {
				return ErrStartCapacity
			}
			continue
		}
		value := &startEvent{id: event.ID, at: time.UnixMilli(event.AtMillis), expires: time.UnixMilli(event.ExpiresMillis), state: startCommitted}
		seen[event.ID] = value
		added = append(added, value)
	}
	if len(added) == 0 {
		return nil
	}
	if len(seen) > FishingStartsPerMinute {
		return ErrStartCapacity
	}
	if user == nil {
		if l.trackedUsersLocked() >= l.maxUsers {
			l.purgeAllLocked(now)
		}
		if l.trackedUsersLocked() >= l.maxUsers {
			return ErrStartCapacity
		}
		user = &startUser{}
		l.users[userKey] = user
	}
	user.events = append(user.events, added...)
	return nil
}

func (l *StartLimiter) PreserveWindowTx(ctx context.Context, tx *sql.Tx, userID, _ int64) error {
	if l == nil || l.continuity == nil {
		return ErrStartClosed
	}
	userKey, err := l.continuity.IdentityTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return ErrStartClosed
	}
	now := l.now()
	l.purgeUserLocked(userKey, now)
	events := []StartWindowFact{}
	if user := l.users[userKey]; user != nil {
		for _, event := range user.events {
			if event.state != startCommitted {
				l.mu.Unlock()
				return ErrStartCapacity
			}
			events = append(events, StartWindowFact{ID: event.id, AtMillis: event.at.UnixMilli(), ExpiresMillis: event.expires.Add(time.Millisecond - 1).UnixMilli()})
		}
	}
	l.mu.Unlock()
	return l.continuity.SaveTx(ctx, tx, userID, now.UnixMilli(), events)
}
