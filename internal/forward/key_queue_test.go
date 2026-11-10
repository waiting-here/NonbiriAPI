package forward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestDeferredHeartbeatPreservesQueueRejectionStatus(t *testing.T) {
	writer := &heartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, _ := newStreamWriter(ctx, writer, time.Millisecond, true)
	defer stream.cancel()
	// Several heartbeat intervals pass before admission. A real rejection must
	// retain its HTTP status instead of becoming an error inside HTTP 200.
	select {
	case <-writer.heartbeat:
		t.Fatal("queue wait committed HTTP 200")
	case <-time.After(10 * time.Millisecond):
	}
	stream.stopHeartbeat()
	failure := platformFailure(httperr.CodeRateLimited, "at capacity")
	if !stream.finishFailure(failure) {
		writeFailure(stream, failure)
	}
	if writer.Code != http.StatusTooManyRequests {
		t.Fatalf("queue rejection HTTP %d", writer.Code)
	}
}

func TestDeferredHeartbeatStartsAfterDispatch(t *testing.T) {
	writer := &heartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1)}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	stream, _ := newStreamWriter(ctx, writer, time.Millisecond, true)
	defer stream.cancel()
	stream.armHeartbeat()
	if err := waitHeartbeat(ctx, writer); err != nil {
		t.Fatal(err)
	}
	stream.stopHeartbeat()
	if writer.Code != http.StatusOK {
		t.Fatalf("dispatched stream HTTP %d", writer.Code)
	}
}
