// Package egress provides the single outbound network boundary used by all
// upstream connectors. It validates endpoint URLs, pins validated DNS answers
// into direct dials, and prevents requests from reaching non-public or local
// service addresses unless an operator has allowlisted an exact origin.
package egress

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/config"
)

const (
	maxBaseURLBytes          = 4096
	selfResolutionTimeout    = 10 * time.Second
	selfRefusalMinimumDelay  = 150 * time.Millisecond
	selfRefusalDelayVariants = 201 // 150 through 350 milliseconds, inclusive.
)

// ErrInvalidBaseURL identifies endpoint URL validation failures. Validation
// errors contain only controlled reason text and never echo the supplied URL,
// which could contain credentials entered by mistake.
var ErrInvalidBaseURL = errors.New("invalid base URL")

// ValidationError reports a controlled reason for an invalid endpoint URL.
type ValidationError struct {
	Reason string
}

func (e *ValidationError) Error() string {
	if e == nil || e.Reason == "" {
		return ErrInvalidBaseURL.Error()
	}
	return ErrInvalidBaseURL.Error() + ": " + e.Reason
}

func (e *ValidationError) Unwrap() error { return ErrInvalidBaseURL }

func invalidBaseURL(reason string) error { return &ValidationError{Reason: reason} }

// IPResolver is the subset of net.Resolver needed by the policy. A resolver
// returns the complete A and AAAA answer so the policy can reject mixed public
// and blocked answers as one unit.
type IPResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// ContextDialer is the subset of net.Dialer used after DNS validation. The
// address passed to it is always a vetted IP literal, never the original
// hostname.
type ContextDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// EgressPolicy owns immutable operator allowlist entries and the self-origin
// set registered at startup. An allowlist entry is an exact HTTP(S) origin;
// CIDRs, bare IPs, paths, credentials, queries, and fragments are rejected.
type EgressPolicy struct {
	allowedOrigins map[string]struct{}

	mu          sync.RWMutex
	selfIPs     map[netip.Addr]struct{}
	selfOrigins map[string]struct{}
	selfPorts   map[uint16]struct{}
	selfReady   bool

	resolver       IPResolver
	dialer         ContextDialer
	interfaceAddrs func() ([]net.Addr, error)
	refusalDelay   func(context.Context) error
}

// NewEgressPolicy parses an operator allowlist. Public global-unicast origins
// need no entry; an entry is only an explicit exception for a private or local
// upstream deployment. Self-origin protection always takes precedence.
func NewEgressPolicy(entries []string) (*EgressPolicy, error) {
	p := &EgressPolicy{
		allowedOrigins: make(map[string]struct{}),
		selfIPs:        make(map[netip.Addr]struct{}),
		selfOrigins:    make(map[string]struct{}),
		selfPorts:      make(map[uint16]struct{}),
		resolver:       net.DefaultResolver,
		dialer: &net.Dialer{
			Timeout:   DefaultDialTimeout,
			KeepAlive: 30 * time.Second,
		},
		interfaceAddrs: net.InterfaceAddrs,
		refusalDelay:   defaultSelfRefusalDelay,
	}

	for i, entry := range entries {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		_, origin, _, _, err := canonicalizeBaseURL(entry, true)
		if err != nil {
			return nil, fmt.Errorf("invalid egress allowlist entry %d: %w", i+1, err)
		}
		p.allowedOrigins[origin] = struct{}{}
	}
	return p, nil
}

// ValidateBaseURL validates and safely canonicalizes a user-supplied endpoint
// base URL. It deliberately does not resolve DNS or consult the self-origin
// set: save-time behavior for a public self origin and an unrelated public
// origin must be identical, preventing the endpoint form from becoming an IP
// discovery oracle. Every dial performs the authoritative DNS and self check.
func (p *EgressPolicy) ValidateBaseURL(raw string) (string, error) {
	_, origin, parsed, explicitPort, err := canonicalizeBaseURL(raw, false)
	if err != nil {
		return "", err
	}

	_, originAllowed := p.allowedOrigins[origin]
	host := parsed.Hostname()
	if isLocalHostname(host) && !originAllowed {
		return "", invalidBaseURL("local hostnames require an exact-origin allowlist entry")
	}
	if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
		if p.blocked(addr) && !originAllowed {
			return "", invalidBaseURL("non-public IP addresses require an exact-origin allowlist entry")
		}
	}
	return displayBaseURL(parsed, explicitPort), nil
}

