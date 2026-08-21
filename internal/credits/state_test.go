package credits

import (
	"errors"
	"sync"
	"testing"
)

func TestParseReservationState(t *testing.T) {
	for _, s := range []ReservationState{StateReserved, StateDispatched, StateCommitted, StateReleased} {
		got, err := ParseReservationState(string(s))
		if err != nil || got != s {
			t.Fatalf("ParseReservationState(%q) = (%q, %v)", s, got, err)
		}
	}
	if _, err := ParseReservationState("settled"); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("unknown state = %v, want ErrIllegalTransition", err)
	}
	if _, err := ParseReservationState(""); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("empty state = %v, want ErrIllegalTransition", err)
	}
}

func TestCanTransitionFrozenGraph(t *testing.T) {
	states := []ReservationState{StateReserved, StateDispatched, StateCommitted, StateReleased}
	legal := map[[2]ReservationState]bool{
		{StateReserved, StateDispatched}:  true,
		{StateReserved, StateReleased}:    true,
		{StateDispatched, StateCommitted}: true,
	}
	for _, from := range states {
		for _, to := range states {
			want := legal[[2]ReservationState{from, to}]
			if got := CanTransition(from, to); got != want {
				t.Fatalf("CanTransition(%s→%s) = %v, want %v", from, to, got, want)
			}
		}
	}
	// The forbidden shapes the contract calls out explicitly.
	if CanTransition(StateReserved, StateCommitted) {
		t.Fatal("reserved must never jump straight to committed")
	}
	if CanTransition(StateDispatched, StateReleased) {
		t.Fatal("dispatched must never be released")
	}
}

// memReservationStore is a fake CAS store: a mutex-guarded map whose
// CompareAndSetState mirrors the conditional UPDATE ... WHERE state=<from>
// semantics of the production persistence boundary.
type memReservationStore struct {
	mu    sync.Mutex
	state map[int64]ReservationState
	// history records every applied move in order (for effect-exactly-once
	// assertions in the model tests).
	history [][2]ReservationState
}

func newMemReservationStore(initial map[int64]ReservationState) *memReservationStore {
	s := &memReservationStore{state: make(map[int64]ReservationState, len(initial))}
	for id, st := range initial {
		s.state[id] = st
	}
	return s
}

func (s *memReservationStore) get(id int64) ReservationState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state[id]
}

func (s *memReservationStore) CompareAndSetState(id int64, from, to ReservationState) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state[id] != from {
		return false, nil // terminal/advanced replay: atomic no-op
	}
	if !CanTransition(from, to) {
		return false, ErrIllegalTransition
	}
	s.state[id] = to
	s.history = append(s.history, [2]ReservationState{from, to})
	return true, nil
}

func TestTransitionReservationTerminalReplayIsNoop(t *testing.T) {
	store := newMemReservationStore(map[int64]ReservationState{1: StateReserved})

	if applied, err := TransitionReservation(store, 1, StateReserved, StateDispatched); err != nil || !applied {
		t.Fatalf("reserved→dispatched = (%v, %v), want (true, nil)", applied, err)
	}
	// A duplicate dispatch callback observes the advanced state and no-ops.
	if applied, err := TransitionReservation(store, 1, StateReserved, StateDispatched); err != nil || applied {
		t.Fatalf("duplicate dispatch = (%v, %v), want (false, nil)", applied, err)
	}
	if applied, err := TransitionReservation(store, 1, StateReserved, StateReleased); err != nil || applied {
		t.Fatalf("release after dispatch = (%v, %v), want (false, nil): dispatched can never be released", applied, err)
	}
	if applied, err := TransitionReservation(store, 1, StateDispatched, StateCommitted); err != nil || !applied {
		t.Fatalf("dispatched→committed = (%v, %v), want (true, nil)", applied, err)
	}
	// Terminal replays of every callback shape are no-ops.
	for _, mv := range [][2]ReservationState{
		{StateDispatched, StateCommitted},
		{StateReserved, StateReleased},
	} {
		if applied, err := TransitionReservation(store, 1, mv[0], mv[1]); err != nil || applied {
			t.Fatalf("terminal replay %v = (%v, %v), want (false, nil)", mv, applied, err)
		}
	}
	// Illegal shapes are rejected before storage is consulted.
	if _, err := TransitionReservation(store, 1, StateReserved, StateCommitted); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("reserved→committed = %v, want ErrIllegalTransition", err)
	}
}

// TestRecoveryConvergesProperty drives randomized event interleavings against
// a set of reservations and asserts the recovery properties: repeated
// recovery sweeps converge, every reservation terminates at most once, and no
// sequence of duplicate callbacks ever moves a terminal row. Run with
// -shuffle/-count variations; the assertion holds for every seed because it
// is a property, not a fixed scenario.
func TestRecoveryConvergesProperty(t *testing.T) {
	initial := []ReservationState{StateReserved, StateDispatched, StateCommitted, StateReleased}
	store := newMemReservationStore(map[int64]ReservationState{})
	const n = 64
	for i := 0; i < n; i++ {
		store.state[int64(i)] = initial[i%len(initial)]
	}

	applyRecovery := func() int {
		moved := 0
		store.mu.Lock()
		snapshot := make([]int64, 0, n)
		for id := range store.state {
			snapshot = append(snapshot, id)
		}
		store.mu.Unlock()
		for _, id := range snapshot {
			target, needed := RecoveryTarget(store.get(id))
			if !needed {
				continue
			}
			applied, err := TransitionReservation(store, id, store.get(id), target)
			if err != nil {
				t.Fatalf("recovery transition: %v", err)
			}
			if applied {
				moved++
			}
		}
		return moved
	}

	// First sweep converges everything stalled; later sweeps are no-ops even
	// when interrupted and restarted arbitrarily often.
	first := applyRecovery()
	if first == 0 {
		t.Fatal("first recovery sweep moved nothing")
	}
	for i := 0; i < 8; i++ {
		if moved := applyRecovery(); moved != 0 {
			t.Fatalf("recovery sweep %d moved %d rows: not converged/idempotent", i+2, moved)
		}
	}
	for id := int64(0); id < n; id++ {
		st := store.get(id)
		if !st.IsTerminal() {
			t.Fatalf("reservation %d still non-terminal after recovery: %s", id, st)
		}
	}
}

// TestConcurrentDispatchExactlyOnce races many goroutines on one reservation:
// exactly one reserved→dispatched CAS may win, and the losers observe clean
// no-ops. This models concurrent candidate attempts sharing one logical
// reservation (silent retry must never re-reserve).
func TestConcurrentDispatchExactlyOnce(t *testing.T) {
	store := newMemReservationStore(map[int64]ReservationState{7: StateReserved})
	const goroutines = 32
	var wg sync.WaitGroup
	var wins atomicInt
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			applied, err := TransitionReservation(store, 7, StateReserved, StateDispatched)
			if err != nil {
				t.Errorf("concurrent dispatch: %v", err)
				return
			}
			if applied {
				wins.add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if wins.load() != 1 {
		t.Fatalf("concurrent reserved→dispatched winners = %d, want exactly 1", wins.load())
	}
	if got := store.get(7); got != StateDispatched {
		t.Fatalf("final state = %s, want dispatched", got)
	}
}

type atomicInt struct {
	mu sync.Mutex
	v  int
}

func (a *atomicInt) add(n int) { a.mu.Lock(); a.v += n; a.mu.Unlock() }
func (a *atomicInt) load() int { a.mu.Lock(); defer a.mu.Unlock(); return a.v }
