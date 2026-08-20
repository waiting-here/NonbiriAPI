// Package maintenance holds the process-wide, atomically live-applied
// maintenance admission gate and the HTTP middleware that enforces it on the
// user-station API and the OpenAI-compatible exit.
//
// The gate is server-side authoritative: it is read on every user-station
// /api/* and /v1/* request after the host/station edge and before any auth or
// business logic, so flipping maintenance_mode takes effect immediately for
// already-issued sessions and caller keys — not only for the React page that
// happens to read the public config. The admin station is never routed
// through this gate, so an operator can always toggle maintenance off.
//
// The gate is a single atomic bool: there is no per-request database read, and
// a live apply (site_config PATCH) applies the value to the runtime singleton
// first and then persists it, reverting the runtime singleton on a persistence
// failure (see adminapi). On startup the persisted value is applied before
// the server listens.
package maintenance

import (
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// Gate is the process-wide maintenance admission state. A nil gate is treated
// as never-enabled by Enabled and as a pass-through by GateMiddleware, so test
// wiring that does not care about maintenance keeps working.
type Gate struct {
	enabled atomic.Bool
}

// New returns a gate in the disabled (normal-operation) state.
func New() *Gate { return &Gate{} }

// Enabled reports the current maintenance state. A nil gate is never enabled.
func (g *Gate) Enabled() bool {
	if g == nil {
		return false
	}
	return g.enabled.Load()
}

// Set live-applies the maintenance state atomically. It is safe for concurrent
// use with Enabled and inflight requests: a toggle is observed by the next
// request that reaches the gate. A nil gate is a no-op.
func (g *Gate) Set(enabled bool) {
	if g == nil {
		return
	}
	g.enabled.Store(enabled)
}

// allowedDuringMaintenance is the strict allowlist of user-station paths that
// remain usable while the gate is enabled. The public config endpoint must
// stay open so the SPA can learn that maintenance is on and render the notice;
// logout must stay open so an already-logged-in user can end their session.
// Everything else under /api/* (session, me, OAuth start/callback/elevate,
// endpoints, models, issues, account export/delete, caller-key) and all of
// /v1/* is refused with a stable 503. The admin station does not route through
// this middleware at all, and /healthz is mounted outside it, so neither needs
// an entry here.
func allowedDuringMaintenance(path string) bool {
	if path == "/api/config" || path == "/api/auth/logout" {
		return true
	}
	return false
}

// GateMiddleware enforces the maintenance gate on the user-station API and the
// OpenAI-compatible exit. It is mounted after the host/station edge (which
// validates the station and only lets /api/* and /v1/* reach the user station)
// and before any auth or business handler. When the gate is disabled the
// request passes through unchanged; when enabled, only the allowlist above is
// admitted and every other path receives a stable 503 service_unavailable
// envelope (source platform, no-store) — the same shape a fresh request would
// receive from any platform admission decision. A nil gate is a pass-through.
func GateMiddleware(gate *Gate, next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gate == nil || !gate.Enabled() || allowedDuringMaintenance(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		httperr.WriteError(w, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
	})
}

// isUserStationAPIPath reports whether a path belongs to the user-station API
// or the OpenAI-compatible exit. It is a package-internal helper used by the
// gate tests to drive the route matrix without mounting the real httpmw edge.
func isUserStationAPIPath(path string) bool {
	return strings.HasPrefix(path, "/api/") || path == "/api" ||
		strings.HasPrefix(path, "/v1/") || path == "/v1"
}