// CanonicalEndpointTarget returns the security-normalized target URL and its
// canonical origin. The target always includes the effective port, while the
// origin is exactly scheme + canonical host + effective port; path is excluded
// from the origin. Callers use this pure parser only after ValidateBaseURL has
// admitted a value, so origin and path comparisons share the egress boundary's
// single URL authority without performing DNS or policy checks a second time.
func CanonicalEndpointTarget(raw string) (target string, origin string, err error) {
	target, origin, _, _, err = canonicalizeBaseURL(raw, false)
	return target, origin, err
}

// AddSelfOrigins registers the process's public station names, every A/AAAA
// answer for those names, and the listener's local addresses and port. It must
// succeed before Stack.NewClient can create a dialing client. Hostname origins
// remain protected even when a station is fronted by a CDN or reverse proxy.
func (p *EgressPolicy) AddSelfOrigins(parent context.Context, cfg *config.Config) error {
	if cfg == nil {
		return errors.New("egress self-origin configuration is required")
	}
	if parent == nil {
		return errors.New("egress self-origin context is required")
	}

	listenHost, listenPortText, err := net.SplitHostPort(strings.TrimSpace(cfg.ListenAddr))
	if err != nil {
		return fmt.Errorf("invalid listener address: %w", err)
	}
	listenPortNumber, err := strconv.ParseUint(listenPortText, 10, 16)
	if err != nil || listenPortNumber == 0 {
		return errors.New("invalid listener port")
	}

	ctx, cancel := context.WithTimeout(parent, selfResolutionTimeout)
	defer cancel()

	selfIPs := map[netip.Addr]struct{}{
		netip.MustParseAddr("127.0.0.1"): {},
		netip.MustParseAddr("::1"):       {},
	}
	selfOrigins := make(map[string]struct{})

	var resolutionErrs []error
	addResolvedHost := func(rawHost string) {
		host, _, parseErr := parseConfiguredHost(rawHost)
		if parseErr != nil {
			resolutionErrs = append(resolutionErrs, parseErr)
			return
		}
		if host == "" {
			return
		}
		if resolveErr := p.resolveSelfHost(ctx, host, selfIPs); resolveErr != nil {
			resolutionErrs = append(resolutionErrs, resolveErr)
		}
	}

	userHost, userPort, err := parseConfiguredHost(cfg.UserHost)
	if err != nil || userHost == "" {
		return errors.New("invalid user station host")
	}
	addResolvedHost(cfg.UserHost)
	addDefaultStationOrigins(selfOrigins, userHost, userPort)

	if strings.TrimSpace(cfg.SiteBaseURL) != "" {
		_, siteOrigin, _, _, siteErr := canonicalizeBaseURL(cfg.SiteBaseURL, true)
		if siteErr != nil {
			return fmt.Errorf("invalid user station origin: %w", siteErr)
		}
		selfOrigins[siteOrigin] = struct{}{}
	}

	adminHost, adminPort, err := parseConfiguredHost(cfg.AdminHost)
	if err != nil || adminHost == "" {
		return errors.New("invalid administrator station host")
	}
	addResolvedHost(cfg.AdminHost)
	addDefaultStationOrigins(selfOrigins, adminHost, adminPort)

	listenHost = strings.TrimSpace(strings.Trim(listenHost, "[]"))
	switch {
	case listenHost == "":
		if err := addInterfaceAddresses(p.interfaceAddrs, selfIPs); err != nil {
			resolutionErrs = append(resolutionErrs, err)
		}
	case isLocalHostname(listenHost):
		// Loopback addresses are registered unconditionally above.
	default:
		addr, parseErr := netip.ParseAddr(listenHost)
		if parseErr == nil {
			addr = addr.WithZone("").Unmap()
			if addr.IsUnspecified() {
				if err := addInterfaceAddresses(p.interfaceAddrs, selfIPs); err != nil {
					resolutionErrs = append(resolutionErrs, err)
				}
			} else {
				selfIPs[addr] = struct{}{}
			}
		} else if resolveErr := p.resolveSelfHost(ctx, listenHost, selfIPs); resolveErr != nil {
			resolutionErrs = append(resolutionErrs, resolveErr)
		}
	}

	if len(resolutionErrs) != 0 {
		return fmt.Errorf("register egress self origins: %w", errors.Join(resolutionErrs...))
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("register egress self origins: %w", err)
	}

	p.mu.Lock()
	for addr := range selfIPs {
		p.selfIPs[addr] = struct{}{}
	}
	for origin := range selfOrigins {
		p.selfOrigins[origin] = struct{}{}
	}
	p.selfPorts[uint16(listenPortNumber)] = struct{}{}
	p.selfReady = true
	p.mu.Unlock()
	return nil
}

