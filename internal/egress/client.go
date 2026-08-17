package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"nonbiriapi/internal/config"
)

const (
	DefaultDialTimeout                  = 10 * time.Second
	DefaultRequestTimeout               = 5 * time.Minute
	DefaultResponseHeaderTimeout        = 30 * time.Second
	DefaultTLSHandshakeTimeout          = 10 * time.Second
	DefaultMaxResponseBytes       int64 = 32 << 20
	DefaultMaxResponseHeaderBytes int64 = 1 << 20
)

var (
	// ErrRedirectBlocked is returned without echoing the untrusted Location
	// value. Connectors must be configured with the final upstream URL.
	ErrRedirectBlocked = errors.New("egress redirects are disabled")
	// ErrSelfOriginsNotConfigured prevents a connector from dialing before the
	// process's own origins have been registered.
	ErrSelfOriginsNotConfigured = errors.New("egress self origins are not configured")
)

// StackOptions configure the shared outbound boundary. Zero values select
// finite defaults. Resolver and Dialer are dependency-injection points for
// deterministic tests; production uses net.DefaultResolver and net.Dialer.
type StackOptions struct {
	AllowedOrigins []string

	RequestTimeout         time.Duration
	DialTimeout            time.Duration
	ResponseHeaderTimeout  time.Duration
	TLSHandshakeTimeout    time.Duration
	MaxResponseBytes       int64
	MaxResponseHeaderBytes int64
	Concurrency            ConcurrencyLimits

	Resolver IPResolver
	Dialer   ContextDialer
}

// Stack owns one policy, one concurrency gate, and a transport cache shared by
// every connector. Keeping these resources behind one object prevents a
// per-caller gate from silently weakening process-wide limits.
type Stack struct {
	policy *EgressPolicy
	gate   *Gate

	requestTimeout         time.Duration
	responseHeaderTimeout  time.Duration
	tlsHandshakeTimeout    time.Duration
	maxResponseBytes       int64
	maxResponseHeaderBytes int64

	clientsMu sync.Mutex
	clients   map[string]*http.Client
}

