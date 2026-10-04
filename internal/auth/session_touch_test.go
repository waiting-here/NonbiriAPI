package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestSessionTouchPreservesExactIdleExpiryWithoutDuplicateWrites(t *testing.T) {
	f := newRuntimeFixture(t, func(c *RuntimeConfig) { c.SessionIdleTTL = 2 * time.Minute })
	cookie := loginUser(t, f, "same-second", "")
	ctx := context.Background()
	changes := func() int64 {
		t.Helper()
		var n int64
		if err := f.store.DB().QueryRow(`SELECT total_changes()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := changes()
	for range 3 {
		if _, err := f.runtime.authenticate(ctx, cookie.Value, authz.ActorUserSession, ""); err != nil {
			t.Fatal(err)
		}
	}
	if changes() != before {
		t.Fatal("unchanged session was written")
	}
	f.clock.Add(time.Second)
	p, err := f.runtime.authenticate(ctx, cookie.Value, authz.ActorUserSession, "")
	if err != nil || p.expiresAt != f.clock.Now().Unix()+120 {
		t.Fatalf("expiry=%d err=%v", p.expiresAt, err)
	}
	if changes() != before+1 {
		t.Fatal("new second did not persist one touch")
	}
	if _, err := f.runtime.deleteSession(ctx, cookie.Value); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.authenticate(ctx, cookie.Value, authz.ActorUserSession, ""); !errors.Is(err, errSessionUnauthorized) {
		t.Fatalf("revoked session: %v", err)
	}
}