func (p *EgressPolicy) resolveSelfHost(ctx context.Context, rawHost string, dst map[netip.Addr]struct{}) error {
	host, err := canonicalHost(rawHost)
	if err != nil {
		return errors.New("invalid self hostname")
	}
	if isLocalHostname(host) {
		dst[netip.MustParseAddr("127.0.0.1")] = struct{}{}
		dst[netip.MustParseAddr("::1")] = struct{}{}
		return nil
	}
	if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
		dst[addr.WithZone("").Unmap()] = struct{}{}
		return nil
	}

	addresses, err := p.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return newBoundedError("resolve self hostname", err)
	}
	if len(addresses) == 0 {
		return errors.New("self hostname resolved to no addresses")
	}
	for _, addr := range addresses {
		addr = addr.WithZone("").Unmap()
		if !addr.IsValid() {
			return errors.New("self hostname resolved to an invalid address")
		}
		dst[addr] = struct{}{}
	}
	return nil
}

func addInterfaceAddresses(list func() ([]net.Addr, error), dst map[netip.Addr]struct{}) error {
	addresses, err := list()
	if err != nil {
		return newBoundedError("enumerate listener interfaces", err)
	}
	for _, address := range addresses {
		prefix, parseErr := netip.ParsePrefix(address.String())
		if parseErr != nil {
			continue
		}
		addr := prefix.Addr().WithZone("").Unmap()
		if addr.IsValid() && !addr.IsUnspecified() {
			dst[addr] = struct{}{}
		}
	}
	return nil
}

func addDefaultStationOrigins(dst map[string]struct{}, host, explicitPort string) {
	if explicitPort != "" {
		dst[originKey("http", host, explicitPort)] = struct{}{}
		dst[originKey("https", host, explicitPort)] = struct{}{}
		return
	}
	dst[originKey("http", host, "80")] = struct{}{}
	dst[originKey("https", host, "443")] = struct{}{}
}

func parseConfiguredHost(raw string) (host string, port string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil
	}
	if strings.Contains(raw, "://") {
		_, _, parsed, _, parseErr := canonicalizeBaseURL(raw, true)
		if parseErr != nil {
			return "", "", parseErr
		}
		return parsed.Hostname(), parsed.Port(), nil
	}

	if addr, parseErr := netip.ParseAddr(raw); parseErr == nil {
		if addr.Zone() != "" {
			return "", "", errors.New("zoned addresses are not valid station hosts")
		}
		return addr.Unmap().String(), "", nil
	}
	if strings.HasPrefix(raw, "[") {
		if strings.HasSuffix(raw, "]") {
			addr, parseErr := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"))
			if parseErr != nil || addr.Zone() != "" {
				return "", "", errors.New("invalid bracketed station host")
			}
			return addr.Unmap().String(), "", nil
		}
		h, p, splitErr := net.SplitHostPort(raw)
		if splitErr != nil {
			return "", "", errors.New("invalid station host and port")
		}
		canonical, hostErr := canonicalHost(h)
		if hostErr != nil {
			return "", "", hostErr
		}
		portNumber, portErr := parsePort(p)
		if portErr != nil {
			return "", "", portErr
		}
		return canonical, portNumber, nil
	}
	if strings.Count(raw, ":") == 1 {
		h, p, splitErr := net.SplitHostPort(raw)
		if splitErr != nil {
			return "", "", errors.New("invalid station host and port")
		}
		canonical, hostErr := canonicalHost(h)
		if hostErr != nil {
			return "", "", hostErr
		}
		portNumber, portErr := parsePort(p)
		if portErr != nil {
			return "", "", portErr
		}
		return canonical, portNumber, nil
	}
	canonical, hostErr := canonicalHost(raw)
	return canonical, "", hostErr
}

