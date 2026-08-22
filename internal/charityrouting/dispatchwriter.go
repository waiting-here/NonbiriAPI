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
// boundary (§5.3): the FIRST legal response-body byte submitted downstream is
// the linearization point of the reserved→dispatched compare-and-set. The CAS
// runs synchronously inside the first Write, BEFORE the byte is delegated, so
// a crash after this point always finds a dispatched row that recovery
// converges conservatively (committed unknown) — never a released one after
// the client already received data.
//
// The writer performs no buffering, no transformation, and no header logic:
// Header() delegates verbatim, Flush delegates when the underlying writer
// supports it (the SSE path requires it), and WriteHeader delegates as-is.
type dispatchWriter struct {
	http.ResponseWriter

	onFirstByte func() bool // CAS; true = now dispatched

	once       sync.Once
	dispatched bool
	poisoned   bool
}

func newDispatchWriter(w http.ResponseWriter, onFirstByte func() bool) *dispatchWriter {
	return &dispatchWriter{ResponseWriter: w, onFirstByte: onFirstByte}
}

func (d *dispatchWriter) markDispatched() {
	d.once.Do(func() {
		if d.onFirstByte != nil && d.onFirstByte() {
			d.dispatched = true
		} else {
			d.poisoned = true
			d.dispatched = true // bytes crossed the boundary even though the CAS lost
		}
	})
}

func (d *dispatchWriter) Write(p []byte) (int, error) {
	d.markDispatched()
	if d.poisoned {
		// Fail every further write so the adapter terminates the stream via
		// its sink-failure path instead of writing against a settled state.
		return 0, errDispatchRefused
	}
	n, err := d.ResponseWriter.Write(p)
	if n > 0 {
		d.markDispatched()
	}
	return n, err
}

// Flush satisfies http.Flusher for the streaming path and for
// http.ResponseController's deadline handling.
func (d *dispatchWriter) Flush() {
	if flusher, ok := d.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
