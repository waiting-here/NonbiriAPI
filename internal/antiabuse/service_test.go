package antiabuse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func testStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db", "anti-abuse.db")
	dbtest.EnsureOwnerOnlyParent(t, path)
	key := bytes.Repeat([]byte{0x31}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func retirementRecorder(t *testing.T, store *db.Store, begins, commits, aborts *atomic.Int64) func(int64) (func() bool, func() bool, error) {
	t.Helper()
	return func(userID int64) (func() bool, func() bool, error) {
		current, err := store.GetUserByID(userID)
		if err != nil || current == nil || current.IsBanned {
			return nil, nil, errors.New("retirement ordering violation")
		}
		begins.Add(1)
		var done atomic.Bool
		commit := func() bool {
			if !done.CompareAndSwap(false, true) {
				return false
			}
			commits.Add(1)
			return true
		}
		abort := func() bool {
			if !done.CompareAndSwap(false, true) {
				return false
			}
			aborts.Add(1)
			return true
		}
		return commit, abort, nil
	}
}

func TestAllAutomaticBanPathsUseRetirementBoundary(t *testing.T) {
	t.Run("rpm threshold", func(t *testing.T) {
		store := testStore(t)
		user := newUser(t, store, "retire-rpm")
		setPolicy(t, store, KeyRPMBanThreshold, "1")
		var begins, commits, aborts atomic.Int64
		svc, err := NewService(ServiceConfig{
			Store: store, BeginUserRetirement: retirementRecorder(t, store, &begins, &commits, &aborts),
		})
		if err != nil {
			t.Fatal(err)
		}
		svc.RPMDenied(context.Background(), user.ID, false, ratelimit.RPMUserLimit)
		if begins.Load() != 1 || commits.Load() != 1 || aborts.Load() != 0 {
			t.Fatalf("retirement begin=%d commit=%d abort=%d", begins.Load(), commits.Load(), aborts.Load())
		}
	})

	for _, tc := range []struct {
		name      string
		configure func(*testing.T, *db.Store)
	}{
		{"single violation", func(t *testing.T, store *db.Store) {
			setPolicy(t, store, KeyCharityViolationBanSeconds, "30")
		}},
		{"window threshold", func(t *testing.T, store *db.Store) {
			setPolicy(t, store, KeyCharityViolationBanThreshold, "1")
			setPolicy(t, store, KeyCharityViolationWindowBanSeconds, "30")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := testStore(t)
			user := newUser(t, store, "retire-"+tc.name)
			setPolicy(t, store, "charity_enabled", "1")
			setPolicy(t, store, KeyCharityMinChars, "3")
			tc.configure(t, store)
			var begins, commits, aborts atomic.Int64
			svc, err := NewService(ServiceConfig{
				Store: store, BeginUserRetirement: retirementRecorder(t, store, &begins, &commits, &aborts),
			})
			if err != nil {
				t.Fatal(err)
			}
			request := shortRequest(t)
			defer request.Clear()
			_ = svc.Preflight(context.Background(), user.ID, request)
			if begins.Load() != 1 || commits.Load() != 1 || aborts.Load() != 0 {
				t.Fatalf("retirement begin=%d commit=%d abort=%d", begins.Load(), commits.Load(), aborts.Load())
			}
		})
	}
}

func TestAutomaticBanCallbacksRunOutsideWindowStateLock(t *testing.T) {
	for _, kind := range []string{"rpm", "charity"} {
		t.Run(kind, func(t *testing.T) {
			store := testStore(t)
			user := newUser(t, store, "state-unlock-"+kind)
			if kind == "rpm" {
				setPolicy(t, store, KeyRPMBanThreshold, "1")
			} else {
				setPolicy(t, store, "charity_enabled", "1")
				setPolicy(t, store, KeyCharityMinChars, "3")
				setPolicy(t, store, KeyCharityViolationBanSeconds, "30")
			}
			var svc *Service
			begin := func(userID int64) (func() bool, func() bool, error) {
				svc.mu.Lock()
				window := svc.rpm[userID]
				if kind == "charity" {
					window = svc.charity[userID]
				}
				svc.mu.Unlock()
				if window == nil {
					return nil, nil, errors.New("missing event window")
				}
				// This lock acquisition is the regression assertion: a durable ban
				// callback must never be invoked while recordViolation/RPMDenied
				// still owns the bounded state mutex.
				window.mu.Lock()
				window.mu.Unlock()
				var done atomic.Bool
				finish := func() bool { return done.CompareAndSwap(false, true) }
				return finish, finish, nil
			}
			var err error
			svc, err = NewService(ServiceConfig{Store: store, BeginUserRetirement: begin})
			if err != nil {
				t.Fatal(err)
			}
			var request *openai.ChatRequest
			if kind == "charity" {
				request = shortRequest(t)
				defer request.Clear()
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				if kind == "rpm" {
					svc.RPMDenied(context.Background(), user.ID, false, ratelimit.RPMUserLimit)
					return
				}
				_ = svc.Preflight(context.Background(), user.ID, request)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("automatic ban callback ran under window state lock")
			}
		})
	}
}

func TestAutomaticBanMalformedRetirementFailsClosedWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		begin func(int64) (func() bool, func() bool, error)
	}{
		{"error", func(int64) (func() bool, func() bool, error) { return nil, nil, errors.New("unavailable") }},
		{"nil terminals", func(int64) (func() bool, func() bool, error) { return nil, nil, nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := testStore(t)
			user := newUser(t, store, "invalid-retirement-"+tc.name)
			setPolicy(t, store, "charity_enabled", "1")
			setPolicy(t, store, KeyCharityMinChars, "3")
			setPolicy(t, store, KeyCharityViolationBanSeconds, "30")
			svc, err := NewService(ServiceConfig{Store: store, BeginUserRetirement: tc.begin})
			if err != nil {
				t.Fatal(err)
			}
			request := shortRequest(t)
			defer request.Clear()
			if err := svc.Preflight(context.Background(), user.ID, request); !errors.Is(err, forward.ErrAntiAbuseUnavailable) {
				t.Fatalf("preflight err=%v", err)
			}
			current, err := store.GetUserByID(user.ID)
			if err != nil || current == nil || current.IsBanned {
				t.Fatalf("DB mutated current=%+v err=%v", current, err)
			}
		})
	}
}

