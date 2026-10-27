// Package endpointtags defines administrative endpoint labels and domain rules.
package endpointtags

import (
	"net/netip"
	"net/url"
	"slices"
	"strings"
)

const Mainstream = "mainstream"

var values = [...]string{"abusive_third_party", "community_charity", "noncommercial_gateway", "user_gateway", "steward_gateway", "invalid_entry", Mainstream, "mysterious", "other_third_party"}

// Values returns a copy of the supported administrative tag vocabulary.
func Values() []string { return slices.Clone(values[:]) }

func Valid(tag string) bool {
	return slices.Contains(values[:], tag)
}

// Domain follows the existing endpoint ASCII/punycode hostname contract. It
// performs no network lookup and never interprets a path as a hostname.
func Domain(raw string) string {
	host := strings.ToLower(strings.TrimRight(raw, "."))
	if host == "" || len(host) > 253 {
		return ""
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return ""
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return ""
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return ""
			}
		}
	}
	return host
}

func Pattern(raw string) string {
	if !strings.HasPrefix(raw, "*.") {
		return ""
	}
	if domain := Domain(raw[2:]); domain != "" {
		return "*." + domain
	}
	return ""
}

// Domains produces the finite ancestor set once when an address is stored.
// Indexed equality joins then serve all rule matching and authorization reads.
func Domains(baseURL string) []string {
	result := []string{}
	if len(baseURL) > 4096 {
		return result
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.User != nil || u.Scheme != "https" && u.Scheme != "http" {
		return result
	}
	domain := Domain(u.Hostname())
	for domain != "" {
		result = append(result, domain)
		_, rest, found := strings.Cut(domain, ".")
		if !found {
			break
		}
		domain = rest
	}
	return result
}
