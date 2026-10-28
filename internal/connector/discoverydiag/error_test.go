package discoverydiag

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

func TestRequestErrorUsesTypedCausesWithoutCopyingPrivateText(t *testing.T) {
	private := "sk-private-secret private-endpoint.example 203.0.113.77"
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"DNS", &net.DNSError{Err: private, Name: private, Server: private}, "model discovery DNS lookup failed"},
		{"DNS timeout", &net.DNSError{Err: private, Name: private, IsTimeout: true}, "model discovery DNS lookup timed out"},
		{"certificate", &tls.CertificateVerificationError{Err: errors.New(private)}, "model discovery TLS certificate verification failed"},
		{"hostname", x509.HostnameError{Host: private}, "model discovery TLS certificate verification failed"},
		{"TLS record", tls.RecordHeaderError{Msg: private}, "model discovery TLS connection failed"},
		{"TLS alert", tls.AlertError(40), "model discovery TLS connection failed"},
		{"network timeout", &net.OpError{Op: "read", Err: context.DeadlineExceeded}, "model discovery network request timed out"},
		{"refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, "model discovery connection was refused"},
		{"dial", &net.OpError{Op: "dial", Err: errors.New(private)}, "model discovery connection could not be established"},
		{"reset", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, "model discovery connection was interrupted"},
		{"closed", io.EOF, "model discovery connection closed before a complete response was received"},
		{"canceled", context.Canceled, "model discovery request was canceled"},
		{"redirect", egress.ErrRedirectBlocked, "model discovery redirect was blocked by the outbound request policy"},
		{"invalid endpoint", &egress.ValidationError{Reason: private}, "model discovery endpoint was rejected by the outbound request policy"},
		{"unknown", errors.New("tls: DNS connection refused " + private), "model discovery request failed; no further cause was available"},
		{"nil", nil, "model discovery request failed; no further cause was available"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Match the nested URL, transport and policy wrappers used in production.
			err := &url.Error{Op: private, URL: "https://" + private, Err: fmt.Errorf("%s: %w", private, tc.err)}
			if got := RequestError(err); got != tc.want {
				t.Fatalf("diagnostic = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestRequestErrorDoesNotAttributeMaskedPolicyRefusalToProvider(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{"https://127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.CloseIdleConnections()
	if err := stack.AddSelfOrigins(ctx, &config.Config{
		SiteBaseURL: "https://127.0.0.1", UserHost: "127.0.0.1", AdminHost: "127.0.0.2", ListenAddr: "127.0.0.1:1",
	}); err != nil {
		t.Fatal(err)
	}
	client, err := stack.NewClient("https://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://127.0.0.1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if response != nil || !errors.Is(err, syscall.ECONNREFUSED) || egress.ExecutionFailure(err) != egress.FailurePlatform {
		t.Fatalf("expected a masked policy refusal: response=%v err=%v", response, err)
	}
	if got := RequestError(err); got != "model discovery could not use the configured outbound route" {
		t.Fatalf("policy refusal was attributed to the provider: %q", got)
	}
}
