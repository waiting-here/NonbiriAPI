package egress

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/config"
)

const (
	workerTargetHeader   = "X-Nonbiri-Proxy-Target"
	workerTokenHeader    = "X-Nonbiri-Proxy-Token"
	workerErrorHeader    = "X-Nonbiri-Proxy-Error"
	workerResponseHeader = "X-Nonbiri-Proxy-Response"
)

type cloudflareWorker struct {
	target        *url.URL
	rawURL, token string
}

func buildCloudflareWorker(policy *EgressPolicy, entry config.CloudflareWorker) (*cloudflareWorker, error) {
	if entry.URL() == "" {
		return nil, nil
	}
	if _, err := policy.ValidateBaseURL(entry.URL()); err != nil {
		return nil, errors.New("invalid Cloudflare Worker URL")
	}
	_, _, target, _, err := canonicalizeBaseURL(entry.URL(), false)
	if err != nil || unsafeRoutePath(target.Path) {
		return nil, errors.New("invalid Cloudflare Worker URL")
	}
	return &cloudflareWorker{target: target, rawURL: entry.URL(), token: entry.Token()}, nil
}

func removeWorkerHeaders(headers http.Header) {
	for name := range headers {
		if strings.HasPrefix(strings.ToLower(name), "x-nonbiri-proxy-") {
			delete(headers, name)
		}
	}
}

func workerCredentialHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "x-api-key", "x-goog-api-key":
		return true
	}
	return false
}

func (w *cloudflareWorker) prepare(outbound *http.Request) (*routeSecrets, error) {
	target := outbound.URL.String()
	if len(target) > 8192 || strings.ContainsAny(target, "\\\r\n\x00") || unsafeRoutePath(outbound.URL.Path) {
		return nil, errors.New("invalid Worker target URL")
	}
	if _, err := netip.ParseAddr(outbound.URL.Hostname()); err == nil {
		return nil, errors.New("Cloudflare Worker requires an upstream DNS hostname")
	}
	switch outbound.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
	default:
		return nil, errors.New("unsupported Worker request method")
	}
	secrets := newRouteSecrets(w.token, w.rawURL, w.target.String())
	for name, values := range outbound.Header {
		if workerCredentialHeader(name) {
			for _, value := range values {
				secrets.add(value)
				if scheme, raw, ok := strings.Cut(value, " "); ok && strings.EqualFold(scheme, "Bearer") {
					secrets.add(raw)
				}
			}
		}
	}
	removeWorkerHeaders(outbound.Header)
	removeCloudflareControls(outbound.Header)
	if outbound.Header == nil {
		outbound.Header = make(http.Header)
	}
	outbound.Header.Set(workerTargetHeader, target)
	outbound.Header.Set(workerTokenHeader, w.token)
	proxy := *w.target
	outbound.URL, outbound.Host = &proxy, proxy.Host
	return secrets, nil
}

func workerFailure(headers http.Header) bool {
	for name := range headers {
		// Unknown values also fail closed: they indicate a proxy protocol
		// mismatch, never an upstream credential or quota response.
		if strings.EqualFold(name, workerErrorHeader) {
			return true
		}
	}
	return false
}

func workerConfirmedResponse(headers http.Header) bool {
	count := 0
	for name, values := range headers {
		if !strings.EqualFold(name, workerResponseHeader) {
			continue
		}
		for _, value := range values {
			if value != "upstream" {
				return false
			}
			count++
		}
	}
	return count == 1
}

func workerTransportFailure(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return &executionError{class: FailureCanceled, cause: context.Canceled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &executionError{class: FailurePlatform, cause: context.DeadlineExceeded}
	}
	return &executionError{class: FailurePlatform, cause: errors.New("Cloudflare Worker transport failed")}
}

type workerResponseBody struct{ io.ReadCloser }

func (b workerResponseBody) Read(dst []byte) (int, error) {
	n, err := b.ReadCloser.Read(dst)
	if err != nil && !errors.Is(err, io.EOF) {
		err = workerTransportFailure(err)
	}
	return n, err
}

func (b workerResponseBody) Close() error { return workerTransportFailure(b.ReadCloser.Close()) }
