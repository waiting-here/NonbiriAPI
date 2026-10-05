package ai

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type sourceFunc func(context.Context, Request) (Result, error)

func (sourceFunc) ID() string                                              { return "test" }
func (sourceFunc) Supports(c Capability) bool                              { return c.ProtocolVersion == ProtocolVersion }
func (f sourceFunc) Decide(ctx context.Context, r Request) (Result, error) { return f(ctx, r) }
func request(id string) Request {
	return Request{Capability: Capability{ProtocolVersion: ProtocolVersion, Game: "complex", ObservationSchema: "board/v7", ActionSchema: ChoiceSchema}, DecisionID: id, Window: Window{Match: "match", Actor: "actor", Token: id, Phase: "choose"}, Deadline: time.Now().Add(5 * time.Second)}
}
func wait[T any](t *testing.T, c <-chan T) T {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("bounded work did not complete")
		var zero T
		return zero
	}
}
func closePool(t *testing.T, p *Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestChoiceProtocolHasNoBiddingOrActorCountAssumptions(t *testing.T) {
	r := request("window")
	r.Observation = struct{ Rows int }{Rows: 3}
	choices := Choices{}
	for i := range 40 {
		choices = append(choices, Choice{ID: fmt.Sprint(i), Action: struct{ Target int }{Target: i}})
	}
	r.Actions = choices
	action, err := ResolveChoice(r, Selected(r, "39"))
	if err != nil || action != choices[39].Action {
		t.Fatal(action, err)
	}
	for _, bad := range []Result{
		Selected(r, "missing"),
		{ProtocolVersion: ProtocolVersion, DecisionID: "old", ActionSchema: ChoiceSchema, Action: Selection{"39"}},
		{ProtocolVersion: 99, DecisionID: r.DecisionID, ActionSchema: ChoiceSchema, Action: Selection{"39"}},
	} {
		if _, err := ResolveChoice(r, bad); !errors.Is(err, ErrInvalidResult) {
			t.Fatal("invalid selection accepted", err)
		}
	}
}

func TestCompletionReleasesComputeBeforeNextWindowAndKeepsDedup(t *testing.T) {
	p, _ := NewPool(1, 3)
	defer closePool(t, p)
	results := make(chan error, 1)
	source := sourceFunc(func(_ context.Context, r Request) (Result, error) { return Selected(r, "ok"), nil })
	parent := Job{Request: request("first"), Source: source, Budget: time.Second}
	parent.Complete = func(_ Result, err error) {
		if err != nil {
			results <- err
			return
		}
		duplicate := parent
		duplicate.Complete = func(Result, error) {}
		accepted, err := p.Submit(context.Background(), duplicate)
		if accepted || err != nil {
			results <- fmt.Errorf("dedup ended before commit")
			return
		}
		next := request("second")
		next.Window.Actor = "third-actor"
		// A child choice retains the parent phase deadline, not a new game timer.
		next.Deadline = parent.Request.Deadline
		child := make(chan error, 1)
		_, err = p.Submit(context.Background(), Job{Request: next, Source: source, Budget: time.Second, Complete: func(_ Result, err error) { child <- err }})
		if err != nil {
			results <- err
			return
		}
		select {
		case err := <-child:
			results <- err
		case <-time.After(2 * time.Second):
			results <- errors.New("callback retained compute slot")
		}
	}
	if ok, err := p.Submit(context.Background(), parent); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if err := wait(t, results); err != nil {
		t.Fatal(err)
	}
}

func TestTimedOutUncooperativeSourceKeepsSlotAndFallbackOnce(t *testing.T) {
	p, _ := NewPool(1, 2)
	started, release := make(chan struct{}), make(chan struct{})
	returned := make(chan error, 2)
	var calls atomic.Int32
	slow := sourceFunc(func(_ context.Context, r Request) (Result, error) {
		close(started)
		<-release
		return Selected(r, "late"), nil
	})
	_, err := p.Submit(context.Background(), Job{Request: request("slow"), Source: slow, Budget: 30 * time.Millisecond, Complete: func(_ Result, err error) { calls.Add(1); returned <- err }})
	if err != nil {
		t.Fatal(err)
	}
	wait(t, started)
	if err := wait(t, returned); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	var ran atomic.Bool
	_, err = p.Submit(context.Background(), Job{Request: request("queued"), Source: sourceFunc(func(_ context.Context, r Request) (Result, error) { ran.Store(true); return Selected(r, "ok"), nil }), Budget: 30 * time.Millisecond, Complete: func(_ Result, err error) { returned <- err }})
	if err != nil {
		t.Fatal(err)
	}
	if err := wait(t, returned); !errors.Is(err, context.DeadlineExceeded) || ran.Load() {
		t.Fatal("replacement compute bypassed occupied slot", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := p.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unreturned source was considered stopped", err)
	}
	cancel()
	close(release)
	closePool(t, p)
	if calls.Load() != 1 {
		t.Fatal("late result applied twice", calls.Load())
	}
}

func TestSeparatePoolsAndTypedActionSchema(t *testing.T) {
	slow, _ := NewPool(1, 1)
	fast, _ := NewPool(1, 1)
	started, release := make(chan struct{}), make(chan struct{})
	defer func() { close(release); closePool(t, slow); closePool(t, fast) }()
	_, err := slow.Submit(context.Background(), Job{Request: request("remote"), Source: sourceFunc(func(ctx context.Context, r Request) (Result, error) {
		close(started)
		<-release
		return Selected(r, "ok"), nil
	}), Budget: time.Second, Complete: func(Result, error) {}})
	if err != nil {
		t.Fatal(err)
	}
	wait(t, started)
	done := make(chan error, 1)
	r := request("native")
	r.ActionSchema = "position-target/v2"
	_, err = fast.Submit(context.Background(), Job{Request: r, Source: sourceFunc(func(_ context.Context, r Request) (Result, error) {
		return Result{ProtocolVersion: ProtocolVersion, DecisionID: r.DecisionID, ActionSchema: r.ActionSchema, Action: struct{ Row, Target int }{2, 87}}, nil
	}), Budget: time.Second, Complete: func(_ Result, err error) { done <- err }})
	if err != nil {
		t.Fatal(err)
	}
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestCancelledQueuedWorkAndInvalidSourceResult(t *testing.T) {
	p, _ := NewPool(1, 2)
	defer closePool(t, p)
	done := make(chan error, 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Submit(ctx, Job{Request: request("cancelled"), Source: sourceFunc(func(context.Context, Request) (Result, error) { t.Error("cancelled source ran"); return Result{}, nil }), Budget: time.Second, Complete: func(_ Result, err error) { done <- err }})
	if err != nil {
		t.Fatal(err)
	}
	if err := wait(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_, err = p.Submit(context.Background(), Job{Request: request("invalid"), Source: sourceFunc(func(context.Context, Request) (Result, error) { return Result{}, nil }), Budget: time.Second, Complete: func(_ Result, err error) { done <- err }})
	if err != nil {
		t.Fatal(err)
	}
	if err := wait(t, done); !errors.Is(err, ErrInvalidResult) {
		t.Fatal(err)
	}
}