func canonicalizeBaseURL(raw string, exactOrigin bool) (canonical string, origin string, parsed *url.URL, explicitPort bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil, false, invalidBaseURL("URL is required")
	}
	if len(raw) > maxBaseURLBytes {
		return "", "", nil, false, invalidBaseURL("URL is too long")
	}
	if !utf8.ValidString(raw) || hasUnsafeControl(raw) {
		return "", "", nil, false, invalidBaseURL("URL contains invalid characters")
	}

	u, parseErr := url.Parse(raw)
	if parseErr != nil {
		return "", "", nil, false, invalidBaseURL("malformed URL")
	}
	if u.Opaque != "" {
		return "", "", nil, false, invalidBaseURL("opaque URLs are not supported")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", nil, false, invalidBaseURL("scheme must be http or https")
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", "", nil, false, invalidBaseURL("hostname is required")
	}
	if strings.Contains(u.Host, "\\") {
		return "", "", nil, false, invalidBaseURL("hostname contains an invalid character")
	}
	if exactOrigin && (u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "") {
		return "", "", nil, false, invalidBaseURL("allowlist entries must be exact origins")
	}

	host, hostErr := canonicalHost(u.Hostname())
	if hostErr != nil {
		return "", "", nil, false, hostErr
	}
	port := u.Port()
	explicitPort = port != ""
	if strings.HasSuffix(u.Host, ":") {
		return "", "", nil, false, invalidBaseURL("port is invalid")
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
			return "", "", nil, false, invalidBaseURL("port is invalid")
		}
	}

	escapedPath := u.EscapedPath()
	if exactOrigin {
		if escapedPath != "" && escapedPath != "/" {
			return "", "", nil, false, invalidBaseURL("allowlist entries must not contain a path")
		}
		escapedPath = ""
	} else {
		escapedPath = collapsePathSlashes(escapedPath)
	}

	origin = originKey(scheme, host, port)
	// The internal canonical URL always carries the explicit port. This form
	// drives the dialer (NewClient), the gate concurrency key, and origin
	// matching, so equivalent default ports collapse to one key. The
	// operator-facing display form (which omits an auto-appended default
	// port) is derived by ValidateBaseURL from the explicitPort flag below.
	normalized := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
	if escapedPath != "" {
		path, unescapeErr := url.PathUnescape(escapedPath)
		if unescapeErr != nil {
			return "", "", nil, false, invalidBaseURL("path contains an invalid escape")
		}
		normalized.Path = path
		normalized.RawPath = escapedPath
	}
	return normalized.String(), origin, normalized, explicitPort, nil
}

// displayBaseURL renders the operator-facing canonical form from the internal
// canonical URL. The internal form always carries the explicit port (used by
// the dialer, the gate concurrency key, and origin matching); the display
// form preserves the operator's input by omitting a port they did not type,
// while a port they did type is kept verbatim. IPv6 literals are bracketed
// with or without a port. This is the only place the stored/shown base URL
// diverges from the internal security form.
func displayBaseURL(internal *url.URL, explicitPort bool) string {
	if explicitPort {
		return internal.String()
	}
	host := internal.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	display := &url.URL{Scheme: internal.Scheme, Host: host, Path: internal.Path, RawPath: internal.RawPath}
	return display.String()
}

func canonicalHost(raw string) (string, error) {
	host := strings.ToLower(strings.TrimRight(strings.TrimSpace(raw), "."))
	if host == "" || len(host) > 253 {
		return "", invalidBaseURL("hostname is invalid")
	}
	if strings.ContainsAny(host, "\x00\r\n\t /\\@%[]") {
		return "", invalidBaseURL("hostname contains an invalid character")
	}
	for _, r := range host {
		if r > 127 || r < 0x21 || r == 0x7f {
			return "", invalidBaseURL("hostname must use ASCII punycode form")
		}
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return "", invalidBaseURL("zoned IP addresses are not supported")
		}
		return addr.Unmap().String(), nil
	}
	if strings.Contains(host, ":") {
		return "", invalidBaseURL("IPv6 addresses must use brackets")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", invalidBaseURL("hostname is invalid")
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				continue
			}
			return "", invalidBaseURL("hostname contains an invalid character")
		}
	}
	return host, nil
}

func parsePort(raw string) (string, error) {
	port, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || port == 0 {
		return "", errors.New("port must be an integer between 1 and 65535")
	}
	return strconv.FormatUint(port, 10), nil
}

func collapsePathSlashes(path string) string {
	if !strings.Contains(path, "//") {
		return path
	}
	var b strings.Builder
	b.Grow(len(path))
	previousSlash := false
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			if previousSlash {
				continue
			}
			previousSlash = true
		} else {
			previousSlash = false
		}
		b.WriteByte(path[i])
	}
	return b.String()
}

func hasUnsafeControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func isLocalHostname(host string) bool {
	host = strings.ToLower(strings.TrimRight(host, "."))
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local")
}

func originKey(scheme, host, port string) string {
	return strings.ToLower(scheme) + "://" + net.JoinHostPort(strings.ToLower(strings.TrimRight(host, ".")), port)
}

