package host

import "testing"

func TestParseNormalizesAuthorities(t *testing.T) {
	cases := map[string]struct {
		raw       string
		authority string
		host      string
	}{
		"dns case and port":      {raw: "EXAMPLE.com:00080", authority: "example.com:80", host: "example.com"},
		"dns trailing dot":       {raw: "Example.COM.", authority: "example.com", host: "example.com"},
		"ipv4":                   {raw: "192.0.2.4:443", authority: "192.0.2.4:443", host: "192.0.2.4"},
		"bracketed ipv6":         {raw: "[2001:0db8::1]:0443", authority: "[2001:db8::1]:443", host: "2001:db8::1"},
		"bracketed ipv6 no port": {raw: "[::1]", authority: "[::1]", host: "::1"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			parsed, err := Parse(tc.raw)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.raw, err)
			}
			if parsed.Authority != tc.authority || parsed.Host != tc.host {
				t.Fatalf("Parse(%q) = %+v, want authority=%q host=%q", tc.raw, parsed, tc.authority, tc.host)
			}
		})
	}
}

func TestParseRejectsAmbiguousOrMalformedHosts(t *testing.T) {
	cases := []string{
		"",
		" example.com",
		"example.com ",
		"example.com:bad",
		"example.com:",
		"example.com:65536",
		"example.com:1:2",
		"2001:db8::1",
		"[2001:db8::1",
		"[2001:db8::1]evil",
		"user@example.com",
		"example.com/path",
		"example..com",
		"-example.com",
		"example-.com",
		"example.com\r\nX: forged",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if _, err := Parse(raw); err == nil {
				t.Fatalf("Parse(%q) accepted malformed host", raw)
			}
		})
	}
}

func TestMatcherNeverDefaultsUnknownToUser(t *testing.T) {
	matcher, err := NewMatcher("Example.com.", "ADMIN.Example.com:8443")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]Station{
		"example.com":              StationUser,
		"EXAMPLE.COM:8080":         StationUser,
		"example.com.":             StationUser,
		"admin.example.com":        StationAdmin,
		"ADMIN.EXAMPLE.COM:443":    StationAdmin,
		"unknown.example.com":      StationUnknown,
		"":                         StationUnknown,
		"admin.example.com:broken": StationUnknown,
		"[::1]":                    StationUnknown,
	}
	for raw, want := range cases {
		if got := matcher.Match(raw); got != want {
			t.Errorf("Match(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestConfiguredBareIPv6(t *testing.T) {
	matcher, err := NewMatcher("::1", "2001:db8::2")
	if err != nil {
		t.Fatal(err)
	}
	if got := matcher.Match("[::1]:8080"); got != StationUser {
		t.Fatalf("IPv6 user match = %v", got)
	}
	if got := matcher.Match("[2001:db8::2]"); got != StationAdmin {
		t.Fatalf("IPv6 admin match = %v", got)
	}
}
