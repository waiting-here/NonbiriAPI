package flowcontrol

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

func generousRPMConfig() ratelimit.RPMConfig {
	config := testRPMConfig()
	config.GlobalLimit = 8
	config.PerUserLimit = 8
	return config
}

func fixedUserLimits(concurrency int) UserLimitResolver {
	return func(context.Context, int64) (UserLimits, error) {
		return UserLimits{ConcurrencyLimit: concurrency}, nil
	}
}

func TestConcurrencyLimitPrecedesAndDoesNotTouchRPM(t *testing.T) {
	var denied atomic.Int64
	controller, err := newWithClock(Config{
		RPM:        generousRPMConfig(),
		UserLimits: fixedUserLimits(1),
		OnDenied: func(context.Context, int64, ratelimit.RPMReason) {
			denied.Add(1)
		},
	}, newFakeClock())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })

	first := reserve(t, controller, 1)
	// Closing only RPM makes any Limits-independent Reserve return ErrClosed.
	// The second request must still be classified by the already-full
	// concurrency gate, proving that path never calls into RPM at all.
	if err := controller.limiter.Close(); err != nil {
		t.Fatal(err)
	}
	reservation, retryAfter, err := controller.Admit(context.Background(), 1)
	if reservation != nil || retryAfter != 0 || !errors.Is(err, ErrConcurrencyLimited) {
		t.Fatalf("reservation=%v retry=%v err=%v", reservation, retryAfter, err)
	}
	if denied.Load() != 0 {
		t.Fatalf("concurrency denial invoked RPM observer %d times", denied.Load())
	}
	first.Release()
	if len(controller.userConcurrency.users) != 0 {
		t.Fatalf("active map leaked after terminal release: %d", len(controller.userConcurrency.users))
	}
}

func TestConcurrencyDynamicLimitsAndIsolation(t *testing.T) {
	var limit atomic.Int64
	limit.Store(2)
	controller := newTestController(t, generousRPMConfig(), func(context.Context, int64) (UserLimits, error) {
		return UserLimits{ConcurrencyLimit: int(limit.Load())}, nil
	}, nil)

	a := reserve(t, controller, 1)
	b := reserve(t, controller, 1)
	if reservation, retry, err := controller.Admit(context.Background(), 1); reservation != nil || retry != 0 || !errors.Is(err, ErrConcurrencyLimited) {
		t.Fatalf("third at limit 2: reservation=%v retry=%v err=%v", reservation, retry, err)
	}
	// Another identity has its own counter.
	other := reserve(t, controller, 2)

	limit.Store(1)
	if !a.Active() || !b.Active() {
		t.Fatal("lowering must not cancel in-flight permits")
	}
	a.Release()
	if reservation, _, err := controller.Admit(context.Background(), 1); reservation != nil || !errors.Is(err, ErrConcurrencyLimited) {
		t.Fatalf("active=1 at lowered limit=1: reservation=%v err=%v", reservation, err)
	}
	limit.Store(3)
	c := reserve(t, controller, 1)
	limit.Store(5) // NULL is projected by the DB resolver to built-in 5.
	d := reserve(t, controller, 1)
	e := reserve(t, controller, 1)
	f := reserve(t, controller, 1)
	if reservation, _, err := controller.Admit(context.Background(), 1); reservation != nil || !errors.Is(err, ErrConcurrencyLimited) {
		t.Fatalf("sixth at reset default 5: reservation=%v err=%v", reservation, err)
	}
	for _, active := range []*Reservation{b, c, d, e, f, other} {
		active.Release()
	}
}

