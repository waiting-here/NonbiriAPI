package charityrouting

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

type deadlineCapableWriter struct {
	header   http.Header
	deadline time.Time
	writes   int
}

func (w *deadlineCapableWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *deadlineCapableWriter) WriteHeader(int) {}

func (w *deadlineCapableWriter) Write(p []byte) (int, error) {
	if w.deadline.IsZero() {
		return 0, errors.New("test: write deadline was not forwarded")
	}
	w.writes++
	return len(p), nil
}

func (w *deadlineCapableWriter) Flush() {}

func (w *deadlineCapableWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestDispatchWriterResponseControllerForwardsWriteDeadline(t *testing.T) {
	underlying := &deadlineCapableWriter{}
	casCalls := 0
	writer := newDispatchWriter(underlying, func() bool {
		casCalls++
		return true
	}, nil)

	deadline := time.Now().Add(time.Minute)
	controller := http.NewResponseController(writer)
	if err := controller.SetWriteDeadline(deadline); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	if !underlying.deadline.Equal(deadline) {
		t.Fatalf("underlying deadline = %v, want %v", underlying.deadline, deadline)
	}
	if casCalls != 0 {
		t.Fatalf("CAS calls after deadline setup = %d, want 0", casCalls)
	}

	n, err := writer.Write([]byte("body"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len("body") || underlying.writes != 1 {
		t.Fatalf("write result = (n=%d, writes=%d), want (%d, 1)", n, underlying.writes, len("body"))
	}
	if casCalls != 1 {
		t.Fatalf("CAS calls after first body write = %d, want 1", casCalls)
	}
}
