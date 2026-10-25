package resources

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCallerKeyReadPoolAndFreshAuthorization(t *testing.T) {
	e := newResourceTestEnvironment(t)
	owner := e.seedUser(t, "caller-reader")
	key, err := e.repository.RegenerateCallerKey(t.Context(), owner, 0)
	if err != nil {
		t.Fatal(err)
	}
	held, err := e.store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	identity, err := e.repository.ResolveCallerKey(ctx, key.Secret)
	cancel()
	held.Close()
	if err != nil || identity.UserID != owner || identity.Generation != 1 {
		t.Fatalf("read was blocked by the writer: %+v %v", identity, err)
	}
	next, err := e.repository.RegenerateCallerKey(t.Context(), owner, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.repository.ResolveCallerKey(t.Context(), key.Secret); !errors.Is(err, ErrNotFound) {
		t.Fatal("old generation accepted", err)
	}
	if identity, err = e.repository.ResolveCallerKey(t.Context(), next.Secret); err != nil || identity.Generation != 2 {
		t.Fatal("committed generation not observed", identity, err)
	}
	if _, err = e.store.DB().Exec("UPDATE users SET is_banned=1,banned_until=? WHERE id=?", e.clock.Load()+60, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = e.repository.ResolveCallerKey(t.Context(), next.Secret); !errors.Is(err, ErrNotFound) {
		t.Fatal("committed ban not observed", err)
	}
	if _, err = e.store.DB().Exec("UPDATE users SET banned_until=? WHERE id=?", e.clock.Load(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err = e.repository.ResolveCallerKey(t.Context(), next.Secret); err != nil {
		t.Fatal("expired ban rejected", err)
	}
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if _, err = e.repository.ResolveCallerKey(cancelled, next.Secret); err == nil {
		t.Fatal("cancelled resolution succeeded")
	}
}