func (p *EgressPolicy) isSelfTarget(addr netip.Addr, port uint16) bool {
	addr = addr.WithZone("").Unmap()
	p.mu.RLock()
	defer p.mu.RUnlock()
	if _, ok := p.selfIPs[addr]; !ok {
		return false
	}
	_, ok := p.selfPorts[port]
	return ok
}

func (p *EgressPolicy) isSelfOrigin(scheme, host, port string) bool {
	key := originKey(scheme, host, port)
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.selfOrigins[key]
	return ok
}

func (p *EgressPolicy) originsReady() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.selfReady
}

func (p *EgressPolicy) dialContext(scheme, expectedHost, expectedPort string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid outbound dial address")
		}
		canonicalDialHost, err := canonicalHost(host)
		if err != nil || canonicalDialHost != expectedHost || port != expectedPort {
			return nil, errors.New("unexpected outbound dial target")
		}

		var addresses []netip.Addr
		if literal, parseErr := netip.ParseAddr(expectedHost); parseErr == nil {
			addresses = []netip.Addr{literal.Unmap()}
		} else {
			addresses, err = p.resolver.LookupNetIP(ctx, "ip", expectedHost)
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, ctxErr
				}
				return nil, newBoundedError("resolve outbound hostname", err)
			}
		}
		addresses = normalizeAddresses(addresses)
		if len(addresses) == 0 {
			return nil, errors.New("outbound hostname resolved to no addresses")
		}

		originAllowed := false
		if _, ok := p.allowedOrigins[originKey(scheme, expectedHost, expectedPort)]; ok {
			originAllowed = true
		}
		for _, addr := range addresses {
			if p.blocked(addr) && !originAllowed {
				return nil, errors.New("outbound hostname resolved to a blocked address")
			}
		}

		portNumber64, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || portNumber64 == 0 {
			return nil, errors.New("invalid outbound dial port")
		}
		portNumber := uint16(portNumber64)
		selfTarget := p.isSelfOrigin(scheme, expectedHost, expectedPort)
		if !selfTarget {
			for _, addr := range addresses {
				if p.isSelfTarget(addr, portNumber) {
					selfTarget = true
					break
				}
			}
		}
		if selfTarget {
			if err := p.refusalDelay(ctx); err != nil {
				return nil, err
			}
			first := firstAddressForNetwork(addresses, network)
			return nil, fakeConnectionRefused(network, first, int(portNumber))
		}

		var lastErr error
		attempted := false
		for _, addr := range addresses {
			if !addressMatchesNetwork(addr, network) {
				continue
			}
			attempted = true
			target := net.JoinHostPort(addr.String(), port)
			conn, dialErr := p.dialer.DialContext(ctx, network, target)
			if dialErr == nil {
				return conn, nil
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			lastErr = dialErr
		}
		if !attempted {
			return nil, errors.New("outbound hostname has no address for the requested network")
		}
		return nil, newBoundedError("dial outbound hostname", lastErr)
	}
}

func normalizeAddresses(addresses []netip.Addr) []netip.Addr {
	seen := make(map[netip.Addr]struct{}, len(addresses))
	out := make([]netip.Addr, 0, len(addresses))
	for _, addr := range addresses {
		addr = addr.WithZone("").Unmap()
		if !addr.IsValid() {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	return out
}

func addressMatchesNetwork(addr netip.Addr, network string) bool {
	switch network {
	case "tcp4":
		return addr.Is4()
	case "tcp6":
		return addr.Is6()
	default:
		return true
	}
}

func firstAddressForNetwork(addresses []netip.Addr, network string) netip.Addr {
	for _, addr := range addresses {
		if addressMatchesNetwork(addr, network) {
			return addr
		}
	}
	return addresses[0]
}

func fakeConnectionRefused(network string, addr netip.Addr, port int) *net.OpError {
	return &net.OpError{
		Op:   "dial",
		Net:  network,
		Addr: &net.TCPAddr{IP: net.IP(addr.AsSlice()), Port: port},
		Err:  syscall.ECONNREFUSED,
	}
}

func defaultSelfRefusalDelay(ctx context.Context) error {
	variation := int64((selfRefusalDelayVariants - 1) / 2)
	if n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(selfRefusalDelayVariants)); err == nil {
		variation = n.Int64()
	}
	delay := selfRefusalMinimumDelay + time.Duration(variation)*time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var additionallyBlocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

func (p *EgressPolicy) blocked(addr netip.Addr) bool {
	addr = addr.WithZone("").Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return true
	}
	for _, prefix := range additionallyBlocked {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