func TestConcurrencyMapCapacityFailsClosedAndRecovers(t *testing.T) {
	controller, err := newWithClock(Config{
		RPM:                generousRPMConfig(),
		UserLimits:         fixedUserLimits(5),
		MaxConcurrentUsers: 2,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	a := reserve(t, controller, 1)
	b := reserve(t, controller, 2)
	if reservation, retry, err := controller.Admit(context.Background(), 3); reservation != nil || retry != 0 || !errors.Is(err, ErrConcurrencyLimited) {
		t.Fatalf("capacity reservation=%v retry=%v err=%v", reservation, retry, err)
	}
	a.Release()
	c := reserve(t, controller, 3)
	b.Release()
	c.Release()
	if got := len(controller.userConcurrency.users); got != 0 {
		t.Fatalf("active map size=%d", got)
	}
}

func TestConcurrencyHardRangeUsesRepositoryAuthority(t *testing.T) {
	if maxUserConcurrencyLimit != db.MaxUserConcurrencyLimit {
		t.Fatalf("runtime max=%d repository max=%d", maxUserConcurrencyLimit, db.MaxUserConcurrencyLimit)
	}
	controller := newTestController(t, generousRPMConfig(), func(context.Context, int64) (UserLimits, error) {
		return UserLimits{ConcurrencyLimit: db.MaxUserConcurrencyLimit}, nil
	}, nil)
	reservation := reserve(t, controller, 1)
	reservation.Release()

	invalid := newTestController(t, generousRPMConfig(), func(context.Context, int64) (UserLimits, error) {
		return UserLimits{ConcurrencyLimit: db.MaxUserConcurrencyLimit + 1}, nil
	}, nil)
	if reservation, retry, err := invalid.Admit(context.Background(), 1); reservation != nil || retry != 0 || !errors.Is(err, ErrInvalidUser) {
		t.Fatalf("above repository max: reservation=%v retry=%v err=%v", reservation, retry, err)
	}
}

func TestRPMFailureReleasesConcurrencyPermit(t *testing.T) {
	controller := newTestController(t, generousRPMConfig(), fixedUserLimits(1), nil)
	if err := controller.limiter.Close(); err != nil {
		t.Fatal(err)
	}
	if reservation, retry, err := controller.Admit(context.Background(), 1); reservation != nil || retry != 0 || !errors.Is(err, ErrClosed) {
		t.Fatalf("closed RPM: reservation=%v retry=%v err=%v", reservation, retry, err)
	}
	controller.userConcurrency.mu.Lock()
	defer controller.userConcurrency.mu.Unlock()
	if got := len(controller.userConcurrency.users); got != 0 {
		t.Fatalf("RPM error leaked %d concurrency states", got)
	}
}

func TestRealRPMWindowDenialReleasesConcurrencyPermit(t *testing.T) {
	clock := newFakeClock()
	config := generousRPMConfig()
	config.GlobalLimit = 1
	config.PerUserLimit = 1
	controller := newTestController(t, config, fixedUserLimits(1), clock)

	first := reserve(t, controller, 1)
	if !first.Commit() {
		t.Fatal("first RPM reservation commit")
	}
	reservation, retryAfter, err := controller.Admit(context.Background(), 1)
	if reservation != nil || !errors.Is(err, ErrRateLimited) || retryAfter <= 0 {
		t.Fatalf("window denial reservation=%v retry=%v err=%v", reservation, retryAfter, err)
	}
	controller.userConcurrency.mu.Lock()
	activeUsers := len(controller.userConcurrency.users)
	controller.userConcurrency.mu.Unlock()
	if activeUsers != 0 {
		t.Fatalf("RPM window denial leaked %d concurrency states", activeUsers)
	}

	clock.Advance(config.Window)
	recovered := reserve(t, controller, 1)
	recovered.Release()
}

func TestAdmissionRetirementLinearizesResolverAndDBChange(t *testing.T) {
	var active atomic.Bool
	active.Store(true)
	resolverEntered := make(chan struct{}, 1)
	resolverContinue := make(chan struct{})
	var blockFirst atomic.Bool
	blockFirst.Store(true)
	controller := newTestController(t, generousRPMConfig(), func(ctx context.Context, _ int64) (UserLimits, error) {
		if blockFirst.CompareAndSwap(true, false) {
			resolverEntered <- struct{}{}
			select {
			case <-resolverContinue:
			case <-ctx.Done():
				return UserLimits{}, ctx.Err()
			}
		}
		if !active.Load() {
			return UserLimits{}, ErrInvalidUser
		}
		return UserLimits{ConcurrencyLimit: 5}, nil
	}, nil)

	type admitResult struct {
		reservation *Reservation
		err         error
	}
	admitted := make(chan admitResult, 1)
	go func() {
		reservation, _, err := controller.Admit(context.Background(), 7)
		admitted <- admitResult{reservation: reservation, err: err}
	}()
	<-resolverEntered

	retirementReady := make(chan *UserRetirement, 1)
	go func() {
		retirement, err := controller.BeginUserRetirement(7)
		if err != nil {
			retirementReady <- nil
			return
		}
		retirementReady <- retirement
	}()
	select {
	case <-retirementReady:
		t.Fatal("retirement crossed an in-flight live-account resolver")
	case <-time.After(20 * time.Millisecond):
	}
	close(resolverContinue)
	result := <-admitted
	if result.err != nil || result.reservation == nil {
		t.Fatalf("request that linearized first: reservation=%v err=%v", result.reservation, result.err)
	}
	retirement := <-retirementReady
	if retirement == nil {
		t.Fatal("begin retirement")
	}
	// This assignment models the DB ban/delete while the write barrier is
	// held. Commit calls Forget before the barrier unlocks.
	active.Store(false)
	if !retirement.Commit() || retirement.Commit() || retirement.Abort() {
		t.Fatal("retirement commit must be exactly once")
	}
	result.reservation.Release()
	if reservation, _, err := controller.Admit(context.Background(), 7); reservation != nil || !errors.Is(err, ErrInvalidUser) {
		t.Fatalf("post-retirement admission=%v err=%v", reservation, err)
	}
}

func TestRetirementFirstBlocksResolverAndAbortRecovers(t *testing.T) {
	resolverEntered := make(chan struct{}, 1)
	controller := newTestController(t, generousRPMConfig(), func(context.Context, int64) (UserLimits, error) {
		resolverEntered <- struct{}{}
		return UserLimits{ConcurrencyLimit: 5}, nil
	}, nil)

	retirement, err := controller.BeginUserRetirement(11)
	if err != nil {
		t.Fatal(err)
	}
	admitted := make(chan *Reservation, 1)
	go func() {
		reservation, _, _ := controller.Admit(context.Background(), 11)
		admitted <- reservation
	}()
	select {
	case <-resolverEntered:
		t.Fatal("resolver ran while retirement write barrier was held")
	case <-time.After(20 * time.Millisecond):
	}
	if !retirement.Abort() || retirement.Abort() || retirement.Commit() {
		t.Fatal("retirement abort must be exactly once")
	}
	select {
	case <-resolverEntered:
	case <-time.After(time.Second):
		t.Fatal("resolver did not resume after abort")
	}
	reservation := <-admitted
	if reservation == nil {
		t.Fatal("admission did not recover after abort")
	}
	reservation.Release()
}

func TestAdmissionGuardPanicAndStripeCollisionDoNotDeadlock(t *testing.T) {
	t.Run("resolver panic releases read guard", func(t *testing.T) {
		controller := newTestController(t, generousRPMConfig(), func(context.Context, int64) (UserLimits, error) {
			panic("resolver panic")
		}, nil)
		func() {
			defer func() { _ = recover() }()
			_, _, _ = controller.Admit(context.Background(), 1)
		}()
		ready := make(chan *UserRetirement, 1)
		go func() {
			retirement, _ := controller.BeginUserRetirement(1)
			ready <- retirement
		}()
		select {
		case retirement := <-ready:
			retirement.Abort()
		case <-time.After(time.Second):
			t.Fatal("read guard leaked on panic")
		}
	})

	t.Run("same stripe other user eventually proceeds", func(t *testing.T) {
		var mu sync.Mutex
		seen := make([]int64, 0, 1)
		controller := newTestController(t, generousRPMConfig(), func(_ context.Context, userID int64) (UserLimits, error) {
			mu.Lock()
			seen = append(seen, userID)
			mu.Unlock()
			return UserLimits{ConcurrencyLimit: 5}, nil
		}, nil)
		const userA int64 = 1
		const userB int64 = userA + userAdmissionGateStripes
		retirement, err := controller.BeginUserRetirement(userA)
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan *Reservation, 1)
		go func() {
			reservation, _, _ := controller.Admit(context.Background(), userB)
			result <- reservation
		}()
		select {
		case <-result:
			t.Fatal("same-stripe reader crossed write guard")
		case <-time.After(20 * time.Millisecond):
		}
		retirement.Abort()
		select {
		case reservation := <-result:
			if reservation == nil {
				t.Fatal("same-stripe user failed after guard release")
			}
			reservation.Release()
		case <-time.After(time.Second):
			t.Fatal("same-stripe user remained blocked")
		}
	})
}

func TestForgetUserActiveIdleRepeatAndFinalCleanup(t *testing.T) {
	limiter, err := newUserConcurrencyLimiter(8)
	if err != nil {
		t.Fatal(err)
	}
	permit, err := limiter.tryAcquire(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	limiter.forgetUser(1)
	limiter.forgetUser(1)
	if replacement, err := limiter.tryAcquire(1, 2); replacement != nil || !errors.Is(err, ErrConcurrencyLimited) {
		t.Fatalf("retiring replacement=%v err=%v", replacement, err)
	}
	if !permit.Release() || permit.Release() {
		t.Fatal("permit release must win once")
	}
	limiter.mu.Lock()
	if len(limiter.users) != 0 {
		limiter.mu.Unlock()
		t.Fatalf("retiring state leaked: %d", len(limiter.users))
	}
	limiter.mu.Unlock()
	// Idle/repeated forgets are no-ops and do not consume bounded capacity.
	limiter.forgetUser(1)
	limiter.forgetUser(1)
	newPermit, err := limiter.tryAcquire(1, 2)
	if err != nil {
		t.Fatalf("acquire after idle forget: %v", err)
	}
	newPermit.Release()
}

func TestForgetAcquireReleaseCloseShuffle(t *testing.T) {
	controller, err := New(Config{
		RPM: generousRPMConfig(), UserLimits: fixedUserLimits(32), MaxConcurrentUsers: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		wait.Add(1)
		go func(seed int) {
			defer wait.Done()
			for i := 0; i < 300; i++ {
				userID := int64((seed+i)%8 + 1)
				reservation, _, err := controller.Admit(context.Background(), userID)
				switch {
				case err == nil:
					if (seed+i)%2 == 0 {
						reservation.Commit()
					} else {
						reservation.Release()
					}
				case errors.Is(err, ErrConcurrencyLimited), errors.Is(err, ErrRateLimited), errors.Is(err, ErrClosed):
				default:
					t.Errorf("admit user=%d err=%v", userID, err)
				}
				if i%7 == 0 {
					controller.ForgetUser(userID)
				}
			}
		}(worker)
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		for i := 0; i < 500; i++ {
			controller.ForgetUser(int64(i%8 + 1))
			if i == 250 {
				_ = controller.Close()
			}
		}
	}()
	wait.Wait()
	if err := controller.Close(); err != nil {
		t.Fatal(err)
	}
	controller.userConcurrency.mu.Lock()
	defer controller.userConcurrency.mu.Unlock()
	if len(controller.userConcurrency.users) != 0 {
		t.Fatalf("active states after shuffle close=%d", len(controller.userConcurrency.users))
	}
}
