// Package httpmw provides the inbound HTTP boundary shared by the two
// stations. It validates the configured Host authorities before any redirect,
// derives forwarding metadata only from explicitly trusted proxy peers, and
// enforces the station path split before delegating to application handlers.
package httpmw

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const (
	maxForwardedForBytes = 4096
	maxForwardedHops     = 32
	maxForwardedProtoLen = 32
	maxRealIPLen         = 128
	maxOriginBytes       = 2048
	maxFetchSiteBytes    = 64
)

var proxyHeaders = [...]string{
	"Forwarded",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Proto",
	"X-Real-IP",
}

// Config contains the values that define the inbound station boundary.
// TrustedProxyCIDRs is copied when New returns, so a live handler does not
// share mutable configuration with its caller.
type Config struct {
	UserHost          string
	AdminHost         string
	SiteBaseURL       string
	TrustedProxyCIDRs []netip.Prefix
	ForceHTTPS        bool
}

type policy struct {
	next       http.Handler
	matcher    host.Matcher
	userOrigin string
	adminHost  string
	forceHTTPS bool
	proxies    []netip.Prefix
}

// New validates the station authorities and returns a handler that wraps next.
// A nil next is replaced with a not-found handler, which keeps the boundary
// safe while future routes are being mounted.
func New(cfg Config, next http.Handler) (http.Handler, error) {
	matcher, err := host.NewMatcher(cfg.UserHost, cfg.AdminHost)
	if err != nil {
		return nil, err
	}

	userOrigin, err := fixedOriginAuthority(cfg.SiteBaseURL)
	if err != nil {
		return nil, fmt.Errorf("site base URL: %w", err)
	}
	userOriginParsed, err := host.ParseConfigured(userOrigin)
	if err != nil || userOriginParsed.Host != matcher.UserHost() {
		return nil, fmt.Errorf("site base URL host must match the configured user host")
	}
	admin, err := host.ParseConfigured(cfg.AdminHost)
	if err != nil {
		return nil, fmt.Errorf("admin host: %w", err)
	}
	if next == nil {
		next = http.NotFoundHandler()
	}

	prefixes := make([]netip.Prefix, len(cfg.TrustedProxyCIDRs))
	copy(prefixes, cfg.TrustedProxyCIDRs)
	return &policy{
		next:       next,
		matcher:    matcher,
		userOrigin: userOrigin,
		adminHost:  admin.Authority,
		forceHTTPS: cfg.ForceHTTPS,
		proxies:    prefixes,
	}, nil
}

func fixedOriginAuthority(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL")
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("scheme must be http or https")
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("must be an origin without credentials, query, or fragment")
	}
	if u.Path != "" && strings.Trim(u.Path, "/") != "" {
		return "", fmt.Errorf("must not contain a path")
	}
	parsed, err := host.ParseConfigured(u.Host)
	if err != nil {
		return "", err
	}
	return parsed.Authority, nil
}

func (p *policy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	station := p.matcher.Match(r.Host)
	info := p.requestInfo(r)

	ctx := host.WithStation(r.Context(), station)
	ctx = withRequestInfo(ctx, info)
	request := r.Clone(ctx)
	// Downstream code uses the derived context values rather than reinterpreting
	// forwarding headers. Removing them also prevents a future handler from
	// accidentally treating an untrusted header as authoritative.
	for _, name := range proxyHeaders {
		request.Header.Del(name)
	}

	setSecurityHeaders(w, info.https)
	if station == host.StationUnknown {
		writeMisdirected(w)
		return
	}

	// Host validation is deliberately complete before this branch. The target
	// authority is selected solely from fixed configuration, never r.Host.
	if p.forceHTTPS && !info.https {
		p.redirectHTTPS(w, request, station)
		return
	}

	if !pathAllowed(station, request.URL.Path) {
		writeNotFound(w)
		return
	}
	if !browserSessionRequestAllowed(request, info) {
		writeCrossOriginRefused(w)
		return
	}
	p.next.ServeHTTP(w, request)
}

func (p *policy) requestInfo(r *http.Request) requestInfo {
	peer, peerOK := parsePeer(r.RemoteAddr)
	peerText := "unknown"
	if peerOK {
		peerText = peer.String()
	}
	trusted := peerOK && p.isTrustedProxy(peer)
	secure := r.TLS != nil
	client := peerText

	if trusted {
		if proto, present, valid := oneHeader(r, "X-Forwarded-Proto", maxForwardedProtoLen); present && valid {
			proto = strings.TrimSpace(proto)
			if strings.EqualFold(proto, "https") {
				secure = true
			}
		}

		if value, present, valid := oneHeader(r, "X-Forwarded-For", maxForwardedForBytes); present {
			if valid {
				if forwarded, ok := parseForwardedFor(value, p.isTrustedProxy); ok {
					client = forwarded.String()
				}
			}
		} else if value, present, valid := oneHeader(r, "X-Real-IP", maxRealIPLen); present && valid {
			if addr, ok := parseForwardedAddress(value); ok {
				client = addr.String()
			}
		}
	}

	return requestInfo{clientIP: client, peerIP: peerText, https: secure, trustedProxy: trusted}
}

