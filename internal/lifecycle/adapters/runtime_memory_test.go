package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type recordingAccountStreamForgetter struct{ calls *[]string }

func (forgetter recordingAccountStreamForgetter) ForgetAccounts(context.Context, []int64) error {
	*forgetter.calls = append(*forgetter.calls, "accountstream")
	return nil
}

type recordingDebugForgetter struct{ calls *[]string }

func (forgetter recordingDebugForgetter) ForgetAccount(int64) error {
	*forgetter.calls = append(*forgetter.calls, "debug")
	return nil
}

func TestRuntimeMemoryFinalizerDefersCleanupAndUsesFrozenOrder(t *testing.T) {
	calls := []string{}
	adapter, err := newRuntimeMemoryDeleteAdapter(
		recordingAccountStreamForgetter{calls: &calls}, recordingDebugForgetter{calls: &calls},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := lifecycle.DeleteRequest{UserID: 42, DecisionNow: 1_800_300_000}
	aborted, err := adapter.PrepareDelete(context.Background(), new(sql.Tx), request)
	if err != nil || len(calls) != 0 {
		t.Fatalf("prepare = finalizer:%v calls:%v err:%v", aborted != nil, calls, err)
	}
	if !aborted.Abort() || aborted.Abort() || aborted.Commit() || len(calls) != 0 {
		t.Fatalf("abort changed memory: calls=%v", calls)
	}

	committed, err := adapter.PrepareDelete(context.Background(), new(sql.Tx), request)
	if err != nil || len(calls) != 0 {
		t.Fatalf("second prepare = finalizer:%v calls:%v err:%v", committed != nil, calls, err)
	}
	if !committed.Commit() || committed.Commit() || committed.Abort() {
		t.Fatal("commit finalizer was not one-shot")
	}
	if want := []string{"accountstream", "debug"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("cleanup order = %v, want %v", calls, want)
	}
}

func TestRuntimeMemoryPrepareRejectsInvalidInputs(t *testing.T) {
	calls := []string{}
	adapter, err := newRuntimeMemoryDeleteAdapter(
		recordingAccountStreamForgetter{calls: &calls}, recordingDebugForgetter{calls: &calls},
	)
	if err != nil {
		t.Fatal(err)
	}
	valid := lifecycle.DeleteRequest{UserID: 1, DecisionNow: 0}
	for name, test := range map[string]struct {
		ctx     context.Context
		tx      *sql.Tx
		request lifecycle.DeleteRequest
	}{
		"nil context": {ctx: nil, tx: new(sql.Tx), request: valid},
		"nil tx":      {ctx: context.Background(), tx: nil, request: valid},
		"zero user":   {ctx: context.Background(), tx: new(sql.Tx), request: lifecycle.DeleteRequest{}},
		"future time": {ctx: context.Background(), tx: new(sql.Tx), request: lifecycle.DeleteRequest{UserID: 1, DecisionNow: maxUnixSecond + 1}},
	} {
		t.Run(name, func(t *testing.T) {
			if finalizer, err := adapter.PrepareDelete(test.ctx, test.tx, test.request); finalizer != nil || !errors.Is(err, lifecycle.ErrInvalid) {
				t.Fatalf("PrepareDelete = (%v,%v)", finalizer, err)
			}
		})
	}
	if len(calls) != 0 {
		t.Fatalf("invalid prepare changed memory: %v", calls)
	}
	for name, test := range map[string]struct {
		accountEvents *accountstream.Hub
		debugHub      *debug.Hub
	}{
		"nil account stream": {accountEvents: nil, debugHub: new(debug.Hub)},
		"nil Debug":          {accountEvents: new(accountstream.Hub), debugHub: nil},
		"both nil":           {accountEvents: nil, debugHub: nil},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewRuntimeMemoryDeleteAdapter(test.accountEvents, test.debugHub); !errors.Is(err, lifecycle.ErrInvalid) {
				t.Fatalf("constructor error = %v", err)
			}
		})
	}
}

type runtimeMemoryDebugVerifier struct{}

func (runtimeMemoryDebugVerifier) VerifyDebugIdentity(context.Context, int64, string) (debug.IdentityState, error) {
	return debug.IdentityActive, nil
}

type runtimeMemorySnapshot struct{}

func (runtimeMemorySnapshot) Snapshot(context.Context, int64, accountstream.Channel) (accountstream.Snapshot, error) {
	revision := "1"
	return accountstream.Snapshot{Revision: &revision, Data: json.RawMessage(`{}`)}, nil
}

func TestRuntimeMemoryCommitForgetsLiveHubsWhileAbortPreservesThem(t *testing.T) {
	debugHub, err := debug.NewHub(runtimeMemoryDebugVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = debugHub.Close() })
	accountEvents, err := accountstream.New(runtimeMemorySnapshot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accountEvents.Close() })
	adapter, err := NewRuntimeMemoryDeleteAdapter(accountEvents, debugHub)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := debugHub.Start(7, "binding-7"); err != nil {
		t.Fatal(err)
	}
	subscription, err := accountEvents.Subscribe(context.Background(), accountstream.SubscribeRequest{
		AccountID: 7, Channels: []accountstream.Channel{accountstream.ChannelActivities},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accountEvents.PublishCommitted(context.Background(), 7, accountstream.PublishedEvent{
		Channel: accountstream.ChannelActivities, Type: accountstream.TypeDelta,
		Revision: stringAddress("2"), Data: json.RawMessage(`{"state":"before"}`),
	}); err != nil {
		t.Fatal(err)
	}
	request := lifecycle.DeleteRequest{UserID: 7, DecisionNow: 1_800_300_000}

	aborted, err := adapter.PrepareDelete(context.Background(), new(sql.Tx), request)
	if err != nil || !aborted.Abort() {
		t.Fatalf("abort prepare = (%v,%v)", aborted, err)
	}
	if metadata, err := debugHub.Metadata(7); err != nil || !metadata.Active {
		t.Fatalf("abort changed Debug = (%+v,%v)", metadata, err)
	}
	if _, err := accountEvents.PublishCommitted(context.Background(), 7, accountstream.PublishedEvent{
		Channel: accountstream.ChannelActivities, Type: accountstream.TypeDelta,
		Revision: stringAddress("3"), Data: json.RawMessage(`{"state":"after_abort"}`),
	}); err != nil {
		t.Fatalf("abort changed account stream: %v", err)
	}

	committed, err := adapter.PrepareDelete(context.Background(), new(sql.Tx), request)
	if err != nil || !committed.Commit() {
		t.Fatalf("commit prepare = (%v,%v)", committed, err)
	}
	if metadata, err := debugHub.Metadata(7); err != nil || metadata.Active {
		t.Fatalf("commit retained Debug = (%+v,%v)", metadata, err)
	}
	if _, _, err := debugHub.Start(7, "binding-7"); !errors.Is(err, debug.ErrNoActiveSession) {
		t.Fatalf("deleted account restarted Debug: %v", err)
	}
	if _, err := subscription.Next(context.Background()); !errors.Is(err, accountstream.ErrClosed) {
		t.Fatalf("deleted subscription remained readable: %v", err)
	}
	if _, err := accountEvents.PublishCommitted(context.Background(), 7, accountstream.PublishedEvent{
		Channel: accountstream.ChannelActivities, Type: accountstream.TypeDelta,
		Revision: stringAddress("4"), Data: json.RawMessage(`{"state":"late"}`),
	}); !errors.Is(err, accountstream.ErrClosed) {
		t.Fatalf("late account stream publish = %v", err)
	}
}

func stringAddress(value string) *string { return &value }
