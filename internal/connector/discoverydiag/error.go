// Package discoverydiag provides fixed, credential-safe model discovery diagnostics.
package discoverydiag

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

// RequestError describes only typed causes. Error strings can contain credentials,
// endpoint addresses or certificate contents and must never enter this diagnostic.
func RequestError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "model discovery request was canceled"
	case errors.Is(err, egress.ErrRedirectBlocked):
		return "model discovery redirect was blocked by the outbound request policy"
	case errors.Is(err, egress.ErrInvalidBaseURL):
		return "model discovery endpoint was rejected by the outbound request policy"
	case errors.Is(err, egress.ErrSelfOriginsNotConfigured):
		return "model discovery outbound request configuration is unavailable"
	}
	// Policy checks may deliberately mask a blocked destination as a dial error.
	if egress.ExecutionFailure(err) == egress.FailurePlatform {
		return "model discovery could not use the configured outbound route"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		if dns.Timeout() {
			return "model discovery DNS lookup timed out"
		}
		return "model discovery DNS lookup failed"
	}
	var verification *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	if errors.As(err, &verification) || errors.As(err, &unknownAuthority) ||
		errors.As(err, &hostname) || errors.As(err, &invalidCertificate) {
		return "model discovery TLS certificate verification failed"
	}
	var record tls.RecordHeaderError
	var alert tls.AlertError
	if errors.As(err, &record) || errors.As(err, &alert) {
		return "model discovery TLS connection failed"
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return "model discovery network request timed out"
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "model discovery connection was refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return "model discovery connection was interrupted"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "model discovery connection closed before a complete response was received"
	}
	var operation *net.OpError
	if errors.As(err, &operation) {
		switch operation.Op {
		case "dial":
			return "model discovery connection could not be established"
		case "read", "write":
			return "model discovery connection was interrupted"
		}
	}
	return "model discovery request failed; no further cause was available"
}
