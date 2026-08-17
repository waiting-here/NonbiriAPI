package flowcontrol

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"nonbiriapi/internal/ratelimit"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func testRPMConfig() ratelimit.RPMConfig {
	return ratelimit.RPMConfig{
		Window:       10 * time.Second,
		GlobalLimit:  3,
		PerUserLimit: 2,
		MaxUserKeys:  8,
		MaxEvents:    8,
		MaxKeyBytes:  16,
	}
}

func newTestController(t *testing.T, config ratelimit.RPMConfig, resolver UserLimitResolver, clock *fakeClock) *Controller {
	t.Helper()
	controller, err := newWithClock(Config{RPM: config, UserLimits: resolver}, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	return controller
}

func admit(t *testing.T, controller *Controller, userID int64) *Reservation {
	t.Helper()
	reservation, retryAfter, err := controller.Admit(context.Background(), userID)
	if err != nil || reservation == nil || retryAfter != 0 {
		t.Fatalf("admit user=%d: reservation=%v retryAfter=%v err=%v", userID, reservation, retryAfter, err)
	}
	return reservation
}

func deny(t *testing.T, controller *Controller, userID int64) (time.Duration, error) {
	t.Helper()
	reservation, retryAfter, err := controller.Admit(context.Background(), userID)
	if reservation != nil || !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limited for user=%d: reservation=%v err=%v", userID, reservation, err)
	}
	return retryAfter, err
}

func TestAdmitGlobalAndPerUserDenyWithRetryAfter(t *testing.T) {
	clock := newFakeClock()
	controller := newTestController(t, testRPMConfig(), nil, clock)

	// Per-user window: two admits fill user 1 against PerUserLimit=2.
	if reservation := admit(t, controller, 1); !reservation.Active() {
		t.Fatal("reservation should be active")
	}
	admit(t, controller, 1)
	retryAfter, err := deny(t, controller, 1)
	if err == nil || retryAfter <= 0 || retryAfter > MaxRetryAfter {
		t.Fatalf("per-user denial retry-after=%v", retryAfter)
	}

	// Global window: user 2 fills the third slot, user 3 hits the global cap.
	admit(t, controller, 2)
	retryAfter, err = deny(t, controller, 3)
	if err == nil || retryAfter <= 0 || retryAfter > MaxRetryAfter {
		t.Fatalf("global denial retry-after=%v", retryAfter)
	}

	// Window expiry restores both windows.
	clock.Advance(10 * time.Second)
	admit(t, controller, 3)
}

func TestAdmitUserIsolation(t *testing.T) {
	controller := newTestController(t, testRPMConfig(), nil, nil)
	admit(t, controller, 1)
	admit(t, controller, 1)
	// User 1 is full, but user 2 still has its own budget.
	admit(t, controller, 2)
	// Global cap (3) is now reached; user 3 is denied globally.
	deny(t, controller, 3)
}

func TestAdmitPerUserClampFromServer(t *testing.T) {
	clock := newFakeClock()
	config := testRPMConfig() // PerUserLimit ceiling = 2

	t.Run("over-ceiling value clamped", func(t *testing.T) {
		controller := newTestController(t, config, func(_ context.Context, _ int64) (int, bool, error) {
			return 100, true, nil
		}, clock)
		admit(t, controller, 1)
		admit(t, controller, 1)
		deny(t, controller, 1) // clamped to 2, not 100
	})

	t.Run("invalid values fall back to ceiling", func(t *testing.T) {
		for _, invalid := range []int{0, -7} {
			controller := newTestController(t, config, func(_ context.Context, _ int64) (int, bool, error) {
				return invalid, true, nil
			}, newFakeClock())
			admit(t, controller, 1)
			admit(t, controller, 1)
			deny(t, controller, 1)
		}
	})

	t.Run("no resolver means default ceiling", func(t *testing.T) {
		controller := newTestController(t, config, nil, newFakeClock())
		admit(t, controller, 1)
		admit(t, controller, 1)
		deny(t, controller, 1)
	})

	t.Run("no custom cap means default ceiling", func(t *testing.T) {
		controller := newTestController(t, config, func(_ context.Context, _ int64) (int, bool, error) {
			return 0, false, nil
		}, newFakeClock())
		admit(t, controller, 1)
		admit(t, controller, 1)
		deny(t, controller, 1)
	})

	t.Run("within-ceiling value honored", func(t *testing.T) {
		controller := newTestController(t, config, func(_ context.Context, _ int64) (int, bool, error) {
			return 1, true, nil
		}, newFakeClock())
		admit(t, controller, 1)
		deny(t, controller, 1) // custom cap 1, not ceiling 2
	})

	t.Run("resolver failure fails closed", func(t *testing.T) {
		controller := newTestController(t, config, func(_ context.Context, _ int64) (int, bool, error) {
			return 0, false, errors.New("db down")
		}, newFakeClock())
		reservation, _, err := controller.Admit(context.Background(), 1)
		if reservation != nil || err == nil || errors.Is(err, ErrRateLimited) {
			t.Fatalf("resolver failure must be a hard error: reservation=%v err=%v", reservation, err)
		}
	})
}