type requestInfo struct {
	clientIP     string
	peerIP       string
	https        bool
	trustedProxy bool
}

func parsePeer(raw string) (netip.Addr, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Addr{}, false
	}
	if addr, err := netip.ParseAddr(raw); err == nil {
		return addr.WithZone("").Unmap(), true
	}
	if addrPort, err := netip.ParseAddrPort(raw); err == nil {
		return addrPort.Addr().WithZone("").Unmap(), true
	}
	return netip.Addr{}, false
}

func (p *policy) isTrustedProxy(addr netip.Addr) bool {
	addr = addr.WithZone("").Unmap()
	for _, prefix := range p.proxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func oneHeader(r *http.Request, name string, maxBytes int) (string, bool, bool) {
	values := r.Header.Values(name)
	if len(values) == 0 {
		return "", false, true
	}
	if len(values) != 1 || len(values[0]) > maxBytes || strings.ContainsAny(values[0], "\x00\r\n") {
		return "", true, false
	}
	return values[0], true, true
}

func parseForwardedFor(raw string, trusted func(netip.Addr) bool) (netip.Addr, bool) {
	parts := strings.Split(raw, ",")
	if len(parts) == 0 || len(parts) > maxForwardedHops {
		return netip.Addr{}, false
	}
	addresses := make([]netip.Addr, len(parts))
	for i, part := range parts {
		addr, ok := parseForwardedAddress(part)
		if !ok {
			return netip.Addr{}, false
		}
		addresses[i] = addr
	}

	var leftmost netip.Addr
	for i := len(addresses) - 1; i >= 0; i-- {
		leftmost = addresses[i]
		if !trusted(addresses[i]) {
			return addresses[i], true
		}
	}
	if leftmost.IsValid() {
		return leftmost, true
	}
	return netip.Addr{}, false
}

func parseForwardedAddress(raw string) (netip.Addr, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n\t") {
		return netip.Addr{}, false
	}
	if addr, err := netip.ParseAddr(raw); err == nil {
		if addr.Zone() != "" {
			return netip.Addr{}, false
		}
		return addr.Unmap(), true
	}
	if addrPort, err := netip.ParseAddrPort(raw); err == nil {
		return addrPort.Addr().WithZone("").Unmap(), true
	}
	return netip.Addr{}, false
}

func setSecurityHeaders(w http.ResponseWriter, secure bool) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	if secure {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	} else {
		w.Header().Del("Strict-Transport-Security")
	}
}

func (p *policy) redirectHTTPS(w http.ResponseWriter, r *http.Request, station host.Station) {
	authority := p.userOrigin
	if station == host.StationAdmin {
		authority = p.adminHost
	}
	target := (&url.URL{
		Scheme:   "https",
		Host:     authority,
		Path:     r.URL.Path,
		RawPath:  r.URL.RawPath,
		RawQuery: r.URL.RawQuery,
	}).String()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

func pathAllowed(station host.Station, path string) bool {
	if station == host.StationAdmin {
		return !isUserAPIPath(path)
	}
	if station == host.StationUser {
		return !isAdminAPIPath(path)
	}
	return false
}

// browserSessionRequestAllowed protects cookie-authenticated browser APIs on
// the two sibling station hosts. SameSite cookies are site-scoped rather than
// origin-scoped, so a malicious sibling origin can otherwise submit a form to
// the other station with its host-only cookie attached. Bearer-authenticated
// /v1 is deliberately excluded. Non-browser requests without cookies remain
// compatible when they omit browser-only metadata.
func browserSessionRequestAllowed(r *http.Request, info requestInfo) bool {
	if r == nil || isSafeMethod(r.Method) || !isBrowserSessionAPIPath(r.URL.Path) {
		return true
	}

	origin, originPresent, originValid := oneHeader(r, "Origin", maxOriginBytes)
	if originPresent && (!originValid || !sameRequestOrigin(origin, r.Host, info.https)) {
		return false
	}
	fetchSite, fetchPresent, fetchValid := oneHeader(r, "Sec-Fetch-Site", maxFetchSiteBytes)
	if fetchPresent && (!fetchValid || strings.TrimSpace(fetchSite) != "same-origin") {
		return false
	}
	// Modern browsers send Origin on unsafe methods and/or Fetch Metadata.
	// Requiring at least one signal when a cookie is present also closes the
	// gap for older user agents instead of treating missing metadata as proof
	// of same-origin. Cookie-less CLI login requests remain usable.
	if len(r.Header.Values("Cookie")) > 0 && !originPresent && !fetchPresent {
		return false
	}
	return true
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func isBrowserSessionAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/") ||
		path == "/admin/api" || strings.HasPrefix(path, "/admin/api/")
}

func sameRequestOrigin(rawOrigin, requestHost string, secure bool) bool {
	if rawOrigin == "" || rawOrigin != strings.TrimSpace(rawOrigin) || rawOrigin == "null" {
		return false
	}
	origin, err := url.Parse(rawOrigin)
	if err != nil || origin.Opaque != "" || origin.Host == "" || origin.User != nil ||
		origin.Path != "" || origin.RawPath != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" {
		return false
	}
	scheme := strings.ToLower(origin.Scheme)
	if (secure && scheme != "https") || (!secure && scheme != "http") {
		return false
	}
	// An Origin header is serialized as exactly scheme://authority. This also
	// rejects bare trailing '?'/'#' forms that net/url otherwise normalizes.
	if rawOrigin != scheme+"://"+origin.Host {
		return false
	}
	originAuthority, err := host.Parse(origin.Host)
	if err != nil {
		return false
	}
	requestAuthority, err := host.Parse(requestHost)
	if err != nil || originAuthority.Host != requestAuthority.Host {
		return false
	}
	return effectiveOriginPort(originAuthority, scheme) == effectiveOriginPort(requestAuthority, scheme)
}

