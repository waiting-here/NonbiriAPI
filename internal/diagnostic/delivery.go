package diagnostic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// Delivery describes only this server's writes to its immediate peer. A
// successful write does not establish that the final client consumed it.
type Delivery struct {
	HTTPStatus int      `json:"http_status,omitempty"`
	Bytes      int64    `json:"bytes"`
	Failed     bool     `json:"failed"`
	Failure    *Failure `json:"failure,omitempty"`
}

type DeliveryWriter struct {
	http.ResponseWriter
	ctx    context.Context
	mu     sync.Mutex
	status int
	bytes  int64
	failed bool
}

func ObserveWriter(w http.ResponseWriter, ctx context.Context) (http.ResponseWriter, *DeliveryWriter) {
	d := &DeliveryWriter{ResponseWriter: w, ctx: ctx}
	if _, ok := w.(http.Flusher); ok {
		return &flushingDeliveryWriter{d}, d
	}
	return d, d
}

func (w *DeliveryWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *DeliveryWriter) WriteHeader(status int) {
	w.mu.Lock()
	if w.status == 0 && (status >= 200 || status == http.StatusSwitchingProtocols) {
		w.status = status
		DeliveryStarted(w.ctx)
	}
	w.mu.Unlock()
	w.ResponseWriter.WriteHeader(status)
}
func (w *DeliveryWriter) Write(body []byte) (int, error) {
	w.mu.Lock()
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.mu.Unlock()
	n, err := w.ResponseWriter.Write(body)
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	w.mu.Lock()
	w.bytes += int64(max(n, 0))
	w.failed = w.failed || err != nil
	w.mu.Unlock()
	RecordWrite(w.ctx, n, err)
	return n, err
}
func (w *DeliveryWriter) Snapshot() Delivery {
	w.mu.Lock()
	d := Delivery{HTTPStatus: w.status, Bytes: w.bytes, Failed: w.failed}
	w.mu.Unlock()
	if d.Failed {
		d.Failure = Finish(w.ctx, false, d.HTTPStatus != 0, false, 0, "Response delivery failed", "downstream")
	}
	return d
}
func (w *DeliveryWriter) SetWriteDeadline(deadline time.Time) error {
	err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(deadline)
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		RecordError(w.ctx, "write_client", err)
	}
	return err
}

type flushingDeliveryWriter struct{ *DeliveryWriter }

func (w *flushingDeliveryWriter) FlushError() error {
	DeliveryStarted(w.ctx)
	w.mu.Lock()
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.mu.Unlock()
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		w.mu.Lock()
		w.failed = true
		w.mu.Unlock()
		RecordError(w.ctx, "write_client", err)
	}
	return err
}
func (w *flushingDeliveryWriter) Flush() { _ = w.FlushError() }
