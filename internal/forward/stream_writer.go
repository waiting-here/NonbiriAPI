package forward

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const streamHeartbeatInterval = 20 * time.Second
const streamWriteTimeout = 15 * time.Second

// streamWriter separates transport activity from validated upstream output.
// Header belongs to the connector goroutine; only serialized operations touch
// the real response writer. Heartbeats bypass the accounting checkpoint.
type streamWriter struct {
	mu                sync.Mutex
	writer            http.ResponseWriter
	header            http.Header
	ctx               context.Context
	cancel            context.CancelFunc
	started, terminal bool
	err               error
	lastWrite         time.Time
	deadline          time.Time
	stop              chan struct{}
	done              chan struct{}
}

func newStreamWriter(ctx context.Context, writer http.ResponseWriter, interval time.Duration) (*streamWriter, context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	w := &streamWriter{writer: writer, header: writer.Header().Clone(), ctx: ctx, cancel: cancel,
		lastWrite: time.Now(), stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stop:
				return
			case <-timer.C:
				w.mu.Lock()
				if !w.terminal && time.Since(w.lastWrite) >= interval && ctx.Err() == nil {
					// Do not read the connector's Header map from this goroutine.
					if !w.started {
						if err := w.setDeadlineLocked(true); err != nil {
							w.failLocked(err)
							w.mu.Unlock()
							return
						}
						w.writer.Header().Set("Content-Type", "text/event-stream")
						w.writer.Header().Set("Cache-Control", "no-cache")
						w.writer.Header().Set("X-Accel-Buffering", "no")
						w.writer.WriteHeader(http.StatusOK)
						w.started = true
					}
					_, _ = w.writeLocked([]byte(": heartbeat\n\n"), true)
				}
				remaining := interval - time.Since(w.lastWrite)
				if remaining <= 0 {
					remaining = interval
				}
				timer.Reset(remaining)
				w.mu.Unlock()
			}
		}
	}()
	return w, ctx
}

func (w *streamWriter) Header() http.Header { return w.header }

func (w *streamWriter) startLocked(status int) {
	if w.started {
		return
	}
	for name, values := range w.header {
		w.writer.Header()[name] = append([]string(nil), values...)
	}
	w.writer.WriteHeader(status)
	w.started = true
}

func (w *streamWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err == nil && w.ctx.Err() == nil {
		w.startLocked(status)
	}
}

func (w *streamWriter) setDeadlineLocked(heartbeat bool) error {
	deadline := time.Now().Add(streamWriteTimeout)
	if !heartbeat && !w.deadline.IsZero() && w.deadline.Before(deadline) {
		deadline = w.deadline
	}
	if total, ok := w.ctx.Deadline(); ok && total.Before(deadline) && !(w.terminal && errors.Is(w.ctx.Err(), context.DeadlineExceeded)) {
		deadline = total
	}
	err := http.NewResponseController(w.writer).SetWriteDeadline(deadline)
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

func (w *streamWriter) failLocked(err error) error {
	if err != nil {
		w.err = err
		w.cancel()
	}
	return err
}

func (w *streamWriter) writeLocked(body []byte, heartbeat bool) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if err := w.ctx.Err(); err != nil && !(w.terminal && errors.Is(err, context.DeadlineExceeded)) {
		return 0, err
	}
	if err := w.setDeadlineLocked(heartbeat); err != nil {
		return 0, w.failLocked(err)
	}
	n, err := w.writer.Write(body)
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	if n > 0 {
		w.lastWrite = time.Now()
	}
	if err == nil && heartbeat {
		err = w.flushLocked()
	}
	return n, w.failLocked(err)
}

func (w *streamWriter) Write(body []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if w.terminal {
		return 0, io.ErrClosedPipe
	}
	w.startLocked(http.StatusOK)
	n, err := w.writeLocked(body, false)
	// Connector terminal frames use the shared error envelope or DONE. Keep
	// their terminal ownership, including a failed attempt to deliver an error.
	if bytes.HasPrefix(body, []byte("data: {\"error\":")) || bytes.Equal(body, []byte("data: [DONE]\n\n")) {
		w.terminal = true
	}
	return n, err
}

func (w *streamWriter) flushLocked() error {
	err := http.NewResponseController(w.writer).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

func (w *streamWriter) FlushError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if err := w.setDeadlineLocked(false); err != nil {
		return w.failLocked(err)
	}
	w.startLocked(http.StatusOK)
	return w.failLocked(w.flushLocked())
}
func (w *streamWriter) Flush() { _ = w.FlushError() }

func (w *streamWriter) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deadline = deadline
	return nil
}

// stopHeartbeat joins the goroutine before settlement or terminal output.
func (w *streamWriter) stopHeartbeat() {
	close(w.stop)
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = http.NewResponseController(w.writer).SetWriteDeadline(time.Time{})
}

func (w *streamWriter) finishFailure(failure wireFailure) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started {
		return false
	}
	if w.terminal || w.err != nil || w.ctx.Err() != nil && !errors.Is(w.ctx.Err(), context.DeadlineExceeded) {
		return true
	}
	w.terminal = true
	value := httperr.New(failure.code, failure.message)
	frame := httperr.SSEErrorFrame(value)
	if failure.upstreamContext {
		value = value.WithUpstreamCode(failure.upstreamCode)
		if failure.diagnostic != "" {
			value = value.WithDiag(failure.diagnostic)
		}
		frame = httperr.SSEUpstreamErrorFrame(value)
	}
	_, _ = w.writeLocked(frame, true)
	_ = http.NewResponseController(w.writer).SetWriteDeadline(time.Time{})
	return true
}
