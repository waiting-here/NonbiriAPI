package egress

import (
	"errors"
	"net/http"
	"strconv"
	"time"
)

// ImageProfile selects one finite, non-replayable image transport profile.
// These clients share the ordinary stack's DNS policy and concurrency gate.
type ImageProfile uint8

const (
	ImageJSON ImageProfile = iota + 1
	ImageDownload
	ImageMetadata
)

// NewImageClient grants only the response capacity needed by the image
// activity. Callers must additionally set the original task deadline on each
// request context; a retry never extends that deadline.
func (s *Stack) NewImageClient(baseURL string, profile ImageProfile) (*Client, error) {
	var timeout time.Duration
	var maxBytes int64
	switch profile {
	case ImageJSON:
		timeout, maxBytes = 24*time.Hour, 96<<20
	case ImageDownload:
		timeout, maxBytes = 2*time.Minute, 32<<20
	case ImageMetadata:
		timeout, maxBytes = time.Minute, 1<<20
	default:
		return nil, errors.New("egress image profile is invalid")
	}
	if s == nil || !s.policy.originsReady() {
		return nil, ErrSelfOriginsNotConfigured
	}
	canonical, err := s.policy.ValidateBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	_, origin, parsed, _, err := canonicalizeBaseURL(canonical, false)
	if err != nil {
		return nil, err
	}
	cacheKey := "image:" + strconv.Itoa(int(profile)) + ":" + origin
	s.clientsMu.Lock()
	httpClient := s.clients[cacheKey]
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = s.policy.dialContext(parsed.Scheme, parsed.Hostname(), parsed.Port())
		transport.DialTLSContext = nil
		transport.ResponseHeaderTimeout = timeout
		transport.TLSHandshakeTimeout = s.tlsHandshakeTimeout
		transport.MaxResponseHeaderBytes = s.maxResponseHeaderBytes
		// A fresh HTTP/1 connection cannot transparently replay a POST after
		// response loss. No pooled connection or HTTP/2 retry rail is used.
		transport.DisableKeepAlives = true
		transport.ForceAttemptHTTP2 = false
		transport.Protocols = new(http.Protocols)
		transport.Protocols.SetHTTP1(true)
		httpClient = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrRedirectBlocked }}
		s.clients[cacheKey] = httpClient
	}
	s.clientsMu.Unlock()
	return &Client{baseURL: canonical, origin: origin, originScheme: parsed.Scheme, originHost: parsed.Host,
		httpClient: httpClient, gate: s.gate, timeout: timeout, maxResponseBytes: maxBytes, disableReplay: true}, nil
}
