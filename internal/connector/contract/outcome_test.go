package contract

import (
	"context"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"net"
	"net/http"
	"testing"
)

func TestReadOutcomeSeparatesNetworkInterruptionAndProtocolFailure(t *testing.T) {
	result := UpstreamFailed(AttemptResult{Failure: FailureUpstream}, OriginUpstreamProtocol)
	for _, tc := range []struct {
		err    error
		origin FailureOrigin
	}{
		{&net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}, OriginNetwork},
		{context.DeadlineExceeded, OriginTimeout},
		{errors.New("invalid frame"), OriginUpstreamProtocol},
	} {
		got := ReadFailed(result, tc.err, context.Background())
		if got.StreakDisposition != StreakUpstreamFailure || got.FailureOrigin != tc.origin {
			t.Fatalf("read outcome: %+v", got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := ReadFailed(result, context.Canceled, ctx)
	if got.StreakDisposition != StreakNeutral || got.FailureOrigin != OriginClientCancel {
		t.Fatalf("canceled read: %+v", got)
	}
	definitive := UpstreamFailed(AttemptResult{Failure: FailureCanceled}, OriginUpstreamResponse)
	if NormalizeOutcome(definitive) != definitive {
		t.Fatal("confirmed failure overwritten")
	}
}

type failedDialer struct{ err error }

func (d failedDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, d.err
}

func TestCapturedNetworkFailureSurvivesLaterCallerCancellation(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		cause := errors.New("connection refused")
		want := OriginNetwork
		if timeout {
			cause = context.DeadlineExceeded
			want = OriginTimeout
		}
		stack, err := egress.NewStack(egress.StackOptions{Dialer: failedDialer{&net.OpError{Op: "dial", Net: "tcp", Err: cause}}})
		if err != nil {
			t.Fatal(err)
		}
		defer stack.CloseIdleConnections()
		if err = stack.AddSelfOrigins(context.Background(), &config.Config{SiteBaseURL: "https://127.0.0.1", UserHost: "127.0.0.1", AdminHost: "127.0.0.2", ListenAddr: "127.0.0.1:1"}); err != nil {
			t.Fatal(err)
		}
		client, err := stack.NewClient("http://8.8.4.4/v1")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://8.8.4.4/v1", nil)
		_, err = client.Do(request)
		if err == nil {
			t.Fatal("expected injected dial failure")
		}
		cancel()
		for _, classify := range []func(AttemptResult, error, context.Context) AttemptResult{TransportFailed, ReadFailed} {
			got := classify(AttemptResult{Failure: FailureUpstream}, err, ctx)
			if got.StreakDisposition != StreakUpstreamFailure || got.FailureOrigin != want {
				t.Fatalf("captured failure lost: %+v", got)
			}
		}
	}
}
