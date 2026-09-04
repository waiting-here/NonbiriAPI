package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type accountSnapshotStub struct {
	lastChannel accountstream.Channel
	epoch       *string
}

func (stub *accountSnapshotStub) Snapshot(_ context.Context, _ int64, channel accountstream.Channel) (accountstream.Snapshot, error) {
	stub.lastChannel = channel
	return accountstream.Snapshot{Data: json.RawMessage(`{"ok":true}`)}, nil
}

func (stub *accountSnapshotStub) CurrentIdentityEpoch(context.Context, int64) (*string, error) {
	return stub.epoch, nil
}

func TestAccountEventSourcesRequireStartupBindingAndDispatchClosedChannels(t *testing.T) {
	activities := &accountSnapshotStub{}
	sources, err := newAccountEventSources(activities)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sources.Snapshot(context.Background(), 1, accountstream.ChannelActivities); err != nil || activities.lastChannel != accountstream.ChannelActivities {
		t.Fatalf("activities snapshot channel=%q err=%v", activities.lastChannel, err)
	}
	if _, err := sources.Snapshot(context.Background(), 1, accountstream.ChannelRPS); !errors.Is(err, accountstream.ErrSnapshot) {
		t.Fatalf("unbound RPS snapshot err=%v", err)
	}
	if _, err := sources.CurrentIdentityEpoch(context.Background(), 1); !errors.Is(err, accountstream.ErrSnapshot) {
		t.Fatalf("unbound RPS epoch err=%v", err)
	}
	epoch := "1"
	rps := &accountSnapshotStub{epoch: &epoch}
	if err := sources.BindRPS(rps); err != nil {
		t.Fatal(err)
	}
	if err := sources.BindRPS(rps); err == nil {
		t.Fatal("duplicate RPS binding succeeded")
	}
	if _, err := sources.Snapshot(context.Background(), 1, accountstream.ChannelRPS); err != nil || rps.lastChannel != accountstream.ChannelRPS {
		t.Fatalf("RPS snapshot channel=%q err=%v", rps.lastChannel, err)
	}
	gotEpoch, err := sources.CurrentIdentityEpoch(context.Background(), 1)
	if err != nil || gotEpoch == nil || *gotEpoch != epoch {
		t.Fatalf("RPS epoch=%v err=%v", gotEpoch, err)
	}
	if _, err := sources.Snapshot(context.Background(), 1, accountstream.Channel("unknown")); !errors.Is(err, accountstream.ErrSnapshot) {
		t.Fatalf("unknown channel err=%v", err)
	}
}

