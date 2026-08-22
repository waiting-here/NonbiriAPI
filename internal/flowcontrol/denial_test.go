package flowcontrol

import (
	"context"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

func TestAdmitDenialObserverClassifiesOnlyUserLimit(t *testing.T) {
	var mu sync.Mutex
	var reasons []ratelimit.RPMReason
	var users []int64
	config := testRPMConfig()
	controller, err := newWithClock(Config{
		RPM: config,
		OnDenied: func(_ context.Context, userID int64, reason ratelimit.RPMReason) {
			mu.Lock()
			users = append(users, userID)
			reasons = append(reasons, reason)
			mu.Unlock()
		},
	}, newFakeClock())
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	admit(t, controller, 1)
	admit(t, controller, 1)
	deny(t, controller, 1)
	admit(t, controller, 2)
	deny(t, controller, 3)
	mu.Lock()
	defer mu.Unlock()
	if len(reasons) != 2 || users[0] != 1 || users[1] != 3 || reasons[0] != ratelimit.RPMUserLimit || reasons[1] != ratelimit.RPMGlobalLimit {
		t.Fatalf("observed denial users=%v reasons=%v", users, reasons)
	}
}
