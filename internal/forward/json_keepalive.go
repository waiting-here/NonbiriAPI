package forward

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// Only operations whose successful response is always JSON may send leading
// whitespace. Audio transcription and translation can instead return text.
func supportsJSONKeepalive(request *validatedRequest) bool {
	if request == nil || request.Stream {
		return false
	}
	switch request.operation {
	case contract.OperationChatCompletions, contract.OperationEmbeddings,
		contract.OperationImagesGenerations, contract.OperationDecisions,
		contract.OperationResponses, contract.OperationResponsesCompact,
		contract.OperationResponsesInputTokens, contract.OperationCompletions,
		contract.OperationImagesEdits, contract.OperationImagesVariations,
		contract.OperationModerations, contract.OperationRerank, contract.OperationSystemOne:
		return true
	default:
		return false
	}
}

// JSON keepalives are transport activity, outside the attempt's response and
// billing checkpoints. The connector owns header; mu owns all actual I/O.
type jsonKeepaliveWriter struct {
	mu                          sync.Mutex
	writer                      http.ResponseWriter
	header                      http.Header
	ctx                         context.Context
	cancel                      context.CancelFunc
	armed, started, bodyStarted bool
	finalizing                  bool
	err                         error
	deadline                    time.Time
	lastWrite                   time.Time
	stop, done                  chan struct{}
	stopOnce                    sync.Once
}

func newJSONKeepaliveWriter(ctx context.Context, writer http.ResponseWriter, interval time.Duration) (*jsonKeepaliveWriter, context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	w := &jsonKeepaliveWriter{writer: writer, header: writer.Header().Clone(), ctx: ctx, cancel: cancel,
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
				if w.armed && !w.bodyStarted && time.Since(w.lastWrite) >= interval && ctx.Err() == nil {
					if !w.started {
						if err := w.setDeadlineLocked(); err != nil {
							w.failLocked(err)
							w.mu.Unlock()
							return
						}
						// Never read the connector-owned header from the timer goroutine.
						w.writer.Header().Set("Content-Type", "application/json")
						w.writer.Header().Set("Cache-Control", "no-store")
						w.writer.Header().Set("X-Accel-Buffering", "no")
						w.writer.Header().Del("Content-Length")
						w.writer.WriteHeader(http.StatusOK)
						w.started = true
					}
					if _, err := w.writeLocked([]byte{'\n'}); err == nil {
						w.failLocked(w.flushLocked())
					}
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

func (w *jsonKeepaliveWriter) armHeartbeat() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.armed {
		w.armed, w.lastWrite = true, time.Now()
	}
}

func (w *jsonKeepaliveWriter) Header() http.Header { return w.header }

func (w *jsonKeepaliveWriter) contextErrorLocked() error {
	err := w.ctx.Err()
	if w.finalizing && errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

func (w *jsonKeepaliveWriter) setDeadlineLocked() error {
	deadline := time.Now().Add(streamWriteTimeout)
	if !w.deadline.IsZero() && w.deadline.Before(deadline) && !w.finalizing {
		deadline = w.deadline
	}
	if total, ok := w.ctx.Deadline(); ok && total.Before(deadline) && !(w.finalizing && errors.Is(w.ctx.Err(), context.DeadlineExceeded)) {
		deadline = total
	}
	err := http.NewResponseController(w.writer).SetWriteDeadline(deadline)
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

func (w *jsonKeepaliveWriter) failLocked(err error) error {
	if err != nil {
		w.err = err
		w.cancel()
	}
	return err
}

func (w *jsonKeepaliveWriter) startLocked(status int) {
	if w.started {
		return
	}
	for name, values := range w.header {
		w.writer.Header()[name] = append([]string(nil), values...)
	}
	w.writer.WriteHeader(status)
	w.started = true
}

func (w *jsonKeepaliveWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err == nil && w.contextErrorLocked() == nil {
		w.startLocked(status)
	}
}

func (w *jsonKeepaliveWriter) writeLocked(body []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if err := w.contextErrorLocked(); err != nil {
		return 0, err
	}
	if err := w.setDeadlineLocked(); err != nil {
		return 0, w.failLocked(err)
	}
	n, err := w.writer.Write(body)
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	if n > 0 {
		w.lastWrite = time.Now()
	}
	return n, w.failLocked(err)
}

func (w *jsonKeepaliveWriter) Write(body []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	if err := w.contextErrorLocked(); err != nil {
		return 0, err
	}
	w.bodyStarted = true
	w.startLocked(http.StatusOK)
	n, err := w.writeLocked(body)
	if err == nil {
		// Do not leave a completed JSON body buffered during settlement after
		// disabling its timer. Flush errors remain real delivery failures.
		err = w.failLocked(w.flushLocked())
	}
	return n, err
}

func (w *jsonKeepaliveWriter) flushLocked() error {
	err := http.NewResponseController(w.writer).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

func (w *jsonKeepaliveWriter) FlushError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if err := w.contextErrorLocked(); err != nil {
		return err
	}
	if err := w.setDeadlineLocked(); err != nil {
		return w.failLocked(err)
	}
	w.startLocked(http.StatusOK)
	return w.failLocked(w.flushLocked())
}

func (w *jsonKeepaliveWriter) Flush() { _ = w.FlushError() }

func (w *jsonKeepaliveWriter) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deadline = deadline
	return nil
}

func (w *jsonKeepaliveWriter) stopHeartbeat() {
	w.stopOnce.Do(func() {
		close(w.stop)
		<-w.done
		w.mu.Lock()
		defer w.mu.Unlock()
		_ = http.NewResponseController(w.writer).SetWriteDeadline(time.Time{})
	})
}

func (w *jsonKeepaliveWriter) finishFailure(failure wireFailure) {
	w.mu.Lock()
	if w.bodyStarted || w.err != nil || w.ctx.Err() != nil && !errors.Is(w.ctx.Err(), context.DeadlineExceeded) {
		w.mu.Unlock()
		return
	}
	w.finalizing = true
	w.mu.Unlock()
	// The ordinary error envelope retains its status until the first heartbeat.
	// After that, only its JSON body can carry the failure.
	writeFailure(w, failure)
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = http.NewResponseController(w.writer).SetWriteDeadline(time.Time{})
}
