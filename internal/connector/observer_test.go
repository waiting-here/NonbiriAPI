package connector

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSafeObserverSlowFullPanicAndClosedAreContained(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	observer, err := NewSafeObserver(1, func(Observation) {
		call := calls.Add(1)
		if call == 1 {
			close(entered)
			<-release
		}
		if call == 2 {
			panic("observer test panic")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !observer.TryObserve(Observation{Kind: ObservationAttemptStarted}) {
		t.Fatal("first event was refused")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	if !observer.TryObserve(Observation{Kind: ObservationAttemptFinished}) {
		t.Fatal("bounded queued event was refused")
	}
	started := time.Now()
	if observer.TryObserve(Observation{Kind: ObservationAttemptFinished}) {
		t.Fatal("full observer queue accepted an event")
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("full queue blocked request path for %v", elapsed)
	}
	close(release)
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("accepted events consumed = %d, want 2", calls.Load())
	}
	if observer.TryObserve(Observation{}) {
		t.Fatal("closed observer accepted an event")
	}
	if observer.Dropped() != 2 {
		t.Fatalf("dropped = %d, want 2", observer.Dropped())
	}
}

func TestSafeObserverCloseLinearizesConcurrentTryObserve(t *testing.T) {
	var consumed atomic.Uint64
	observer, err := NewSafeObserver(MaxObserverCapacity, func(Observation) { consumed.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Uint64
	var senders sync.WaitGroup
	var ready sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < 16; worker++ {
		senders.Add(1)
		ready.Add(1)
		go func() {
			defer senders.Done()
			ready.Done()
			<-start
			for index := 0; index < 1000; index++ {
				if observer.TryObserve(Observation{Kind: ObservationAttemptStarted}) {
					accepted.Add(1)
				}
			}
		}()
	}
	ready.Wait()
	closed := make(chan struct{})
	go func() {
		<-start
		_ = observer.Close()
		close(closed)
	}()
	close(start)
	senders.Wait()
	<-closed
	if got, want := consumed.Load(), accepted.Load(); got != want {
		t.Fatalf("accepted events were abandoned at close: consumed=%d accepted=%d", got, want)
	}
	for index := 0; index < 100; index++ {
		if observer.TryObserve(Observation{}) {
			t.Fatal("post-close event accepted")
		}
	}
}

func TestSafeObserverNilAndBoundsFailClosed(t *testing.T) {
	var observer *SafeObserver
	if observer.TryObserve(Observation{}) {
		t.Fatal("nil observer accepted an event")
	}
	if got, err := NewSafeObserver(-1, func(Observation) {}); err == nil || got != nil {
		t.Fatalf("negative capacity accepted: %v %v", got, err)
	}
	if got, err := NewSafeObserver(MaxObserverCapacity+1, func(Observation) {}); err == nil || got != nil {
		t.Fatalf("oversized capacity accepted: %v %v", got, err)
	}
	if got, err := NewSafeObserver(1, nil); err == nil || got != nil {
		t.Fatalf("nil handler accepted: %v %v", got, err)
	}
}

func TestSafeObserverSanitizesTraceAndBoundsDiagnostic(t *testing.T) {
	events := make(chan Observation, 1)
	observer, err := NewSafeObserver(1, func(event Observation) { events <- event })
	if err != nil {
		t.Fatal(err)
	}
	if !observer.TryObserve(Observation{
		TraceID: strings.Repeat("x", MaxObserverTraceIDBytes+1), AttemptIndex: -2,
		Diagnostic: strings.Repeat("d", MaxObserverDiagnostic+100),
	}) {
		t.Fatal("bounded event was refused")
	}
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
	event := <-events
	if event.TraceID != "" || event.AttemptIndex != -1 || len(event.Diagnostic) > MaxObserverDiagnostic {
		t.Fatalf("event was not sanitized: %+v", event)
	}
}