func effectiveOriginPort(authority host.Parsed, scheme string) string {
	if authority.HasPort {
		return authority.Port
	}
	if scheme == "https" {
		return "443"
	}
	return "80"
}

func isUserAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/") || path == "/v1" || strings.HasPrefix(path, "/v1/")
}

func isAdminAPIPath(path string) bool {
	return path == "/admin/api" || strings.HasPrefix(path, "/admin/api/")
}

func writeMisdirected(w http.ResponseWriter) {
	httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "misdirected request"))
}

func writeCrossOriginRefused(w http.ResponseWriter) {
	httperr.WriteError(w, httperr.New(httperr.CodeForbidden, "cross-origin request refused"))
}

// API wraps an API route tree. It applies the mandatory no-store policy and
// converts a standard-library 404/405 fallback into the stable JSON envelope
// without touching JSON errors or event-stream responses written by a real
// route.
func API(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &apiResponseWriter{ResponseWriter: w}
		defer func() {
			// API responses are never cacheable, including a handler that only
			// mutates headers and returns without calling WriteHeader.
			rw.Header().Set("Cache-Control", "no-store")
		}()
		next.ServeHTTP(rw, r)
	})
}

type apiResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
	replaced    bool
}

func (w *apiResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.Header().Set("Cache-Control", "no-store")
	contentType := strings.ToLower(w.Header().Get("Content-Type"))
	if (status == http.StatusNotFound || status == http.StatusMethodNotAllowed) &&
		!strings.HasPrefix(contentType, "application/json") {
		w.Header().Del("Content-Length")
		code := httperr.CodeNotFound
		if status == http.StatusMethodNotAllowed {
			code = httperr.CodeMethodNotAllowed
		}
		httperr.WriteError(w.ResponseWriter, httperr.New(code, strings.ToLower(http.StatusText(status))))
		w.replaced = true
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *apiResponseWriter) Write(p []byte) (int, error) {
	if w.replaced {
		return len(p), nil
	}
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *apiResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.NewResponseController reach optional interfaces on the
// underlying writer while retaining the JSON fallback behavior above.
func (w *apiResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func writeNotFound(w http.ResponseWriter) {
	httperr.WriteError(w, httperr.New(httperr.CodeNotFound, "not found"))
}

// ClientIP returns the effective client IP selected by the edge. It returns
// the direct peer when the request was not wrapped, and "unknown" when the
// peer address is not an IP address.
func ClientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	if info, ok := requestInfoFromContext(r.Context()); ok {
		return info.clientIP
	}
	if peer, ok := parsePeer(r.RemoteAddr); ok {
		return peer.String()
	}
	return "unknown"
}

// PeerIP returns the immediate TCP peer selected by the HTTP server.
func PeerIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	if info, ok := requestInfoFromContext(r.Context()); ok {
		return info.peerIP
	}
	if peer, ok := parsePeer(r.RemoteAddr); ok {
		return peer.String()
	}
	return "unknown"
}

// RequestIsHTTPS reports the protocol decision made at the edge. Forwarded
// protocol is considered only for a trusted peer; otherwise only TLS counts.
func RequestIsHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if info, ok := requestInfoFromContext(r.Context()); ok {
		return info.https
	}
	return r.TLS != nil
}

// TrustedProxy reports whether the immediate peer was in the configured
// trusted proxy set.
func TrustedProxy(r *http.Request) bool {
	if r == nil {
		return false
	}
	if info, ok := requestInfoFromContext(r.Context()); ok {
		return info.trustedProxy
	}
	return false
}

// StationOf reports the station chosen by the host boundary.
func StationOf(r *http.Request) host.Station {
	if r == nil {
		return host.StationUnknown
	}
	if station, ok := host.StationFromContext(r.Context()); ok {
		return station
	}
	return host.StationUnknown
}

type requestInfoContextKey struct{}

func withRequestInfo(ctx context.Context, info requestInfo) context.Context {
	return context.WithValue(ctx, requestInfoContextKey{}, info)
}

func requestInfoFromContext(ctx context.Context) (requestInfo, bool) {
	if ctx == nil {
		return requestInfo{}, false
	}
	info, ok := ctx.Value(requestInfoContextKey{}).(requestInfo)
	return info, ok
}
