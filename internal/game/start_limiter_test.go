package game

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type limiterClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *limiterClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *limiterClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

func newTestStartLimiter(t *testing.T, maxUsers int) (*StartLimiter, *limiterClock) {
	t.Helper()
	clock := &limiterClock{now: time.Unix(1_700_000_000, 0)}
	limiter, err := NewStartLimiter(StartLimiterConfig{Now: clock.Now, MaxUsers: maxUsers})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = limiter.Close() })
	return limiter, clock
}

func TestStartLimiterTentativeCommitReleaseAndWindow(t *testing.T) {
	limiter, clock := newTestStartLimiter(t, 4)
	reservations := make([]*StartReservation, FishingStartsPerMinute)
	for index := range reservations {
		reservation, _, err := limiter.Reserve(1)
		if err != nil {
			t.Fatalf("reserve %d: %v", index, err)
		}
		reservations[index] = reservation
		if index%2 == 0 {
			reservation.Commit()
		}
	}
	if _, retry, err := limiter.Reserve(1); !errors.Is(err, ErrStartRateLimited) || retry != time.Minute {
		t.Fatalf("limit = retry %v err %v", retry, err)
	}
	if !reservations[1].Release() || reservations[1].Release() || reservations[1].Commit() {
		t.Fatal("reservation did not terminate exactly once")
	}
	replacement, _, err := limiter.Reserve(1)
	if err != nil {
		t.Fatalf("replacement: %v", err)
	}
	replacement.Commit()
	clock.Advance(time.Minute)
	if reservation, retry, err := limiter.Reserve(1); err != nil || retry != 0 || !reservation.Commit() {
		t.Fatalf("exact window boundary = %v, %v, %v", reservation, retry, err)
	}
}

func TestStartLimiterCapacityAndDeletionAbort(t *testing.T) {
	limiter, _ := newTestStartLimiter(t, 1)
	reservation, _, err := limiter.Reserve(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := limiter.Reserve(2); !errors.Is(err, ErrStartCapacity) {
		t.Fatalf("capacity = %v", err)
	}
	commit, abort, err := limiter.BeginUserDeletion(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := limiter.Reserve(1); !errors.Is(err, ErrUserDeleting) {
		t.Fatalf("reserve during delete = %v", err)
	}
	if !abort() || abort() || commit() {
		t.Fatal("abort did not win exactly once")
	}
	if !reservation.Commit() {
		t.Fatal("pre-delete reservation was not preserved by abort")
	}
	if second, _, err := limiter.Reserve(1); err != nil || !second.Release() {
		t.Fatalf("reserve after abort = %v, %v", second, err)
	}
}

func TestStartLimiterCapacityEvictsExpiredUsers(t *testing.T) {
	limiter, clock := newTestStartLimiter(t, 1)
	first, _, err := limiter.Reserve(1)
	if err != nil || !first.Commit() {
		t.Fatalf("first reservation = %v, %v", first, err)
	}
	clock.Advance(time.Minute)
	second, _, err := limiter.Reserve(2)
	if err != nil || !second.Commit() {
		t.Fatalf("expired user retained capacity = %v, %v", second, err)
	}
}

func TestStartLimiterCommittedDeletionWaitsForTentative(t *testing.T) {
	limiter, _ := newTestStartLimiter(t, 2)
	reservation, _, err := limiter.Reserve(1)
	if err != nil {
		t.Fatal(err)
	}
	commit, abort, err := limiter.BeginUserDeletion(1)
	if err != nil {
		t.Fatal(err)
	}
	if !commit() || commit() || abort() {
		t.Fatal("commit did not win exactly once")
	}
	if _, _, err := limiter.Reserve(1); !errors.Is(err, ErrUserDeleting) {
		t.Fatalf("deletion marker drained before tentative reservation: %v", err)
	}
	if !reservation.Release() {
		t.Fatal("tentative reservation did not release")
	}
	if next, _, err := limiter.Reserve(1); err != nil || !next.Release() {
		t.Fatalf("post-drain limiter state = %v, %v", next, err)
	}
}

func TestStartLimiterClose(t *testing.T) {
	limiter, _ := newTestStartLimiter(t, 1)
	if err := limiter.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := limiter.Reserve(1); !errors.Is(err, ErrStartClosed) {
		t.Fatalf("reserve after close = %v", err)
	}
}
