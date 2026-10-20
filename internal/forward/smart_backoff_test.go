package forward

import (
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSmartBackoffObserverOnlyAddsTrialAcceptance(t *testing.T) {
	for _, trial := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			calls, marks := 0, 0
			_, w := checkpointResponseWriter(httptest.NewRecorder(), func() error { marks++; return nil })
			w.acceptedStreams = true
			w.backoffTrial = trial
			w.now = func() time.Time { return time.Unix(1700000000, 0) }
			w.observe = func(value contract.ResponseObservation) error {
				calls++
				if value.HasRetryAfter || value.Status != 200 || value.Stream != stream {
					t.Fatal(value)
				}
				return nil
			}
			if err := w.ObserveUpstreamResponse(200, stream, nil); err != nil {
				t.Fatal(err)
			}
			wantCalls := 0
			if trial {
				wantCalls = 1
			}
			wantMarks := 0
			if stream && !trial {
				wantMarks = 1
			}
			if calls != wantCalls || marks != wantMarks || w.started != stream {
				t.Fatal(trial, stream, calls, marks, w.started)
			}
			if _, err := w.Write([]byte("validated output")); err != nil {
				t.Fatal(err)
			}
			if !stream && marks != 1 {
				t.Fatal("nonstream billing checkpoint missing")
			}
		}
	}
	// A valid zero header is observed as a header fact, not a synthetic trial acceptance.
	_, w := checkpointResponseWriter(httptest.NewRecorder(), func() error { t.Fatal("unexpected billing"); return nil })
	w.acceptedStreams = true
	w.backoffTrial = true
	w.now = func() time.Time { return time.Unix(1700000000, 0) }
	w.observe = func(value contract.ResponseObservation) error {
		if !value.HasRetryAfter || value.RateLimitedUntil != 1700000000 {
			t.Fatal(value)
		}
		return nil
	}
	if err := w.ObserveUpstreamResponse(429, false, http.Header{"Retry-After": {"0"}}); err != nil {
		t.Fatal(err)
	}
}
