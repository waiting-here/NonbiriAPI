package forward

import "net/http"

// Connectors write only validated successful payloads before their first body
// commit. Error frames are emitted only after that boundary. The checkpoint
// therefore records upstream output even if the downstream write later fails.
type responseStartWriter struct {
	http.ResponseWriter
	mark    func() error
	started bool
	err     error
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
	flusher http.Flusher
}

func (w *flushingResponseStartWriter) Flush() { w.flusher.Flush() }

func checkpointResponseWriter(writer http.ResponseWriter, mark func() error) (http.ResponseWriter, *responseStartWriter) {
	checkpoint := &responseStartWriter{ResponseWriter: writer, mark: mark}
	if flusher, ok := writer.(http.Flusher); ok {
		return &flushingResponseStartWriter{responseStartWriter: checkpoint, flusher: flusher}, checkpoint
	}
	return checkpoint, checkpoint
}
