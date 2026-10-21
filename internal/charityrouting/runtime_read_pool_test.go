package charityrouting

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRuntimePreflightReadsUseReadPoolAndCurrentAccess(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	e.seedUserBalance(t, e.caller, "100000")
	model := e.createModel(t, 'R')
	reads := []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := e.service.RequestPolicy(ctx, e.caller, model.FullName, routingTestNow)
			return err
		},
		func(ctx context.Context) error {
			_, err := e.service.preflight(ctx, e.caller, model.FullName, nil, routingTestNow)
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
	if e.state.dueCalls.Load() != 0 {
		t.Fatal("preflight materialized expiry")
	}
	if _, err = e.store.DB().Exec("UPDATE users SET is_banned=1 WHERE id=?", e.caller); err != nil {
		t.Fatal(err)
	}
	for _, read := range reads {
		if err := read(t.Context()); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("revoked user admitted", err)
		}
	}
	if _, err = e.store.DB().Exec("UPDATE users SET is_banned=0 WHERE id=?", e.caller); err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.DB().Exec("UPDATE charity_model_access SET allowed_level_mask=0 WHERE model_id=?", model.ID); err != nil {
		t.Fatal(err)
	}
	for _, read := range reads {
		if err := read(t.Context()); !errors.Is(err, ErrForbidden) {
			t.Fatal("revoked access admitted", err)
		}
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
