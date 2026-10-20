package forward

import (
	"net/http"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// Charity streams establish the durable billing checkpoint at HTTP 200.
// Other responses establish it on validated output, before downstream delivery.
type responseStartWriter struct {
	http.ResponseWriter
	mark               func() error
	observe            func(connectorcontract.ResponseObservation) error
	now                func() time.Time
	retryAfterObserved bool
	acceptedStreams    bool
	upstreamStatus     int
	started            bool
	err                error
}

func (w *responseStartWriter) ObserveUpstreamResponse(status int, stream bool, headers http.Header) error {
	if w.acceptedStreams && w.observe != nil && (len(headers.Values("Retry-After")) != 0 || len(headers.Values("X-Retry-After")) != 0) {
		received := w.now()
		if until, valid := connectorcontract.ParseRetryAfter(headers, received); valid {
			if w.err != nil {
				return w.err
			}
			w.err = w.observe(connectorcontract.ResponseObservation{Status: status, Stream: stream, ObservedAt: received.Unix(), RateLimitedUntil: until})
			if w.err != nil {
				return w.err
			}
			w.retryAfterObserved = true
			if stream && status == http.StatusOK {
				w.upstreamStatus, w.started = status, true
			}
			return nil
		}
	}
	if w.acceptedStreams && stream && status == http.StatusOK {
		w.upstreamStatus = status
		return w.MarkResponseStarted()
	}
	return nil
}

func (w *responseStartWriter) Write(body []byte) (int, error) {
	if len(body) > 0 {
		if err := w.MarkResponseStarted(); err != nil {
			return 0, err
		}
	}
	return w.ResponseWriter.Write(body)
}

func (w *responseStartWriter) MarkResponseStarted() error {
	if !w.started && w.err == nil {
		w.err = w.mark()
		w.started = w.err == nil
	}
	return w.err
}

func (w *responseStartWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type flushingResponseStartWriter struct {
	*responseStartWriter
}

func (w *flushingResponseStartWriter) FlushError() error {
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *flushingResponseStartWriter) Flush() { _ = w.FlushError() }

func checkpointResponseWriter(writer http.ResponseWriter, mark func() error) (http.ResponseWriter, *responseStartWriter) {
	checkpoint := &responseStartWriter{ResponseWriter: writer, mark: mark}
	if _, ok := writer.(http.Flusher); ok {
		return &flushingResponseStartWriter{responseStartWriter: checkpoint}, checkpoint
	}
	return checkpoint, checkpoint
}