// NewStack builds a shared outbound boundary. AddSelfOrigins must succeed
// before NewClient can return a dialing client.
func NewStack(options StackOptions) (*Stack, error) {
	if options.RequestTimeout < 0 || options.DialTimeout < 0 || options.ResponseHeaderTimeout < 0 || options.TLSHandshakeTimeout < 0 {
		return nil, errors.New("egress timeouts must not be negative")
	}
	if options.MaxResponseBytes < 0 || options.MaxResponseHeaderBytes < 0 {
		return nil, errors.New("egress response limits must not be negative")
	}
	if options.RequestTimeout == 0 {
		options.RequestTimeout = DefaultRequestTimeout
	}
	if options.DialTimeout == 0 {
		options.DialTimeout = DefaultDialTimeout
	}
	if options.ResponseHeaderTimeout == 0 {
		options.ResponseHeaderTimeout = DefaultResponseHeaderTimeout
	}
	if options.TLSHandshakeTimeout == 0 {
		options.TLSHandshakeTimeout = DefaultTLSHandshakeTimeout
	}
	if options.MaxResponseBytes == 0 {
		options.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if options.MaxResponseHeaderBytes == 0 {
		options.MaxResponseHeaderBytes = DefaultMaxResponseHeaderBytes
	}
	if options.MaxResponseBytes == int64(^uint64(0)>>1) {
		return nil, errors.New("egress response limit is too large")
	}
	if options.Concurrency == (ConcurrencyLimits{}) {
		options.Concurrency = DefaultConcurrencyLimits()
	}

	policy, err := NewEgressPolicy(options.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	if options.Resolver != nil {
		policy.resolver = options.Resolver
	}
	if options.Dialer != nil {
		policy.dialer = options.Dialer
	} else {
		policy.dialer = &netDialer{
			timeout: options.DialTimeout,
		}
	}

	gate, err := NewGate(options.Concurrency)
	if err != nil {
		return nil, err
	}
	return &Stack{
		policy:                 policy,
		gate:                   gate,
		requestTimeout:         options.RequestTimeout,
		responseHeaderTimeout:  options.ResponseHeaderTimeout,
		tlsHandshakeTimeout:    options.TLSHandshakeTimeout,
		maxResponseBytes:       options.MaxResponseBytes,
		maxResponseHeaderBytes: options.MaxResponseHeaderBytes,
		clients:                make(map[string]*http.Client),
	}, nil
}

// netDialer keeps net.Dialer construction private while allowing a compact
// immutable ContextDialer in Stack.
type netDialer struct {
	timeout time.Duration
}

func (d *netDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: d.timeout, KeepAlive: 30 * time.Second}
	return dialer.DialContext(ctx, network, address)
}

// AddSelfOrigins registers startup configuration with the shared policy.
func (s *Stack) AddSelfOrigins(ctx context.Context, cfg *config.Config) error {
	return s.policy.AddSelfOrigins(ctx, cfg)
}

// ValidateBaseURL validates and canonicalizes an endpoint for persistence.
func (s *Stack) ValidateBaseURL(raw string) (string, error) {
	return s.policy.ValidateBaseURL(raw)
}

// SetConcurrencyLimits applies runtime site settings to the existing shared
// counters. The Stack and Gate must not be replaced when settings change.
func (s *Stack) SetConcurrencyLimits(limits ConcurrencyLimits) error {
	return s.gate.SetLimits(limits)
}

// ConcurrencyLimits returns a consistent settings snapshot.
func (s *Stack) ConcurrencyLimits() ConcurrencyLimits { return s.gate.Limits() }

// MaxResponseBytes is the cumulative response ceiling enforced by every
// Client. Streaming parsers may use the same value for their own accounting.
func (s *Stack) MaxResponseBytes() int64 { return s.maxResponseBytes }

// NewClient returns a client tied to one canonical base URL. Transports are
// shared by origin, while concurrency remains keyed by the complete canonical
// base URL so path and trailing-slash differences stay distinct.
func (s *Stack) NewClient(baseURL string) (*Client, error) {
	if !s.policy.originsReady() {
		return nil, ErrSelfOriginsNotConfigured
	}
	canonical, err := s.policy.ValidateBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	_, origin, parsed, err := canonicalizeBaseURL(canonical, false)
	if err != nil {
		return nil, err
	}

	s.clientsMu.Lock()
	httpClient := s.clients[origin]
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		// A process environment proxy would perform its own DNS lookup and
		// bypass IP pinning, so protected egress is always direct.
		transport.Proxy = nil
		transport.DialContext = s.policy.dialContext(parsed.Scheme, parsed.Hostname(), parsed.Port())
		transport.DialTLSContext = nil
		transport.ResponseHeaderTimeout = s.responseHeaderTimeout
		transport.TLSHandshakeTimeout = s.tlsHandshakeTimeout
		transport.MaxResponseHeaderBytes = s.maxResponseHeaderBytes
		httpClient = &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return ErrRedirectBlocked
			},
		}
		s.clients[origin] = httpClient
	}
	s.clientsMu.Unlock()

	return &Client{
		baseURL:          canonical,
		origin:           origin,
		originScheme:     parsed.Scheme,
		originHost:       parsed.Host,
		httpClient:       httpClient,
		gate:             s.gate,
		timeout:          s.requestTimeout,
		maxResponseBytes: s.maxResponseBytes,
	}, nil
}

// CloseIdleConnections closes pooled outbound connections, for example during
// process shutdown. Active responses remain governed by their contexts.
func (s *Stack) CloseIdleConnections() {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for _, client := range s.clients {
		client.CloseIdleConnections()
	}
}

// Client performs requests through the shared policy and gate. Its underlying
// http.Client and Transport are intentionally not exposed.
type Client struct {
	baseURL          string
	origin           string
	originScheme     string
	originHost       string
	httpClient       *http.Client
	gate             *Gate
	timeout          time.Duration
	maxResponseBytes int64
}

// BaseURL returns the canonical endpoint base URL used as the concurrency key.
func (c *Client) BaseURL() string { return c.baseURL }

// Do validates that req still targets this client's exact origin, waits for
// both concurrency slots, applies the request timeout, and returns a response
// whose decompressed body is bounded. The slots are released on request error,
// body EOF, body Close, cancellation, or timeout.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("egress request is nil")
	}
	if req.Body == nil {
		req.Body = http.NoBody
	}
	closeBodyOnError := func(err error) (*http.Response, error) {
		_ = req.Body.Close()
		return nil, err
	}
	if req.URL == nil || req.URL.Opaque != "" || req.URL.Scheme == "" || req.URL.Host == "" {
		return closeBodyOnError(errors.New("egress request must use an absolute HTTP(S) URL"))
	}
	if req.URL.User != nil || req.URL.Fragment != "" {
		return closeBodyOnError(errors.New("egress request credentials and fragments are not allowed"))
	}
	requestOrigin, err := canonicalRequestOrigin(req.URL)
	if err != nil {
		return closeBodyOnError(err)
	}
	if requestOrigin != c.origin {
		return closeBodyOnError(errors.New("egress request origin does not match its endpoint client"))
	}
	if req.Host != "" {
		hostOrigin, hostErr := canonicalRequestOrigin(&url.URL{Scheme: req.URL.Scheme, Host: req.Host})
		if hostErr != nil || hostOrigin != c.origin {
			return closeBodyOnError(errors.New("egress custom Host headers are not allowed"))
		}
	}

	ctx, cancel := context.WithTimeout(req.Context(), c.timeout)
	permit, err := c.gate.Acquire(ctx, c.baseURL)
	if err != nil {
		cancel()
		return closeBodyOnError(err)
	}

	outbound := req.Clone(ctx)
	outbound.URL.Scheme = c.originScheme
	outbound.URL.Host = c.originHost
	outbound.Host = c.originHost
	resp, err := c.httpClient.Do(outbound)
	if err != nil {
		permit.Release()
		cancel()
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if errors.Is(err, ErrRedirectBlocked) {
			return nil, ErrRedirectBlocked
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, newBoundedError("egress request failed", unwrapURLError(err))
	}

	managed := newManagedResponseBody(resp.Body, c.maxResponseBytes, ctx, cancel, permit.Release)
	resp.Body = managed
	return resp, nil
}

