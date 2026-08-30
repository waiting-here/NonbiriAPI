// Package maintenance owns the Generation 2 maintenance transition service,
// closed continuation registry and process-visible admission gate.
package maintenance

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// State is the committed maintenance singleton projection.
type State struct {
	Enabled        bool
	Revision       int64
	ChangedAt      int64
	CurrentEventID string
}

func (state State) valid() bool {
	return state.Revision >= 1 && state.ChangedAt >= 0 && state.ChangedAt <= maxUnixSecond
}

type runtimeState struct {
	value State
}

// Gate publishes only committed state. New gates are intentionally
// uninitialized; PrepareListener must load the database singleton before the
// HTTP listener is exposed.
type Gate struct {
	state atomic.Pointer[runtimeState]
}

func NewGate() *Gate { return &Gate{} }

// Ready reports whether committed database state has initialized the gate.
func (gate *Gate) Ready() bool {
	return gate != nil && gate.state.Load() != nil
}

// State returns the latest committed process projection.
func (gate *Gate) State() (State, bool) {
	if gate == nil {
		return State{}, false
	}
	current := gate.state.Load()
	if current == nil {
		return State{}, false
	}
	return current.value, true
}

// Enabled reports committed maintenance state. An uninitialized gate returns
// false here for diagnostics; GateMiddleware still fails closed until Ready.
func (gate *Gate) Enabled() bool {
	state, ok := gate.State()
	return ok && state.Enabled
}

func (gate *Gate) observeCommitted(state State) error {
	if gate == nil || !state.valid() {
		return errors.New("invalid committed maintenance state")
	}
	next := &runtimeState{value: state}
	for {
		current := gate.state.Load()
		if current != nil {
			if current.value.Revision > state.Revision {
				return nil
			}
			if current.value.Revision == state.Revision {
				if current.value == state {
					return nil
				}
				return errors.New("conflicting committed maintenance revision")
			}
		}
		if gate.state.CompareAndSwap(current, next) {
			return nil
		}
	}
}

func allowedDuringMaintenance(path string) bool {
	return path == "/api/config" || path == "/api/auth/logout"
}

// GateMiddleware rejects all normal user-station/API intent while committed
// maintenance is enabled. Exact accepted continuations are authorized by the
// Registry before their narrowly registered handler is invoked; they are not
// inferred from this path allowlist. Admin routes are mounted outside this
// middleware.
func GateMiddleware(gate *Gate, next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		state, ready := gate.State()
		if !ready {
			httperr.WriteError(w, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
			return
		}
		if !state.Enabled || allowedDuringMaintenance(request.URL.Path) {
			next.ServeHTTP(w, request)
			return
		}
		httperr.WriteError(w, httperr.New(httperr.CodeMaintenance, "maintenance in progress"))
	})
}

func isUserStationAPIPath(path string) bool {
	return strings.HasPrefix(path, "/api/") || path == "/api" ||
		strings.HasPrefix(path, "/v1/") || path == "/v1"
}
