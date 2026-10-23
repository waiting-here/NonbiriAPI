package routing

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLogicalReadsUseReadPoolAndCurrentState(t *testing.T) {
	e := newSnapshotTestEnvironment(t)
	owner := e.seedUser(t, "read-pool")
	model := e.seedModel(t, owner, "provider", "reader")
	key := e.seedAvailableBinding(t, owner, model, "upstream")
	reads := []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := e.routing.Preflight(ctx, owner, "provider/reader")
			return err
		},
		func(ctx context.Context) error {
			_, err := e.routing.Snapshot(ctx, owner, Identity{FullName: "provider/reader"})
			return err
		},
	}
	writer, err := e.store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	for _, read := range reads {
		if err := read(ctx); err != nil {
			writer.Close()
			cancel()
			t.Fatal("read waited for writer", err)
		}
	}
	cancel()
	writer.Close()
	if _, err = e.routing.Preflight(t.Context(), owner+1, "provider/reader"); !errors.Is(err, ErrNotFound) {
		t.Fatal("owner isolation", err)
	}
	if _, err = e.store.DB().Exec("UPDATE models SET route_strategy='random',revision=revision+1 WHERE id=?", model); err != nil {
		t.Fatal(err)
	}
	current, err := e.routing.Preflight(t.Context(), owner, "provider/reader")
	if err != nil || current.RouteStrategy() != "random" {
		t.Fatal("committed policy invisible", err)
	}
	if _, err = e.store.DB().Exec("UPDATE endpoint_keys SET enabled=0 WHERE id=?", key); err != nil {
		t.Fatal(err)
	}
	if err = reads[1](t.Context()); !errors.Is(err, ErrUnbound) {
		t.Fatal("revoked candidate remained", err)
	}
	first, err := e.store.ReadDB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := e.store.ReadDB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	canceled, stop := context.WithCancel(t.Context())
	stop()
	for _, read := range reads {
		if err := read(canceled); !errors.Is(err, context.Canceled) {
			t.Fatal("reader cancellation", err)
		}
	}
}