func canonicalRequestOrigin(u *url.URL) (string, error) {
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("egress request scheme must be http or https")
	}
	host, err := canonicalHost(u.Hostname())
	if err != nil {
		return "", err
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("egress request port is invalid")
	}
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	} else {
		port, err = parsePort(port)
		if err != nil {
			return "", errors.New("egress request port is invalid")
		}
	}
	return originKey(scheme, host, port), nil
}

// ResponseTooLargeError reports that a decompressed response or cumulative
// stream crossed its byte ceiling.
type ResponseTooLargeError struct {
	Limit int64
}

func (e *ResponseTooLargeError) Error() string {
	if e == nil {
		return "egress response exceeds its byte limit"
	}
	return fmt.Sprintf("egress response exceeds the %d-byte limit", e.Limit)
}

type managedResponseBody struct {
	body      io.ReadCloser
	remaining int64
	limit     int64
	cancel    context.CancelFunc
	release   func()

	finishOnce sync.Once
	done       chan struct{}
	closeErr   error
}

func newManagedResponseBody(body io.ReadCloser, limit int64, ctx context.Context, cancel context.CancelFunc, release func()) *managedResponseBody {
	if body == nil {
		body = io.NopCloser(strings.NewReader(""))
	}
	managed := &managedResponseBody{
		body:      body,
		remaining: limit,
		limit:     limit,
		cancel:    cancel,
		release:   release,
		done:      make(chan struct{}),
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = managed.Close()
		case <-managed.done:
		}
	}()
	return managed
}

func (b *managedResponseBody) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if b.remaining == 0 {
		var probe [1]byte
		n, err := b.body.Read(probe[:])
		if n > 0 {
			tooLarge := &ResponseTooLargeError{Limit: b.limit}
			b.finish()
			return 0, tooLarge
		}
		if err != nil {
			b.finish()
		}
		return 0, err
	}

	readSize := int64(len(dst))
	if readSize > b.remaining+1 {
		readSize = b.remaining + 1
	}
	n, err := b.body.Read(dst[:int(readSize)])
	if int64(n) > b.remaining {
		allowed := int(b.remaining)
		b.remaining = 0
		tooLarge := &ResponseTooLargeError{Limit: b.limit}
		b.finish()
		return allowed, tooLarge
	}
	b.remaining -= int64(n)
	if err != nil {
		b.finish()
	}
	return n, err
}

func (b *managedResponseBody) Close() error {
	b.finish()
	return b.closeErr
}

func (b *managedResponseBody) finish() {
	b.finishOnce.Do(func() {
		b.closeErr = b.body.Close()
		b.cancel()
		b.release()
		close(b.done)
	})
}

func unwrapURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}

type boundedError struct {
	message string
	cause   error
}

func newBoundedError(prefix string, cause error) error {
	if cause == nil {
		return errors.New(prefix)
	}
	text := safeDiagnostic(cause.Error(), 4096)
	if text == "" {
		text = "unknown error"
	}
	return &boundedError{message: prefix + ": " + text, cause: cause}
}

func (e *boundedError) Error() string { return e.message }
func (e *boundedError) Unwrap() error { return e.cause }

func safeDiagnostic(value string, maxRunes int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	var b strings.Builder
	count := 0
	for _, r := range value {
		if count == maxRunes {
			break
		}
		switch {
		case r == '\t', r == '\n':
			b.WriteRune(r)
		case r == '\r':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			continue
		default:
			b.WriteRune(r)
		}
		count++
	}
	result := b.String()
	if !utf8.ValidString(result) {
		return strings.ToValidUTF8(result, "\uFFFD")
	}
	return result
}
