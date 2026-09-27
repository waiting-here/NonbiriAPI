package game

import (
	"context"
	"crypto/rand"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	FishingStartsPerMinute = 30
	DefaultMaxStartUsers   = 100000
	startWindow            = time.Minute
)

var (
	ErrStartRateLimited = errors.New("game: start rate limited")
	ErrStartCapacity    = errors.New("game: start limiter capacity exhausted")
	ErrStartClosed      = errors.New("game: start limiter closed")
	ErrUserDeleting     = errors.New("game: user deletion in progress")
)

type startEventState uint8

const (
	startTentative startEventState = iota
	startCommitted
	startReleased
)

type startEvent struct {
	id      string
	at      time.Time
	expires time.Time
	state   startEventState
}

type startUser struct {
	events []*startEvent
}

type deletionMarker struct {
	committed bool
}

// StartLimiter is an independent, process-local, bounded per-user tentative
// reservation gate. It neither calls nor shares state with model RPM and
// therefore cannot trigger model-call penalties.
type StartLimiter struct {
	mu         sync.Mutex
	now        func() time.Time
	users      map[string]*startUser
	deleting   map[string]*deletionMarker
	maxUsers   int
	closed     bool
	continuity StartContinuity
}

type StartLimiterConfig struct {
	Now      func() time.Time
	MaxUsers int
}

func NewStartLimiter(config StartLimiterConfig) (*StartLimiter, error) {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.MaxUsers == 0 {
		config.MaxUsers = DefaultMaxStartUsers
	}
	if config.MaxUsers < 1 || config.MaxUsers > DefaultMaxStartUsers {
		return nil, ErrStartCapacity
	}
	return &StartLimiter{
		now: config.Now, users: make(map[string]*startUser),
		deleting: make(map[string]*deletionMarker), maxUsers: config.MaxUsers,
	}, nil
}

// StartReservation counts immediately but can be committed or released
// exactly once after the database result is classified.
type StartReservation struct {
	limiter *StartLimiter
	userKey string
	event   *startEvent
	done    atomic.Bool
}

// Reserve tentatively admits one new start. Exact replays must be resolved by
// the caller before invoking this method.
func (l *StartLimiter) Reserve(userID int64) (*StartReservation, time.Duration, error) {
	if l == nil {
		return nil, 0, ErrStartClosed
	}
	if userID <= 0 {
		return nil, 0, ErrUserDeleting
	}
	if l.continuity != nil {
		return nil, 0, ErrUserDeleting
	}
	return l.reserve(strconv.FormatInt(userID, 10))
}

func (l *StartLimiter) reserve(userKey string) (*StartReservation, time.Duration, error) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, 0, ErrStartClosed
	}
	if _, deleting := l.deleting[userKey]; deleting {
		return nil, 0, ErrUserDeleting
	}
	l.purgeUserLocked(userKey, now)
	user := l.users[userKey]
	if user == nil {
		if l.trackedUsersLocked() >= l.maxUsers {
			l.purgeAllLocked(now)
			if l.trackedUsersLocked() >= l.maxUsers {
				return nil, 0, ErrStartCapacity
			}
		}
		user = &startUser{}
		l.users[userKey] = user
	}
	if len(user.events) >= FishingStartsPerMinute {
		expires := user.events[0].expires
		for _, event := range user.events {
			if event.expires.Before(expires) {
				expires = event.expires
			}
		}
		retry := expires.Sub(now)
		if retry < 0 {
			retry = 0
		}
		return nil, retry, ErrStartRateLimited
	}
	event := &startEvent{id: rand.Text(), at: now, expires: now.Add(startWindow), state: startTentative}
	user.events = append(user.events, event)
	return &StartReservation{limiter: l, userKey: userKey, event: event}, 0, nil
}

func (r *StartReservation) Commit() bool  { return r.finish(startCommitted) }
func (r *StartReservation) Release() bool { return r.finish(startReleased) }

func (r *StartReservation) finish(state startEventState) bool {
	if r == nil || r.limiter == nil || r.event == nil || !r.done.CompareAndSwap(false, true) {
		return false
	}
	l := r.limiter
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return true
	}
	user := l.users[r.userKey]
	if user == nil {
		return true
	}
	r.event.state = state
	if state == startReleased {
		l.removeEventLocked(r.userKey, user, r.event)
	}
	l.finishDeletionLocked(r.userKey)
	return true
}

