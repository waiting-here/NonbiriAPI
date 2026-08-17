// Package host contains the small, shared host-boundary vocabulary used by
// the HTTP edge and the embedded station router. Host values are treated as
// security inputs: only syntactically valid authorities are normalized, and a
// matcher never assigns an unknown value to the user station.
package host

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Station identifies the station selected by an exact configured host.
type Station uint8

const (
	StationUnknown Station = iota
	StationUser
	StationAdmin
)

// String returns a stable diagnostic name.
func (s Station) String() string {
	switch s {
	case StationUser:
		return "user"
	case StationAdmin:
		return "admin"
	default:
		return "unknown"
	}
}

// Parsed is a normalized host authority. Port is normalized to decimal when
// present; station matching intentionally compares Host, not the request's
// transport port. The port is still parsed strictly so a malformed authority
// cannot be reduced to a seemingly valid hostname.
type Parsed struct {
	Host      string
	Port      string
	Authority string
	HasPort   bool
}

// Parse parses a Host header value. Unlike ParseConfigured, bare IPv6 is not
// accepted because an HTTP Host authority must bracket an IPv6 literal.
func Parse(raw string) (Parsed, error) {
	return parse(raw, false)
}

// ParseConfigured parses a configured host or authority. A bare IPv6 literal
// is accepted here because Config.UserHost stores the hostname portion of the
// configured origin without URL brackets.
func ParseConfigured(raw string) (Parsed, error) {
	return parse(raw, true)
}

func parse(raw string, allowBareIPv6 bool) (Parsed, error) {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return Parsed{}, fmt.Errorf("host must not be empty or surrounded by whitespace")
	}
	if strings.ContainsAny(raw, "\x00\r\n\t /\\?#@%") {
		return Parsed{}, fmt.Errorf("host contains a forbidden character")
	}

	hostPart := raw
	port := ""
	hasPort := false

	switch {
	case strings.HasPrefix(raw, "["):
		close := strings.IndexByte(raw, ']')
		if close <= 1 {
			return Parsed{}, fmt.Errorf("bracketed host is malformed")
		}
		hostPart = raw[1:close]
		if _, err := parseIPLiteral(hostPart); err != nil {
			return Parsed{}, err
		}
		rest := raw[close+1:]
		switch {
		case rest == "":
		case strings.HasPrefix(rest, ":"):
			var err error
			port, err = parsePort(rest[1:])
			if err != nil {
				return Parsed{}, err
			}
			hasPort = true
		default:
			return Parsed{}, fmt.Errorf("characters follow a bracketed host")
		}
	case strings.Count(raw, ":") == 0:
		// A DNS name or an IPv4 literal without a port.
	case strings.Count(raw, ":") == 1:
		idx := strings.LastIndexByte(raw, ':')
		hostPart = raw[:idx]
		var err error
		port, err = parsePort(raw[idx+1:])
		if err != nil {
			return Parsed{}, err
		}
		hasPort = true
	case allowBareIPv6:
		// Config.UserHost is a hostname portion, so it may be an unbracketed
		// IPv6 literal even though the wire Host representation may not be.
		if _, err := parseIPLiteral(raw); err != nil {
			return Parsed{}, fmt.Errorf("host contains too many colons")
		}
	default:
		return Parsed{}, fmt.Errorf("IPv6 host must be bracketed")
	}

	hostPart, err := normalizeHost(hostPart)
	if err != nil {
		return Parsed{}, err
	}

	authorityHost := hostPart
	if strings.Contains(hostPart, ":") {
		authorityHost = "[" + hostPart + "]"
	}
	authority := authorityHost
	if hasPort {
		authority += ":" + port
	}
	return Parsed{Host: hostPart, Port: port, Authority: authority, HasPort: hasPort}, nil
}

func parseIPLiteral(raw string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid IP literal")
	}
	if addr.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("IP zones are not allowed in station hosts")
	}
	return addr.Unmap(), nil
}

func normalizeHost(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("host is empty")
	}
	if strings.HasSuffix(raw, ".") {
		raw = strings.TrimSuffix(raw, ".")
		if raw == "" || strings.HasSuffix(raw, ".") {
			return "", fmt.Errorf("host has an invalid trailing-dot form")
		}
	}

	if addr, err := parseIPLiteral(raw); err == nil {
		return addr.String(), nil
	}

	if len(raw) > 253 {
		return "", fmt.Errorf("hostname is too long")
	}
	raw = strings.ToLower(raw)
	for _, label := range strings.Split(raw, ".") {
		if len(label) == 0 || len(label) > 63 {
			return "", fmt.Errorf("hostname label is invalid")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("hostname label has an invalid hyphen")
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
				continue
			}
			return "", fmt.Errorf("hostname contains a non-ASCII DNS character")
		}
	}
	return raw, nil
}

func parsePort(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("port is empty")
	}
	if len(raw) > 5 {
		return "", fmt.Errorf("port is out of range")
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return "", fmt.Errorf("port is not numeric")
		}
	}
	n, err := strconv.ParseUint(raw, 10, 16)
	if err != nil {
		return "", fmt.Errorf("port is out of range")
	}
	return strconv.FormatUint(n, 10), nil
}

// Matcher maps only the two configured station hosts. Port spelling is
// validated by Parse, but host identity is the hostname itself: a reverse
// proxy may forward a public Host with or without its listener port.
type Matcher struct {
	userHost  string
	adminHost string
}

// NewMatcher validates and builds an immutable station matcher.
func NewMatcher(userRaw, adminRaw string) (Matcher, error) {
	user, err := ParseConfigured(userRaw)
	if err != nil {
		return Matcher{}, fmt.Errorf("user host: %w", err)
	}
	admin, err := ParseConfigured(adminRaw)
	if err != nil {
		return Matcher{}, fmt.Errorf("admin host: %w", err)
	}
	if user.Host == admin.Host {
		return Matcher{}, fmt.Errorf("user and admin hosts must differ")
	}
	return Matcher{userHost: user.Host, adminHost: admin.Host}, nil
}

// Match returns Unknown for empty, malformed, or unconfigured hosts.
func (m Matcher) Match(raw string) Station {
	parsed, err := Parse(raw)
	if err != nil {
		return StationUnknown
	}
	switch parsed.Host {
	case m.userHost:
		return StationUser
	case m.adminHost:
		return StationAdmin
	default:
		return StationUnknown
	}
}

// UserHost returns the normalized configured user hostname.
func (m Matcher) UserHost() string { return m.userHost }

// AdminHost returns the normalized configured admin hostname.
func (m Matcher) AdminHost() string { return m.adminHost }

type stationContextKey struct{}

// WithStation records the already-validated station in a request context.
func WithStation(ctx context.Context, station Station) context.Context {
	return context.WithValue(ctx, stationContextKey{}, station)
}

// StationFromContext retrieves a station placed in a request context by the
// HTTP edge. The boolean distinguishes an absent value from Unknown.
func StationFromContext(ctx context.Context) (Station, bool) {
	if ctx == nil {
		return StationUnknown, false
	}
	station, ok := ctx.Value(stationContextKey{}).(Station)
	return station, ok
}
