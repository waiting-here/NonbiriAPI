package gemini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

const regionEnvelope = `{"error":{"code":400,"status":"FAILED_PRECONDITION","message":"User location is not supported for the API use."}}`
const invalidKeyEnvelope = `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"API key not valid. Please pass a valid API key.","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`

func TestDiscoverySafeErrorsPreserveOnlyPrivateRawDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		body, media string
		want        contract.DiscoveryFailureKind
	}{
		{"region", 400, regionEnvelope, "application/json", contract.DiscoveryFailureRegion},
		{"invalid key", 400, invalidKeyEnvelope, "application/json", contract.DiscoveryFailureAuth},
		{"unauthorized", 401, "secret-value at 203.0.113.12", "text/plain", contract.DiscoveryFailureAuth},
		{"forbidden", 403, "denied", "text/plain", contract.DiscoveryFailureAuth},
		{"quota", 429, "limited", "text/plain", contract.DiscoveryFailureRateLimit},
		{"server", 503, "unavailable", "text/plain", contract.DiscoveryFailureUnavailable},
		{"gateway", 502, "<html>temporary error</html>", "text/html", contract.DiscoveryFailureUnavailable},
		{"unknown", 400, `{"error":{"code":400,"status":"FAILED_PRECONDITION","message":"unknown precondition"}}`, "application/json", contract.DiscoveryFailureProtocol},
		{"plain region", 400, "User location is not supported for the API use.", "text/plain", contract.DiscoveryFailureProtocol},
		{"wrong media", 400, regionEnvelope, "text/html", contract.DiscoveryFailureProtocol},
		{"reflected secret", 400, strings.TrimSuffix(regionEnvelope, "}") + `,"reflected":"secret-value"}`, "application/json", contract.DiscoveryFailureProtocol},
		{"duplicate", 400, strings.Replace(regionEnvelope, `"code":400`, `"code":400,"code":400`, 1), "application/json", contract.DiscoveryFailureProtocol},
		{"html", 400, "<script>alert('secret-value')</script>", "text/html", contract.DiscoveryFailureProtocol},
		{"wrong key domain", 400, strings.Replace(invalidKeyEnvelope, `"domain":"googleapis.com"`, `"domain":"untrusted.example"`, 1), "application/json", contract.DiscoveryFailureProtocol},
		{"too large", 400, strings.Repeat("x", upstreamerror.MaxRawBodyBytes+20), "application/json", contract.DiscoveryFailureProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			var captured []byte
			var truncated bool
			ctx := upstreamerror.WithCapture(context.Background(), func(_ context.Context, event upstreamerror.Event) {
				captured = append([]byte(nil), event.Bytes()...)
				truncated = event.Truncated()
				if event.Status() != test.status {
					t.Fatal("lost HTTP status")
				}
			})
			var request *http.Request
			outbound := discoveryBackend{do: func(r *http.Request) (*http.Response, error) {
				request = r
				return &http.Response{StatusCode: test.status, Header: http.Header{"Content-Type": {test.media}}, Body: io.NopCloser(strings.NewReader(test.body)), Request: r}, nil
			}}
			credential := contract.NewShortLivedSecret([]byte("secret-value"), nil)
			result := (ModelDiscoverer{}).Discover(ctx, contract.DiscoveryInput{Backend: outbound, Target: contract.NewTarget(contract.TypeGoogleGemini, "https://provider.example/v1beta", ""), Credential: credential})
			if result.Failure != test.want || result.UpstreamStatus != test.status || !result.ResponseReceived || len(result.Models) != 0 {
				t.Fatalf("wrong safe result %+v", result)
			}
			if request.Header.Get("X-Goog-Api-Key") != "" {
				t.Fatal("request retained credential")
			}
			if _, _, ok := credential.Take(); ok {
				t.Fatal("credential remained readable")
			}
			if string(captured) != test.body[:min(len(test.body), upstreamerror.MaxRawBodyBytes)] || truncated != (len(test.body) > upstreamerror.MaxRawBodyBytes) {
				t.Fatal("private diagnostic not captured exactly")
			}
			if strings.Contains(fmt.Sprint(result), "secret-value") || strings.Contains(result.Diagnostic, "203.0.113.") || result.Diagnostic != "model discovery failed" {
				t.Fatal("public result contains upstream content")
			}
		})
	}
}

func TestDiscoveryCancellationAndTimeoutRemainDistinct(t *testing.T) {
	for _, want := range []contract.DiscoveryFailureKind{contract.DiscoveryFailureInterrupted, contract.DiscoveryFailureTimeout, contract.DiscoveryFailureTransport} {
		ctx, cancel := context.WithCancel(context.Background())
		if want == contract.DiscoveryFailureTimeout {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		}
		outbound := discoveryBackend{do: func(*http.Request) (*http.Response, error) {
			if want == contract.DiscoveryFailureInterrupted {
				cancel()
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("synthetic transport failure")
		}}
		result := (ModelDiscoverer{}).Discover(ctx, contract.DiscoveryInput{Backend: outbound, Target: contract.NewTarget(contract.TypeGoogleGemini, "https://provider.example/v1beta", ""), Credential: contract.NewShortLivedSecret([]byte("secret-value"), nil)})
		cancel()
		if result.Failure != want || result.ResponseReceived {
			t.Fatalf("wrong cancellation/transport result %+v", result)
		}
	}
}
