package credits

// Reservation state machine (implementation contract §5.3, product §J.3).
//
// Only these transitions are legal; everything else fails closed:
//
//	reserved   -> dispatched   (first legal response byte committed downstream)
//	reserved   -> released     (every candidate failed / cancelled pre-dispatch)
//	dispatched -> committed    (complete-or-unknown usage settlement)
//
// committed and released are terminal. A repeated callback or recovery sweep
// against a terminal reservation is an atomic no-op: the compare-and-set
// below matches zero rows when the stored state is no longer the expected
// source state, so a duplicate can never double-refund or double-settle.
// `reserved` can never jump straight to committed, and a dispatched
// reservation can never be released.

import "errors"

// ReservationState is the persisted state of one charity reservation row.
type ReservationState string

const (
	StateReserved   ReservationState = "reserved"
	StateDispatched ReservationState = "dispatched"
	StateCommitted  ReservationState = "committed"
	StateReleased   ReservationState = "released"
)

// ErrIllegalTransition rejects a state-machine shape the frozen contract does
// not allow. It never depends on runtime data.
var ErrIllegalTransition = errors.New("credits: illegal reservation transition")

// ParseReservationState parses a stored state string, failing closed on any
// unknown value instead of defaulting to a state.
func ParseReservationState(s string) (ReservationState, error) {
	switch ReservationState(s) {
	case StateReserved, StateDispatched, StateCommitted, StateReleased:
		return ReservationState(s), nil
	default:
		return "", ErrIllegalTransition
	}
}

// IsTerminal reports whether s is committed or released.
func (s ReservationState) IsTerminal() bool {
	return s == StateCommitted || s == StateReleased
}

// CanTransition reports whether from→to is one of the three frozen legal
// transitions.
func CanTransition(from, to ReservationState) bool {
	switch {
	case from == StateReserved && to == StateDispatched:
		return true
	case from == StateReserved && to == StateReleased:
		return true
	case from == StateDispatched && to == StateCommitted:
		return true
	default:
		return false
	}
}

// CASStore is the atomic persistence boundary for reservation state moves. A
// production implementation performs a single conditional UPDATE inside the
// settlement transaction —
//
//	UPDATE charity_reservations SET state=?, ... WHERE id=? AND state=<from>
//
// — so the move and its accounting effects commit atomically. The interface
// exists so the transition algorithm and the recovery sweep can be model-
// tested without the charity_reservations table (which lands with the
// charity routing rail); that rail must consume exactly this boundary.
type CASStore interface {
	// CompareAndSetState moves reservation id from state `from` to `to` iff
	// its current state still equals `from`. It reports whether the move was
	// applied. A false return with nil error is the terminal-replay no-op.
	CompareAndSetState(id int64, from, to ReservationState) (applied bool, err error)
}

// TransitionReservation applies one guarded state move. Replaying a callback
// whose reservation already sits in the target terminal state (or in the
// other terminal) is a no-op with applied=false, never an error: the first
// terminal transition wins and every later duplicate observes it through the
// CAS guard. An illegal from→to shape is rejected before touching storage.
func TransitionReservation(store CASStore, id int64, from, to ReservationState) (bool, error) {
	if !CanTransition(from, to) {
		return false, ErrIllegalTransition
	}
	return store.CompareAndSetState(id, from, to)
}

// RecoveryTarget returns the converging terminal state for a stalled
// reservation found during startup or periodic recovery, and whether a
// transition is needed at all:
//
//   - reserved   → released (refund the user reserve, release the key reserve;
//     the settlement snapshot comes from the persisted reservation row);
//   - dispatched → committed under unknown-usage semantics (the user keeps
//     paying the discounted reserve; the key consumes its undiscounted
//     reserve; reward 0). Recovery reads only the persisted price/reserve
//     snapshots, never current configuration;
//   - committed/released stay untouched.
func RecoveryTarget(s ReservationState) (ReservationState, bool) {
	switch s {
	case StateReserved:
		return StateReleased, true
	case StateDispatched:
		return StateCommitted, true
	default:
		return s, false
	}
}