// BeginUserDeletion blocks new reservations until exactly one terminal
// callback is chosen. Abort restores the prior window byte-for-byte. Commit
// retains stable-identity facts until their original expiry and forgets only
// legacy account-local windows after tentative reservations terminate.
func (l *StartLimiter) BeginUserDeletion(userID int64) (commit func() bool, abort func() bool, err error) {
	return l.BeginUserDeletionContext(context.Background(), userID)
}

func (l *StartLimiter) BeginUserDeletionContext(ctx context.Context, userID int64) (commit func() bool, abort func() bool, err error) {
	if l == nil || userID <= 0 {
		return nil, nil, ErrUserDeleting
	}
	userKey := strconv.FormatInt(userID, 10)
	if l.continuity != nil {
		key, err := l.continuity.Identity(ctx, userID)
		if err != nil {
			return nil, nil, err
		}
		userKey = key
	}
	now := l.now()
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, nil, ErrStartClosed
	}
	if _, exists := l.deleting[userKey]; exists {
		l.mu.Unlock()
		return nil, nil, ErrUserDeleting
	}
	if _, tracked := l.users[userKey]; !tracked && l.trackedUsersLocked() >= l.maxUsers {
		l.purgeAllLocked(now)
		if l.trackedUsersLocked() >= l.maxUsers {
			l.mu.Unlock()
			return nil, nil, ErrStartCapacity
		}
	}
	marker := &deletionMarker{}
	l.deleting[userKey] = marker
	l.mu.Unlock()
	var terminal atomic.Bool
	commit = func() bool {
		if !terminal.CompareAndSwap(false, true) {
			return false
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.deleting[userKey] != marker {
			return false
		}
		marker.committed = true
		l.finishDeletionLocked(userKey)
		return true
	}
	abort = func() bool {
		if !terminal.CompareAndSwap(false, true) {
			return false
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.deleting[userKey] != marker || marker.committed {
			return false
		}
		delete(l.deleting, userKey)
		return true
	}
	return commit, abort, nil
}

func (l *StartLimiter) finishDeletionLocked(userKey string) {
	marker := l.deleting[userKey]
	if marker == nil || !marker.committed {
		return
	}
	user := l.users[userKey]
	if user != nil {
		for _, event := range user.events {
			if event.state == startTentative {
				return
			}
		}
	}
	if l.continuity == nil {
		delete(l.users, userKey)
	}
	delete(l.deleting, userKey)
}

func (l *StartLimiter) removeEventLocked(userKey string, user *startUser, target *startEvent) {
	for index, event := range user.events {
		if event != target {
			continue
		}
		copy(user.events[index:], user.events[index+1:])
		user.events[len(user.events)-1] = nil
		user.events = user.events[:len(user.events)-1]
		break
	}
	if len(user.events) == 0 {
		delete(l.users, userKey)
	}
}

func (l *StartLimiter) trackedUsersLocked() int {
	count := len(l.users)
	for userID := range l.deleting {
		if l.users[userID] == nil {
			count++
		}
	}
	return count
}

func (l *StartLimiter) purgeUserLocked(userKey string, now time.Time) {
	user := l.users[userKey]
	if user == nil {
		return
	}
	write := 0
	for _, event := range user.events {
		if event.state == startTentative || event.expires.After(now) {
			user.events[write] = event
			write++
		}
	}
	for index := write; index < len(user.events); index++ {
		user.events[index] = nil
	}
	user.events = user.events[:write]
	if write == 0 {
		delete(l.users, userKey)
	}
	l.finishDeletionLocked(userKey)
}

func (l *StartLimiter) purgeAllLocked(now time.Time) {
	for userID := range l.users {
		l.purgeUserLocked(userID, now)
	}
}

// Close clears all process-local state and prevents future starts. It owns no
// goroutine and is safe to call repeatedly.
func (l *StartLimiter) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	l.users = make(map[string]*startUser)
	l.deleting = make(map[string]*deletionMarker)
	return nil
}