func TestAccountEventConnectionsCancelExactUserAndClose(t *testing.T) {
	connections := newAccountEventConnections()
	first, unregisterFirst, err := connections.Register(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	second, unregisterSecond, err := connections.Register(context.Background(), 8)
	if err != nil {
		t.Fatal(err)
	}
	defer unregisterFirst()
	defer unregisterSecond()

	connections.CancelUser(7)
	awaitCancellation(t, first)
	select {
	case <-second.Done():
		t.Fatal("cancelling user 7 cancelled user 8")
	default:
	}
	if err := connections.Close(); err != nil {
		t.Fatal(err)
	}
	awaitCancellation(t, second)
	if err := connections.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	if _, _, err := connections.Register(context.Background(), 9); !errors.Is(err, accountstream.ErrClosed) {
		t.Fatalf("register after close err=%v", err)
	}
}

type accountEventGateStub struct {
	state maintenance.State
	ready bool
}

func (stub accountEventGateStub) State() (maintenance.State, bool) { return stub.state, stub.ready }

type accountEventVerifierStub struct {
	state auth.UserSessionBindingState
	err   error
}

func (stub accountEventVerifierStub) VerifyUserSessionBinding(context.Context, int64, string) (auth.UserSessionBindingState, error) {
	return stub.state, stub.err
}

type accountEventRPSStub struct {
	err   error
	calls int
}

func (stub *accountEventRPSStub) Read(context.Context, rps.ReadInput) (rps.HomeState, error) {
	stub.calls++
	return rps.HomeState{}, stub.err
}

func TestAccountEventRouteAuthorizesMaintenanceBeforeRegisteringConnection(t *testing.T) {
	connections := newAccountEventConnections()
	rpsReader := &accountEventRPSStub{err: rps.ErrMaintenance}
	handler := &accountEventsHTTP{
		verifier: accountEventVerifierStub{state: auth.UserSessionBindingActive},
		maintenance: accountEventGateStub{
			state: maintenance.State{Enabled: true, Revision: 1}, ready: true,
		},
		rps: rpsReader, hub: &accountstream.Hub{}, connections: connections,
	}
	request := httptest.NewRequest(http.MethodGet, "https://user.example/api/events", nil)
	request.Header.Set("Accept", "text/event-stream")
	response := httptest.NewRecorder()
	handler.serve(response, request, resources.ContinuationUserPrincipal{UserID: 7, SessionBinding: "binding"})
	if response.Code != http.StatusServiceUnavailable || rpsReader.calls != 1 {
		t.Fatalf("maintenance response=%d body=%s RPS calls=%d", response.Code, response.Body.String(), rpsReader.calls)
	}
	connections.mu.Lock()
	registered := len(connections.users)
	connections.mu.Unlock()
	if registered != 0 {
		t.Fatalf("unauthorized maintenance continuation retained %d connection registrations", registered)
	}
}

func TestAccountEventRouteUnregistersFailedFinalIdentityCheck(t *testing.T) {
	connections := newAccountEventConnections()
	handler := &accountEventsHTTP{
		verifier:    accountEventVerifierStub{state: auth.UserSessionBindingRevoked},
		maintenance: accountEventGateStub{state: maintenance.State{Revision: 1}, ready: true},
		rps:         &accountEventRPSStub{}, hub: &accountstream.Hub{}, connections: connections,
	}
	request := httptest.NewRequest(http.MethodGet, "https://user.example/api/events", nil)
	request.Header.Set("Accept", "text/event-stream")
	response := httptest.NewRecorder()
	handler.serve(response, request, resources.ContinuationUserPrincipal{UserID: 7, SessionBinding: "binding"})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked response=%d body=%s", response.Code, response.Body.String())
	}
	connections.mu.Lock()
	registered := len(connections.users)
	connections.mu.Unlock()
	if registered != 0 {
		t.Fatalf("failed identity check retained %d connection registrations", registered)
	}
}

func awaitCancellation(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not cancelled")
	}
}

func TestAccountEventHeaderValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		values []string
		want   bool
	}{
		{name: "missing", want: false},
		{name: "exact", values: []string{"text/event-stream"}, want: true},
		{name: "parameter", values: []string{"application/json, text/event-stream; charset=utf-8"}, want: true},
		{name: "wrong", values: []string{"application/json"}, want: false},
		{name: "oversized", values: []string{string(make([]byte, maxAccountEventAccept+1))}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := acceptsAccountEventStream(test.values); got != test.want {
				t.Fatalf("acceptsAccountEventStream=%v want=%v", got, test.want)
			}
		})
	}

	if cursor, ok := accountEventCursor(nil); !ok || cursor != "" {
		t.Fatalf("empty cursor=%q ok=%v", cursor, ok)
	}
	if cursor, ok := accountEventCursor([]string{"sse_value"}); !ok || cursor != "sse_value" {
		t.Fatalf("cursor=%q ok=%v", cursor, ok)
	}
	for _, values := range [][]string{{"a", "b"}, {"bad\nvalue"}, {string(make([]byte, maxAccountEventCursor+1))}} {
		if _, ok := accountEventCursor(values); ok {
			t.Fatalf("invalid cursor accepted: %#v", values)
		}
	}
}
