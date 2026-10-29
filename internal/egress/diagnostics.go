package egress

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

func diagnosticTrace(ctx context.Context) context.Context {
	if diagnostic.RecorderFrom(ctx) == nil {
		return ctx
	}
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { diagnostic.Phase(ctx, "dns") },
		ConnectStart:      func(string, string) { diagnostic.Phase(ctx, "connect") },
		TLSHandshakeStart: func() { diagnostic.Phase(ctx, "tls") },
		GotConn:           func(httptrace.GotConnInfo) { diagnostic.Phase(ctx, "send_request") },
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				diagnostic.Phase(ctx, "wait_headers")
			}
		},
		GotFirstResponseByte: func() { diagnostic.Phase(ctx, "response_headers") },
	})
}

func recordEgressFailure(ctx context.Context, err error) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, ErrRedirectBlocked):
		diagnostic.RecordKnownError(ctx, "redirect", "policy", "redirect_blocked", "Redirects are disabled; configure the final endpoint URL", err)
	case errors.Is(err, ErrSelfOriginsNotConfigured):
		diagnostic.RecordKnownError(ctx, "policy", "platform", "egress_not_ready", "The outbound policy is not ready", err)
	case errors.Is(err, ErrInvalidBaseURL):
		diagnostic.RecordKnownError(ctx, "policy", "policy", "endpoint_invalid", "The configured endpoint URL is invalid", err)
	case errors.Is(err, errBlockedAddress):
		diagnostic.RecordKnownError(ctx, "policy", "policy", "destination_blocked", "The resolved destination is blocked by outbound policy", err)
	case errors.Is(err, errNoAddresses):
		diagnostic.RecordKnownError(ctx, "dns", "network", "dns_empty", "DNS resolution returned no usable addresses", err)
	default:
		diagnostic.RecordError(ctx, "", err)
	}
}

func observeResponse(ctx context.Context, request *http.Request, response *http.Response) {
	if response == nil || diagnostic.RecorderFrom(ctx) == nil {
		return
	}
	var ids []diagnostic.Correlation
	for _, name := range []string{"x-request-id", "request-id", "x-correlation-id", "traceparent", "cf-ray"} {
		value := response.Header.Get(name)
		if !safeCorrelationValue(value) || reflectsRequestSecret(request, value) {
			continue
		}
		ids = append(ids, diagnostic.Correlation{Source: "upstream_response_header", Name: name, Value: value})
	}
	diagnostic.RecordResponse(ctx, response.StatusCode, ids)
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	switch media {
	case "application/json", "text/event-stream", "text/plain", "text/html", "application/octet-stream":
	default:
		if media != "" {
			media = "other"
		}
	}
	encoding := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding")))
	switch encoding {
	case "", "identity", "gzip", "deflate", "br", "zstd":
	default:
		encoding = "other"
	}
	var retry *int64
	if raw := response.Header.Get("Retry-After"); len(raw) > 0 && len(raw) <= 128 {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 && seconds <= 86400*7 {
			retry = &seconds
		} else if at, err := http.ParseTime(raw); err == nil {
			seconds := int64(time.Until(at).Seconds())
			if seconds >= 0 && seconds <= 86400*7 {
				retry = &seconds
			}
		}
	}
	diagnostic.ResponseMetadata(ctx, media, encoding, retry)
}
func safeCorrelationValue(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:/", r)) {
			return false
		}
	}
	return true
}
func reflectsRequestSecret(request *http.Request, value string) bool {
	if request == nil {
		return true
	}
	check := func(secret string) bool {
		if len(secret) < 4 {
			return false
		}
		return strings.Contains(value, secret) || strings.Contains(value, base64.StdEncoding.EncodeToString([]byte(secret))) || strings.Contains(value, base64.RawURLEncoding.EncodeToString([]byte(secret))) || strings.Contains(value, url.QueryEscape(secret))
	}
	for _, values := range request.Header {
		for _, v := range values {
			if check(v) {
				return true
			}
			for _, part := range strings.Fields(v) {
				if check(part) {
					return true
				}
			}
		}
	}
	if request.URL != nil {
		for _, values := range request.URL.Query() {
			for _, v := range values {
				if check(v) {
					return true
				}
			}
		}
	}
	return false
}

type diagnosticBody struct {
	io.ReadCloser
	ctx context.Context
}

func (b diagnosticBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	diagnostic.RecordRead(b.ctx, n, err)
	observeReadLimit(b.ctx, err)
	return n, err
}
func observeReadLimit(ctx context.Context, err error) {
	var total *ResponseTooLargeError
	var line *SSELineTooLargeError
	var event *SSEEventTooLargeError
	switch {
	case errors.As(err, &total):
		diagnostic.RecordLimit(ctx, "response_bytes", total.Limit, err)
	case errors.As(err, &line):
		diagnostic.RecordLimit(ctx, "sse_line_bytes", int64(line.Limit), err)
	case errors.As(err, &event):
		diagnostic.RecordLimit(ctx, "sse_event_bytes", int64(event.Limit), err)
	}
}
