package charityrouting

import (
	"errors"
	"net/http"
	"sync"
)

// errDispatchRefused is returned by the spy writer after the dispatch CAS
// failed: the reservation can no longer accept bytes (a terminal move won the
// race), so every subsequent write must fail and drive the connector into its
// sink-failure path instead of streaming against a dead reservation.
var errDispatchRefused = errors.New("charityrouting: dispatch was refused")

// dispatchWriter wraps the client ResponseWriter with the frozen dispatch
// boundary (§5.3 / clarification §C1.5): the FIRST legal response-body byte
// successfully submitted downstream is the linearization point of the
// reserved→dispatched compare-and-set.
//
// Crash ordering: the CAS runs synchronously inside the first non-empty Write,
// BEFORE the byte is delegated, so a crash after this point always finds a
// dispatched row that recovery converges conservatively (committed unknown)
// — never a released one after the client already received data. This keeps
// the uncertain crash window conservative, as the audit requires.
//
// Provable zero-byte compensation: if that first non-empty Write delegates and
// the underlying writer returns ZERO bytes (n == 0 — a sink failure such as an
// HTTP/2 stream reset, an early client disconnect, or a write-deadline/pressure
// failure), the frozen "first body byte successfully submitted" boundary was
// NOT crossed. The writer then calls the compensate hook once to revert the
// row dispatched→reserved, so the service's pre-dispatch path can release the
// reservation (refund the user, free the donation-key cap) and retry the next
// candidate. A concurrent terminal move that already settled the row (account
// deletion or recovery) makes the compensation CAS a no-op and the row keeps
// its conservative state; the service then tolerates the illegal-transition
// release as before. The compensation runs at most once per writer and never
// after any byte has been delivered, so a real short write (n > 0) keeps the
// row dispatched and is settled under the frozen commit formula.
//
// The writer performs no buffering, no transformation, and no header logic:
// Header() delegates verbatim, Flush delegates when the underlying writer
// supports it (the SSE path requires it), and WriteHeader delegates as-is. A
// zero-length Write carries no body byte and never crosses the boundary, so
// it delegates verbatim without the CAS (a header-only flush cannot poison a
// later real write).
type dispatchWriter struct {
	http.ResponseWriter

	cas        func() bool // reserved→dispatched CAS; true = now dispatched
	compensate func()      // dispatched→reserved un-dispatch on provable 0-byte

	once      sync.Once
	poisoned  bool // CAS lost to a concurrent terminal move
	delivered bool // at least one body byte delegated successfully
	compOnce  sync.Once
}

func newDispatchWriter(w http.ResponseWriter, cas func() bool, compensate func()) *dispatchWriter {
	return &dispatchWriter{ResponseWriter: w, cas: cas, compensate: compensate}
}

func (d *dispatchWriter) Write(p []byte) (int, error) {
	// A zero-length write carries no body byte and does not cross the frozen
	// dispatch boundary. Delegate verbatim without the CAS.
	if len(p) == 0 {
		return d.ResponseWriter.Write(p)
	}
	d.once.Do(func() {
		if d.cas != nil && d.cas() {
			return // now dispatched
		}
		// The CAS lost to a concurrent terminal move (recovery / account
		// delete). Fail every further write so the adapter terminates via its
		// sink-failure path instead of streaming against a settled state.
		d.poisoned = true
	})
	if d.poisoned {
		return 0, errDispatchRefused
	}
	n, err := d.ResponseWriter.Write(p)
	if n > 0 {
		d.delivered = true
	} else if !d.delivered {
		// The CAS ran (the row is dispatched) but this first non-empty write
		// provably delivered ZERO body bytes: the frozen boundary was NOT
		// crossed. Compensate once by reverting dispatched→reserved so the
		// service can release and retry. The adapter never retries a write
		// after a zero-byte return, so a later second write cannot re-cross a
		// compensated row. A concurrent terminal move that already settled
		// the row makes this CAS a no-op (the row keeps its conservative
		// state) and the service tolerates the resulting illegal transition.
		d.compOnce.Do(d.compensate)
	}
	return n, err
}

// Flush satisfies http.Flusher for the streaming path and lets
// http.ResponseController drive a flush through this wrapper.
func (d *dispatchWriter) Flush() {
	if flusher, ok := d.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.NewResponseController reach optional interfaces on the
// underlying writer, including SetWriteDeadline, without bypassing this
// wrapper's Write/CAS boundary.
func (d *dispatchWriter) Unwrap() http.ResponseWriter { return d.ResponseWriter }