func TestReservationLifecycleCommitConsumesReleaseRefunds(t *testing.T) {
	clock := newFakeClock()
	controller := newTestController(t, testRPMConfig(), nil, clock)

	reservation := admit(t, controller, 1)
	if !reservation.Commit() || reservation.Commit() || reservation.Release() {
		t.Fatal("commit must win exactly once and be terminal")
	}
	if reservation.Active() {
		t.Fatal("committed reservation must not be active")
	}
	// Committed event still consumes the window.
	admit(t, controller, 1)
	deny(t, controller, 1)

	clock.Advance(10 * time.Second)

	// Release refunds: user 1 can admit PerUserLimit times again.
	released := admit(t, controller, 1)
	if !released.Release() || released.Release() || released.Commit() {
		t.Fatal("release must win exactly once and be terminal")
	}
	if released.Active() {
		t.Fatal("released reservation must not be active")
	}
	admit(t, controller, 1)
	admit(t, controller, 1)
	deny(t, controller, 1)
}

func TestAdmitInvalidUsersAndClosedController(t *testing.T) {
	controller := newTestController(t, testRPMConfig(), nil, nil)

	if reservation, _, err := controller.Admit(context.Background(), 0); reservation != nil || !errors.Is(err, ErrInvalidUser) {
		t.Fatalf("zero user id: reservation=%v err=%v", reservation, err)
	}
	if reservation, _, err := controller.Admit(context.Background(), -3); reservation != nil || !errors.Is(err, ErrInvalidUser) {
		t.Fatalf("negative user id: reservation=%v err=%v", reservation, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if reservation, _, err := controller.Admit(cancelled, 1); reservation != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled admit: reservation=%v err=%v", reservation, err)
	}

	var nilController *Controller
	if reservation, _, err := nilController.Admit(context.Background(), 1); reservation != nil || !errors.Is(err, ErrClosed) {
		t.Fatalf("nil controller: reservation=%v err=%v", reservation, err)
	}

	if err := controller.Close(); err != nil {
		t.Fatal(err)
	}
	if err := controller.Close(); err != nil {
		t.Fatal("close must be idempotent")
	}
	if reservation, _, err := controller.Admit(context.Background(), 1); reservation != nil || !errors.Is(err, ErrClosed) {
		t.Fatalf("admit after close: reservation=%v err=%v", reservation, err)
	}
}

func TestCloseForceReleasesInFlightReservations(t *testing.T) {
	controller := newTestController(t, testRPMConfig(), nil, nil)
	reservationA := admit(t, controller, 1)
	reservationB := admit(t, controller, 2)
	if !reservationA.Active() || !reservationB.Active() {
		t.Fatal("reservations should be active before close")
	}
	if err := controller.Close(); err != nil {
		t.Fatal(err)
	}
	// Force-released: terminal actions become no-ops, nothing leaks.
	if reservationA.Active() || reservationB.Active() {
		t.Fatal("reservations must be inactive after close")
	}
	if reservationA.Commit() || reservationB.Release() {
		t.Fatal("terminal action after close must be a no-op")
	}
}

func TestSetLimitsReplacesCapsNotCounters(t *testing.T) {
	clock := newFakeClock()
	controller := newTestController(t, testRPMConfig(), nil, clock)

	admit(t, controller, 1)
	admit(t, controller, 2)
	admit(t, controller, 3)
	deny(t, controller, 4) // global cap 3 reached

	if err := controller.SetLimits(ratelimit.RPMLimits{GlobalLimit: 4, PerUserLimit: 2}); err != nil {
		t.Fatal(err)
	}
	// Existing events are not discarded: the fourth admit brings the count to
	// 4, not 1.
	admit(t, controller, 4)
	deny(t, controller, 5) // still 3 committed events + 1 active = 4

	// Lowering the cap again tightens immediately for new admissions.
	if err := controller.SetLimits(ratelimit.RPMLimits{GlobalLimit: 2, PerUserLimit: 2}); err != nil {
		t.Fatal(err)
	}
	deny(t, controller, 6)

	if err := controller.SetLimits(ratelimit.RPMLimits{GlobalLimit: 0, PerUserLimit: 2}); err == nil {
		t.Fatal("invalid limits must be rejected")
	}
	limits := controller.Limits()
	if limits.GlobalLimit != 2 || limits.PerUserLimit != 2 {
		t.Fatalf("limits snapshot = %#v", limits)
	}
}

func TestBoundedUserKeysFailClosed(t *testing.T) {
	config := testRPMConfig()
	config.GlobalLimit = 100
	config.PerUserLimit = 100
	config.MaxUserKeys = 4
	config.MaxEvents = 256
	controller := newTestController(t, config, nil, nil)

	first := admit(t, controller, 1)
	for userID := int64(2); userID <= 4; userID++ {
		admit(t, controller, userID)
	}
	// Fifth distinct identity is refused even though the windows have room:
	// the bounded store fails closed instead of evicting a live identity.
	retryAfter, err := deny(t, controller, 5)
	if err == nil || retryAfter <= 0 || retryAfter > MaxRetryAfter {
		t.Fatalf("capacity denial retry-after=%v", retryAfter)
	}
	// The live identity was not evicted.
	if !first.Active() {
		t.Fatal("capacity pressure must not evict an existing reservation")
	}
}

func TestRetryAfterBounds(t *testing.T) {
	if got := boundedRetryAfter(0); got != DefaultRetryAfter {
		t.Fatalf("zero retry-after = %v", got)
	}
	if got := boundedRetryAfter(-time.Second); got != DefaultRetryAfter {
		t.Fatalf("negative retry-after = %v", got)
	}
	if got := boundedRetryAfter(48 * time.Hour); got != MaxRetryAfter {
		t.Fatalf("oversized retry-after = %v", got)
	}
	if got := boundedRetryAfter(3 * time.Second); got != 3*time.Second {
		t.Fatalf("in-range retry-after = %v", got)
	}
	if got := retryAfterSeconds(0); got != 1 {
		t.Fatalf("seconds(0) = %d", got)
	}
	if got := retryAfterSeconds(500 * time.Millisecond); got != 1 {
		t.Fatalf("seconds(500ms) = %d", got)
	}
	if got := retryAfterSeconds(time.Second); got != 1 {
		t.Fatalf("seconds(1s) = %d", got)
	}
	if got := retryAfterSeconds(MaxRetryAfter); got != 3600 {
		t.Fatalf("seconds(1h) = %d", got)
	}
}

func TestDBUserLimitResolverNilStore(t *testing.T) {
	controller := newTestController(t, testRPMConfig(), DBUserLimitResolver(nil), nil)
	reservation, _, err := controller.Admit(context.Background(), 1)
	if reservation != nil || err == nil || errors.Is(err, ErrRateLimited) {
		t.Fatalf("nil store resolver: reservation=%v err=%v", reservation, err)
	}
}

func TestZeroConfigUsesFiniteDefaults(t *testing.T) {
	controller, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	limits := controller.Limits()
	if limits.GlobalLimit != ratelimit.DefaultRPMGlobalLimit || limits.PerUserLimit != ratelimit.DefaultRPMPerUserLimit {
		t.Fatalf("default limits = %#v", limits)
	}
}

func TestInvalidConfigRejected(t *testing.T) {
	config := testRPMConfig()
	config.GlobalLimit = 0
	if _, err := New(Config{RPM: config}); err == nil {
		t.Fatal("invalid config must be rejected")
	}
}
