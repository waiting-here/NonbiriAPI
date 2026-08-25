package connector

import (
	"errors"
	"sync"
	"sync/atomic"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

const (
	DefaultObserverCapacity = 128
	MaxObserverCapacity     = 1024
	MaxObserverDiagnostic   = 512
	MaxObserverTraceIDBytes = 128
)

type ObservationKind uint8

const (
	ObservationAttemptStarted ObservationKind = iota + 1
	ObservationAttemptFinished
)

// Observation is bounded, body-free attempt metadata. Request/response bytes,
// headers, URLs, physical ids, and credentials intentionally have no field in
// this hook; richer redacted projections belong outside the execution core.
type Observation struct {
	Kind         ObservationKind
	Connector    connectorcontract.Type
	TraceID      string
	AttemptIndex int
	Success      bool
	Committed    bool
	Failure      connectorcontract.FailureKind
	Usage        connectorcontract.Usage
	// Safe policy-state booleans only. No endpoint/key/origin/credential or
	// caller-provided value crosses this body-free observation boundary.
	StoreForcedFalse        bool
	FlattenApplied          bool
	SafetyIdentifierApplied bool
	CallerStorePresent      bool
	// CallerStoreValue is a non-secret OpenAI policy bit. It is separated
	// from CallerStorePresent so an omitted field is not confused with an
	// explicit false; the observer only receives it after strict bool decode.
	CallerStoreValueKnown bool
	CallerStoreValue      bool
	Diagnostic            string
}

// ObserverHandler consumes already-redacted observations off the request
// path. It may be slow or panic without changing a real attempt.
type ObserverHandler func(Observation)

// SafeObserver is a bounded, non-blocking sidecar. TryObserve only attempts a
// channel send; the handler runs on one private worker and panics are contained.
// A nil or zero-value observer is a no-op that rejects every event.
type SafeObserver struct {
	queue     chan Observation
	done      chan struct{}
	handler   ObserverHandler
	gate      sync.RWMutex
	hookMu    sync.Mutex
	closeHook func()
	hookOnce  sync.Once
	closed    atomic.Bool
	dropped   atomic.Uint64
	closeOnce sync.Once
}

func NewSafeObserver(capacity int, handler ObserverHandler) (*SafeObserver, error) {
	if capacity == 0 {
		capacity = DefaultObserverCapacity
	}
	if capacity < 1 || capacity > MaxObserverCapacity {
		return nil, errors.New("connector: observer capacity is out of range")
	}
	if handler == nil {
		return nil, errors.New("connector: observer handler is required")
	}
	o := &SafeObserver{
		queue:   make(chan Observation, capacity),
		done:    make(chan struct{}),
		handler: handler,
	}
	go o.run()
	return o, nil
}

// TryObserve never waits for the handler or queue capacity. It returns false
// (and records one drop) when the queue is full or closed; the only lock is a
// short send-versus-close lease that never covers handler execution.
func (o *SafeObserver) TryObserve(event Observation) bool {
	if o == nil || o.queue == nil {
		if o != nil {
			o.dropped.Add(1)
		}
		return false
	}
	o.gate.RLock()
	defer o.gate.RUnlock()
	if o.closed.Load() {
		o.dropped.Add(1)
		return false
	}
	event.Diagnostic = diagnostic.BoundTo(event.Diagnostic, MaxObserverDiagnostic)
	if !validObservationTraceID(event.TraceID) {
		event.TraceID = ""
	}
	if event.AttemptIndex < 0 || event.AttemptIndex > 1_000_000 {
		event.AttemptIndex = -1
	}
	select {
	case o.queue <- event:
		return true
	default:
		o.dropped.Add(1)
		return false
	}
}

func (o *SafeObserver) Dropped() uint64 {
	if o == nil {
		return 0
	}
	return o.dropped.Load()
}

// SetCloseHook installs one lifecycle callback which runs after Close has
// drained the worker. It is used by bounded sidecars whose admission budget
// must follow the actual queue lifetime across session replacement. A hook is
// deliberately not part of the observation callback, so it cannot run on the
// request/connector path.
func (o *SafeObserver) SetCloseHook(hook func()) {
	if o == nil || hook == nil {
		return
	}
	o.hookMu.Lock()
	if o.closed.Load() {
		o.hookMu.Unlock()
		go func() {
			<-o.done
			hook()
		}()
		return
	}
	o.closeHook = hook
	o.hookMu.Unlock()
}

func (o *SafeObserver) runCloseHook() {
	if o == nil {
		return
	}
	o.hookOnce.Do(func() {
		o.hookMu.Lock()
		hook := o.closeHook
		o.closeHook = nil
		o.hookMu.Unlock()
		if hook != nil {
			hook()
		}
	})
}

// Close rejects future events and drains every event that TryObserve already
// accepted. The gate makes send-versus-close linearizable: Close cannot close
// the queue while a sender holds a read lease, and once it takes the write
// lease no later call can report accepted. Shutdown may wait for the handler;
// real request paths never call Close. Close is idempotent.
func (o *SafeObserver) Close() error {
	if o == nil || o.queue == nil {
		return nil
	}
	o.closeOnce.Do(func() {
		o.gate.Lock()
		o.closed.Store(true)
		close(o.queue)
		o.gate.Unlock()
		<-o.done
		o.runCloseHook()
	})
	return nil
}

func (o *SafeObserver) run() {
	defer close(o.done)
	for event := range o.queue {
		o.call(event)
	}
}

func (o *SafeObserver) call(event Observation) {
	defer func() { _ = recover() }()
	o.handler(event)
}

func validObservationTraceID(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > MaxObserverTraceIDBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}