func TestWindowEffectsReleaseStateLockBeforeRetirementCleanupCycle(t *testing.T) {
	store := testStore(t)
	user := newUser(t, store, "three-lock-cycle")
	setPolicy(t, store, "charity_enabled", "1")
	setPolicy(t, store, KeyCharityMinChars, "3")
	setPolicy(t, store, KeyCharityViolationDeductMilli, "7")
	setPolicy(t, store, KeyCharityViolationBanSeconds, "30")
	setPolicy(t, store, KeyCharityViolationWindowSeconds, "100")
	setPolicy(t, store, KeyCharitySuspendWindowSeconds, "100")

	baseNow := time.Unix(1_000, 0).UTC()
	var maintenancePhase atomic.Bool
	cleanupHasServiceLock := make(chan struct{})
	allowCleanupNow := make(chan struct{})
	var cleanupSignal sync.Once
	now := func() time.Time {
		if maintenancePhase.Load() {
			cleanupSignal.Do(func() { close(cleanupHasServiceLock) })
			<-allowCleanupNow
		}
		return baseNow
	}

	// stripe models flowcontrol.BeginUserRetirement's per-user write stripe.
	// The lifecycle goroutine holds it across PreDeleteUser/ForgetUser and then
	// aborts the simulated DB delete, allowing the anti-abuse ban to proceed.
	var stripe sync.Mutex
	stripeHeld := make(chan struct{})
	startForget := make(chan struct{})
	forgetAttempted := make(chan struct{})
	lifecycleDone := make(chan struct{})
	var startForgetOnce sync.Once
	startLifecycleForget := func() { startForgetOnce.Do(func() { close(startForget) }) }
	defer startLifecycleForget()
	var allowCleanupOnce sync.Once
	allowCleanup := func() { allowCleanupOnce.Do(func() { close(allowCleanupNow) }) }
	defer allowCleanup()

	var begins, commits, aborts atomic.Int64
	beginEntered := make(chan struct{})
	var beginSignal sync.Once
	beginRetirement := func(int64) (func() bool, func() bool, error) {
		begins.Add(1)
		beginSignal.Do(func() { close(beginEntered) })
		stripe.Lock()
		var done atomic.Bool
		terminal := func(commit bool) bool {
			if !done.CompareAndSwap(false, true) {
				return false
			}
			if commit {
				commits.Add(1)
			} else {
				aborts.Add(1)
			}
			stripe.Unlock()
			return true
		}
		return func() bool { return terminal(true) }, func() bool { return terminal(false) }, nil
	}
	svc, err := NewService(ServiceConfig{Store: store, Now: now, BeginUserRetirement: beginRetirement})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		stripe.Lock()
		close(stripeHeld)
		<-startForget
		close(forgetAttempted)
		svc.ForgetUser(user.ID)
		stripe.Unlock() // simulated delete failure calls retirement Abort
		close(lifecycleDone)
	}()

	request := shortRequest(t)
	defer request.Clear()
	preflightDone := make(chan error, 1)
	cleanupDone := make(chan struct{})

	wait := func(ch <-chan struct{}, label string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not complete", label)
		}
	}
	wait(stripeHeld, "lifecycle stripe acquisition")
	go func() { preflightDone <- svc.Preflight(context.Background(), user.ID, request) }()
	wait(beginEntered, "anti-abuse retirement begin")

	maintenancePhase.Store(true)
	go func() {
		svc.Cleanup()
		close(cleanupDone)
	}()
	wait(cleanupHasServiceLock, "maintenance service-map lock")
	startLifecycleForget()
	wait(forgetAttempted, "lifecycle ForgetUser attempt")
	allowCleanup()

	wait(cleanupDone, "maintenance cleanup")
	wait(lifecycleDone, "lifecycle ForgetUser")
	select {
	case err := <-preflightDone:
		if !errors.Is(err, forward.ErrCharityContentTooShort) {
			t.Fatalf("preflight err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("anti-abuse consequence did not complete")
	}

	if begins.Load() != 1 || commits.Load() != 1 || aborts.Load() != 0 {
		t.Fatalf("retirement begin=%d commit=%d abort=%d", begins.Load(), commits.Load(), aborts.Load())
	}
	current, err := store.GetUserByID(user.ID)
	if err != nil || current == nil || !current.IsBanned || current.Credits != -7 {
		t.Fatalf("durable consequence current=%+v err=%v", current, err)
	}
	var penalties int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM credit_ledger WHERE user_id=? AND kind=?`,
		user.ID, db.LedgerAntiAbusePenalty).Scan(&penalties); err != nil {
		t.Fatal(err)
	}
	if penalties != 1 {
		t.Fatalf("penalty rows=%d, want exactly one", penalties)
	}
	svc.mu.Lock()
	_, rpmExists := svc.rpm[user.ID]
	_, charityExists := svc.charity[user.ID]
	svc.mu.Unlock()
	if rpmExists || charityExists {
		t.Fatalf("ForgetUser left window state: rpm=%v charity=%v", rpmExists, charityExists)
	}
}

func TestWindowPinPreventsOrphanedThresholdEvents(t *testing.T) {
	recordPinnedEvent := func(t *testing.T, svc *Service, window *userWindow, now time.Time, duration time.Duration, operationID string) {
		t.Helper()
		window.effectsMu.Lock()
		window.mu.Lock()
		svc.pruneLocked(window, now, duration)
		accepted, fresh := svc.addEventLocked(window, now, operationID)
		window.mu.Unlock()
		window.effectsMu.Unlock()
		svc.unpinWindow(window)
		if !accepted || !fresh {
			t.Fatalf("pinned event accepted=%v fresh=%v", accepted, fresh)
		}
	}
	assertMapped := func(t *testing.T, svc *Service, store map[int64]*userWindow, userID int64, want *userWindow) {
		t.Helper()
		svc.mu.Lock()
		got := store[userID]
		svc.mu.Unlock()
		if got != want {
			t.Fatalf("cleanup orphaned pinned window: got=%p want=%p", got, want)
		}
	}

	t.Run("new RPM window", func(t *testing.T) {
		store := testStore(t)
		user := newUser(t, store, "pin-rpm")
		setPolicy(t, store, KeyRPMBanThreshold, "2")
		setPolicy(t, store, KeyRPMBanWindowSeconds, "100")
		setPolicy(t, store, KeyRPMBanDurationSeconds, "30")
		now := time.Unix(2_000, 0).UTC()
		var begins, commits, aborts atomic.Int64
		svc, err := NewService(ServiceConfig{
			Store: store, Now: func() time.Time { return now },
			BeginUserRetirement: retirementRecorder(t, store, &begins, &commits, &aborts),
		})
		if err != nil {
			t.Fatal(err)
		}

		// Pause the first caller immediately after getWindow releases Service.mu
		// and before it takes either window lock. Cleanup executes fully in that
		// exact gap; the map ownership pin must keep the empty window reachable.
		duration := 100 * time.Second
		window, err := svc.getWindow(svc.rpm, user.ID, duration)
		if err != nil {
			t.Fatalf("get RPM window: %v", err)
		}
		svc.mu.Lock()
		svc.cleanupMapLocked(svc.rpm, now, duration)
		svc.mu.Unlock()
		assertMapped(t, svc, svc.rpm, user.ID, window)
		recordPinnedEvent(t, svc, window, now, duration, "gap-rpm-first")

		svc.RPMDenied(context.Background(), user.ID, false, ratelimit.RPMUserLimit)
		svc.RPMDenied(context.Background(), user.ID, false, ratelimit.RPMUserLimit)
		current, err := store.GetUserByID(user.ID)
		if err != nil || current == nil || !current.IsBanned {
			t.Fatalf("RPM threshold lost across cleanup: user=%+v err=%v", current, err)
		}
		if begins.Load() != 1 || commits.Load() != 1 || aborts.Load() != 0 {
			t.Fatalf("RPM side effects begin=%d commit=%d abort=%d", begins.Load(), commits.Load(), aborts.Load())
		}
	})

	t.Run("existing prunable charity window", func(t *testing.T) {
		store := testStore(t)
		user := newUser(t, store, "pin-charity")
		setPolicy(t, store, KeyCharityViolationWindowSeconds, "100")
		setPolicy(t, store, KeyCharitySuspendWindowSeconds, "100")
		setPolicy(t, store, KeyCharityViolationBanThreshold, "2")
		setPolicy(t, store, KeyCharityViolationWindowBanSeconds, "30")
		now := time.Unix(3_000, 0).UTC()
		var begins, commits, aborts atomic.Int64
		svc, err := NewService(ServiceConfig{
			Store: store, Now: func() time.Time { return now },
			BeginUserRetirement: retirementRecorder(t, store, &begins, &commits, &aborts),
		})
		if err != nil {
			t.Fatal(err)
		}
		duration := 100 * time.Second

		// Seed an existing window whose only event is now prunable.
		window, err := svc.getWindow(svc.charity, user.ID, duration)
		if err != nil {
			t.Fatalf("get charity seed window: %v", err)
		}
		recordPinnedEvent(t, svc, window, now.Add(-duration-time.Second), duration, "expired-charity")

		// A new caller pins that existing window before cleanup. Cleanup would
		// prune/delete it at this instant without the lease; the resumed caller
		// then records the first current event in the still-mapped object.
		pinned, err := svc.getWindow(svc.charity, user.ID, duration)
		if err != nil || pinned != window {
			t.Fatalf("get existing charity window=%p err=%v want=%p", pinned, err, window)
		}
		svc.mu.Lock()
		svc.cleanupMapLocked(svc.charity, now, duration)
		svc.mu.Unlock()
		assertMapped(t, svc, svc.charity, user.ID, window)
		recordPinnedEvent(t, svc, window, now, duration, "gap-charity-first")

		cfg := readConfig(store)
		if err := svc.recordViolation(context.Background(), user.ID, 1, cfg, now); err != nil {
			t.Fatalf("second charity violation: %v", err)
		}
		if err := svc.recordViolation(context.Background(), user.ID, 1, cfg, now); err != nil {
			t.Fatalf("third charity violation: %v", err)
		}
		current, err := store.GetUserByID(user.ID)
		if err != nil || current == nil || !current.IsBanned {
			t.Fatalf("charity threshold lost across cleanup: user=%+v err=%v", current, err)
		}
		if begins.Load() != 1 || commits.Load() != 1 || aborts.Load() != 0 {
			t.Fatalf("charity side effects begin=%d commit=%d abort=%d", begins.Load(), commits.Load(), aborts.Load())
		}
	})
}

func TestPruneLockedBoundsSeenAcrossRollingWindow(t *testing.T) {
	svc := &Service{maxEvents: 4}
	window := &userWindow{
		events: make([]windowEvent, 0, svc.maxEvents),
		seen:   make(map[string]struct{}, svc.maxEvents),
	}
	base := time.Unix(12_000, 0).UTC()
	duration := 3 * time.Second

	for step := 0; step < 20; step++ {
		now := base.Add(time.Duration(step) * time.Second)
		svc.pruneLocked(window, now, duration)
		if step == 2 {
			window.thresholdBanDone = true
		}
		if step >= 3 && !window.thresholdBanDone {
			t.Fatalf("rolling prune reset threshold at step %d while active events remained", step)
		}
		if step == 4 {
			if _, stale := window.seen["operation-0"]; stale {
				t.Fatal("expired operation id remained in seen")
			}
		}

		operationID := fmt.Sprintf("operation-%d", step)
		if step == 4 {
			// Once its event expires, the old id is outside the idempotency
			// window and may be accepted as a new bounded event.
			operationID = "operation-0"
		}
		accepted, fresh := svc.addEventLocked(window, now, operationID)
		if !accepted || !fresh {
			t.Fatalf("rolling event %q accepted=%v fresh=%v", operationID, accepted, fresh)
		}
		accepted, fresh = svc.addEventLocked(window, now, operationID)
		if !accepted || fresh {
			t.Fatalf("active duplicate %q accepted=%v fresh=%v", operationID, accepted, fresh)
		}
		if len(window.seen) != len(window.events) || len(window.events) > svc.maxEvents {
			t.Fatalf("step %d events=%d seen=%d max=%d", step, len(window.events), len(window.seen), svc.maxEvents)
		}
		for _, event := range window.events {
			if _, tracked := window.seen[event.at]; !tracked {
				t.Fatalf("active event %q missing from seen", event.at)
			}
		}
	}

	window.suspendDone = true
	svc.pruneLocked(window, base.Add(100*time.Second), duration)
	if len(window.events) != 0 || len(window.seen) != 0 {
		t.Fatalf("full expiry events=%d seen=%d", len(window.events), len(window.seen))
	}
	if window.thresholdBanDone || window.suspendDone {
		t.Fatalf("full expiry preserved terminal flags: threshold=%v suspend=%v", window.thresholdBanDone, window.suspendDone)
	}
	accepted, fresh := svc.addEventLocked(window, base.Add(100*time.Second), "operation-19")
	if !accepted || !fresh {
		t.Fatalf("expired id was not reusable: accepted=%v fresh=%v", accepted, fresh)
	}
	accepted, fresh = svc.addEventLocked(window, base.Add(100*time.Second), "operation-19")
	if !accepted || fresh {
		t.Fatalf("reused active id lost idempotency: accepted=%v fresh=%v", accepted, fresh)
	}

	window.thresholdBanDone = true
	window.suspendDone = true
	backing := window.events[:1]
	svc.pruneLocked(window, base.Add(101*time.Second), 0)
	if len(window.events) != 0 || len(window.seen) != 0 || window.thresholdBanDone || window.suspendDone {
		t.Fatalf("disabled window was not reset: events=%d seen=%d threshold=%v suspend=%v",
			len(window.events), len(window.seen), window.thresholdBanDone, window.suspendDone)
	}
	if backing[0] != (windowEvent{}) {
		t.Fatalf("disabled window retained backing event: %+v", backing[0])
	}
}

func TestUserDeletionAbortPreservesPinnedThresholdWindow(t *testing.T) {
	store := testStore(t)
	user := newUser(t, store, "delete-abort-window")
	setPolicy(t, store, "charity_enabled", "1")
	setPolicy(t, store, KeyCharityMinChars, "3")
	setPolicy(t, store, KeyCharityViolationBanThreshold, "2")
	setPolicy(t, store, KeyCharityViolationWindowBanSeconds, "30")
	setPolicy(t, store, KeyCharityViolationWindowSeconds, "100")
	setPolicy(t, store, KeyCharitySuspendWindowSeconds, "100")
	now := time.Unix(4_000, 0).UTC()
	var begins, commits, aborts atomic.Int64
	svc, err := NewService(ServiceConfig{
		Store: store, Now: func() time.Time { return now },
		BeginUserRetirement: retirementRecorder(t, store, &begins, &commits, &aborts),
	})
	if err != nil {
		t.Fatal(err)
	}
	duration := 100 * time.Second
	window, err := svc.getWindow(svc.charity, user.ID, duration)
	if err != nil {
		t.Fatal(err)
	}
	svc.unpinWindow(window)

	// Hold the per-user effect rail so a real Preflight can finish its map
	// lookup/pin but cannot record the event until after the simulated delete
	// failure aborts.
	window.effectsMu.Lock()
	var unlockOnce sync.Once
	unlockWindow := func() { unlockOnce.Do(window.effectsMu.Unlock) }
	defer unlockWindow()
	request := shortRequest(t)
	defer request.Clear()
	preflightDone := make(chan error, 1)
	go func() { preflightDone <- svc.Preflight(context.Background(), user.ID, request) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		svc.mu.Lock()
		pins := window.pins
		svc.mu.Unlock()
		if pins == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("preflight did not pin the existing window")
		}
		time.Sleep(time.Millisecond)
	}

	deleteCommit, deleteAbort, err := svc.BeginUserDeletion(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := svc.getWindow(svc.charity, user.ID, duration); other != nil || !errors.Is(err, ErrUserRetiring) {
		t.Fatalf("retiring lookup window=%p err=%v", other, err)
	}
	// A canceled/failed DB transaction calls Abort while retaining the exact
	// pointer. The pinned preflight then records into that original window.
	if !deleteAbort() || deleteAbort() || deleteCommit() {
		t.Fatal("delete abort terminal is not exactly once")
	}
	unlockWindow()
	select {
	case err := <-preflightDone:
		if !errors.Is(err, forward.ErrCharityContentTooShort) {
			t.Fatalf("pinned preflight err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pinned preflight did not finish after abort")
	}
	svc.mu.Lock()
	mapped := svc.charity[user.ID]
	_, retiring := svc.retiring[user.ID]
	svc.mu.Unlock()
	if mapped != window || retiring {
		t.Fatalf("abort state mapped=%p want=%p retiring=%v", mapped, window, retiring)
	}

	second := shortRequest(t)
	defer second.Clear()
	if err := svc.Preflight(context.Background(), user.ID, second); !errors.Is(err, forward.ErrCharityContentTooShort) {
		t.Fatalf("second post-abort preflight err=%v", err)
	}
	third := shortRequest(t)
	defer third.Clear()
	_ = svc.Preflight(context.Background(), user.ID, third)
	current, err := store.GetUserByID(user.ID)
	if err != nil || current == nil || !current.IsBanned {
		t.Fatalf("post-abort threshold user=%+v err=%v", current, err)
	}
	if begins.Load() != 1 || commits.Load() != 1 || aborts.Load() != 0 {
		t.Fatalf("post-abort side effects begin=%d commit=%d abort=%d", begins.Load(), commits.Load(), aborts.Load())
	}
}

func TestUserDeletionCommitDrainsPinsAndPreBeginLookups(t *testing.T) {
	t.Run("pinned windows", func(t *testing.T) {
		store := testStore(t)
		user := newUser(t, store, "delete-commit-pins")
		svc, err := NewService(ServiceConfig{Store: store})
		if err != nil {
			t.Fatal(err)
		}
		rpmWindow, err := svc.getWindow(svc.rpm, user.ID, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		charityWindow, err := svc.getWindow(svc.charity, user.ID, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		commit, abort, err := svc.BeginUserDeletion(user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteUserAccount(context.Background(), user.ID); err != nil {
			t.Fatal(err)
		}
		if !commit() || commit() || abort() {
			t.Fatal("delete commit terminal is not exactly once")
		}
		svc.mu.Lock()
		marker := svc.retiring[user.ID]
		svc.mu.Unlock()
		if marker == nil || !marker.committed {
			t.Fatal("committed marker disappeared before pinned windows drained")
		}
		if window, err := svc.getWindow(svc.rpm, user.ID, time.Minute); window != nil || !errors.Is(err, ErrUserRetiring) {
			t.Fatalf("committed marker lookup window=%p err=%v", window, err)
		}
		svc.unpinWindow(rpmWindow)
		svc.mu.Lock()
		marker = svc.retiring[user.ID]
		svc.mu.Unlock()
		if marker == nil {
			t.Fatal("one remaining pin did not retain committed marker")
		}
		svc.unpinWindow(charityWindow)
		svc.mu.Lock()
		_, rpmExists := svc.rpm[user.ID]
		_, charityExists := svc.charity[user.ID]
		_, retiring := svc.retiring[user.ID]
		svc.mu.Unlock()
		if rpmExists || charityExists || retiring {
			t.Fatalf("pinned drain residue rpm=%v charity=%v retiring=%v", rpmExists, charityExists, retiring)
		}
		// Once the committed marker is collected, the mandatory fresh DB read
		// still prevents a delayed stale caller from rebuilding either map.
		if window, err := svc.getWindow(svc.rpm, user.ID, time.Minute); window != nil || !errors.Is(err, ErrInvalidUser) {
			t.Fatalf("post-delete late lookup window=%p err=%v", window, err)
		}
	})

	t.Run("lookup started before begin", func(t *testing.T) {
		store := testStore(t)
		user := newUser(t, store, "delete-commit-lookup")
		svc, err := NewService(ServiceConfig{Store: store})
		if err != nil {
			t.Fatal(err)
		}
		lookupStarted := make(chan struct{})
		allowLookup := make(chan struct{})
		var allowLookupOnce sync.Once
		releaseLookup := func() { allowLookupOnce.Do(func() { close(allowLookup) }) }
		defer releaseLookup()
		var lookupOnce sync.Once
		svc.lookupUser = func(userID int64) (*db.User, error) {
			lookupOnce.Do(func() {
				close(lookupStarted)
				<-allowLookup
			})
			return store.GetUserByID(userID)
		}
		type result struct {
			window *userWindow
			err    error
		}
		lookupDone := make(chan result, 1)
		go func() {
			window, err := svc.getWindow(svc.rpm, user.ID, time.Minute)
			lookupDone <- result{window: window, err: err}
		}()
		select {
		case <-lookupStarted:
		case <-time.After(2 * time.Second):
			t.Fatal("pre-begin lookup did not start")
		}
		commit, _, err := svc.BeginUserDeletion(user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteUserAccount(context.Background(), user.ID); err != nil {
			t.Fatal(err)
		}
		if !commit() {
			t.Fatal("commit")
		}
		svc.mu.Lock()
		marker := svc.retiring[user.ID]
		lookupPins := svc.lookups[user.ID]
		svc.mu.Unlock()
		if marker == nil || !marker.committed || lookupPins != 1 {
			t.Fatalf("pre-begin lookup state marker=%+v pins=%d", marker, lookupPins)
		}
		releaseLookup()
		select {
		case got := <-lookupDone:
			if got.window != nil || !errors.Is(got.err, ErrUserRetiring) {
				t.Fatalf("pre-begin completion window=%p err=%v", got.window, got.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("pre-begin lookup did not complete")
		}
		svc.mu.Lock()
		_, retiring := svc.retiring[user.ID]
		_, rpmExists := svc.rpm[user.ID]
		lookupPins = svc.lookups[user.ID]
		svc.mu.Unlock()
		if retiring || rpmExists || lookupPins != 0 {
			t.Fatalf("lookup drain residue retiring=%v rpm=%v pins=%d", retiring, rpmExists, lookupPins)
		}
		if window, err := svc.getWindow(svc.rpm, user.ID, time.Minute); window != nil || !errors.Is(err, ErrInvalidUser) {
			t.Fatalf("late lookup rebuilt window=%p err=%v", window, err)
		}
	})
}

func setPolicy(t *testing.T, store *db.Store, key, value string) {
	t.Helper()
	if err := store.SetSiteConfigValue(key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}

func shortRequest(t *testing.T) *openai.ChatRequest {
	t.Helper()
	request, err := openai.DecodeChatRequest(bytes.NewBufferString(`{"model":"[公益]p/m","messages":[{"role":"user","content":"你好"}]}`), openai.MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func newUser(t *testing.T, store *db.Store, id string) *db.User {
	t.Helper()
	user, err := store.CreateUser(id, id, "")
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func TestCharityShortContentUnicodePenaltyAndDisable(t *testing.T) {
	store := testStore(t)
	user := newUser(t, store, "short-user")
	setPolicy(t, store, "charity_enabled", "1")
	setPolicy(t, store, KeyCharityMinChars, "3")
	setPolicy(t, store, KeyCharityViolationDeductMilli, "7")
	now := time.Unix(1000, 0).UTC()
	svc, err := NewService(ServiceConfig{Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := shortRequest(t)
	defer request.Clear()
	err = svc.Preflight(context.Background(), user.ID, request)
	var tooShort *forward.ContentTooShortError
	if !errors.As(err, &tooShort) || tooShort.Actual != 2 || tooShort.Minimum != 3 {
		t.Fatalf("short error = %v, want rune counts 2/3", err)
	}
	got, err := store.GetUserByID(user.ID)
	if err != nil || got.Credits != -7 {
		t.Fatalf("credits = %d, err=%v, want -7", got.Credits, err)
	}
	setPolicy(t, store, KeyCharityMinChars, "0")
	if err := svc.Preflight(context.Background(), user.ID, request); err != nil {
		t.Fatalf("min=0 preflight = %v, want disabled", err)
	}
	got, err = store.GetUserByID(user.ID)
	if err != nil || got.Credits != -7 {
		t.Fatalf("disabled preflight changed credits: %d, %v", got.Credits, err)
	}
}

func TestRPMWindowThresholdExpiryRestartAndAdminExclusion(t *testing.T) {
	store := testStore(t)
	user := newUser(t, store, "rpm-user")
	admin, err := store.EnsureAdminUser("admin")
	if err != nil {
		t.Fatal(err)
	}
	setPolicy(t, store, KeyRPMBanThreshold, "2")
	setPolicy(t, store, KeyRPMBanWindowSeconds, "10")
	setPolicy(t, store, KeyRPMBanDurationSeconds, "20")
	now := time.Unix(1000, 0).UTC()
	svc, err := NewService(ServiceConfig{Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	svc.RPMDenied(context.Background(), admin.ID, true, ratelimit.RPMUserLimit)
	if got, _ := store.GetUserByID(admin.ID); got.IsBanned {
		t.Fatal("administrator was auto-banned")
	}
	svc.RPMDenied(context.Background(), user.ID, false, ratelimit.RPMGlobalLimit)
	svc.RPMDenied(context.Background(), user.ID, false, ratelimit.RPMUserLimit)
	if got, _ := store.GetUserByID(user.ID); got.IsBanned {
		t.Fatal("one user denial crossed threshold")
	}
	now = now.Add(time.Second)
	svc.RPMDenied(context.Background(), user.ID, false, ratelimit.RPMUserLimit)
	got, err := store.GetUserByID(user.ID)
	if err != nil || !got.IsBanned || !got.AutoBanned || got.BannedUntil == nil {
		t.Fatalf("ban projection = %#v, err=%v", got, err)
	}
	// A fresh process has no in-memory history; it must not invent a second
	// threshold hit from the previous process's events.
	if _, err := store.DB().Exec(`UPDATE users SET is_banned=0, auto_banned=0, banned_until=NULL WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := NewService(ServiceConfig{Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	fresh.RPMDenied(context.Background(), user.ID, false, ratelimit.RPMUserLimit)
	got, err = store.GetUserByID(user.ID)
	if err != nil || got.IsBanned {
		t.Fatalf("restart did not discard window: %#v, %v", got, err)
	}
}

func TestCharityWindowBanSuspensionAndCapacity(t *testing.T) {
	store := testStore(t)
	user := newUser(t, store, "window-user")
	setPolicy(t, store, "charity_enabled", "1")
	setPolicy(t, store, KeyCharityMinChars, "3")
	setPolicy(t, store, KeyCharityViolationWindowSeconds, "100")
	setPolicy(t, store, KeyCharitySuspendWindowSeconds, "100")
	setPolicy(t, store, KeyCharityViolationBanThreshold, "2")
	setPolicy(t, store, KeyCharityViolationWindowBanSeconds, "30")
	setPolicy(t, store, KeyCharitySuspendThreshold, "2")
	setPolicy(t, store, KeyCharitySuspendDurationSeconds, "40")
	now := time.Now().UTC()
	svc, err := NewService(ServiceConfig{Store: store, Now: func() time.Time { return now }, MaxUsers: 1, MaxEventsPerUser: 4})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		err := svc.Preflight(context.Background(), user.ID, shortRequest(t))
		if err == nil {
			t.Fatal("short request unexpectedly passed")
		}
	}
	got, err := store.GetUserByID(user.ID)
	if err != nil || !got.IsBanned || !got.AutoBanned || got.CharitySuspendedUntil == nil {
		t.Fatalf("window actions = %#v, err=%v", got, err)
	}
	// A second user cannot allocate another window once the configured total
	// user bound is full; the policy fails closed without growing the map.
	other := newUser(t, store, "window-other")
	if err := svc.Preflight(context.Background(), other.ID, shortRequest(t)); !errors.Is(err, forward.ErrAntiAbuseUnavailable) {
		t.Fatalf("capacity error = %v, want anti-abuse unavailable", err)
	}
	now = now.Add(101 * time.Second)
	svc.Cleanup()
	if err := svc.Preflight(context.Background(), other.ID, shortRequest(t)); !errors.Is(err, forward.ErrCharityContentTooShort) {
		t.Fatalf("post-cleanup preflight = %v, want a counted short violation", err)
	}
}
