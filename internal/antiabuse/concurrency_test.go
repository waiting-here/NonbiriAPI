package antiabuse

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/forward"
)

func TestConcurrentCharityThresholdActionRunsOnce(t *testing.T) {
	store := testStore(t)
	user := newUser(t, store, "concurrent-user")
	setPolicy(t, store, "charity_enabled", "1")
	setPolicy(t, store, KeyCharityMinChars, "3")
	setPolicy(t, store, KeyCharityViolationWindowSeconds, "100")
	setPolicy(t, store, KeyCharitySuspendWindowSeconds, "100")
	setPolicy(t, store, KeyCharityViolationBanThreshold, "2")
	setPolicy(t, store, KeyCharityViolationWindowBanSeconds, "60")
	svc, err := NewService(ServiceConfig{Store: store, Now: time.Now, MaxEventsPerUser: 64})
	if err != nil {
		t.Fatal(err)
	}
	const calls = 16
	var wg sync.WaitGroup
	results := make(chan error, calls)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := svc.Preflight(context.Background(), user.ID, shortRequest(t))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if !errors.Is(err, forward.ErrCharityContentTooShort) {
			t.Fatalf("concurrent preflight = %v", err)
		}
	}
	got, err := store.GetUserByID(user.ID)
	if err != nil || !got.IsBanned || got.BannedUntil == nil {
		t.Fatalf("concurrent ban projection = %#v, err=%v", got, err)
	}
	first := got.BannedUntil.Unix()
	// Replaying while the threshold remains reached must not extend the ban.
	if err := svc.Preflight(context.Background(), user.ID, shortRequest(t)); !errors.Is(err, forward.ErrCharityContentTooShort) {
		t.Fatalf("replay preflight = %v", err)
	}
	got, err = store.GetUserByID(user.ID)
	if err != nil || got.BannedUntil == nil || got.BannedUntil.Unix() != first {
		t.Fatalf("threshold replay extended ban: before=%d after=%#v err=%v", first, got, err)
	}
}
