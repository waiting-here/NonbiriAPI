// Package textprivacy removes network addresses from presentation text.
// It does not change stored audit facts, editable configuration or model output.
package textprivacy

import (
	"net/netip"
	"regexp"
	"strings"
)

var ipv4 = regexp.MustCompile(`[0-9]{1,3}(?:\.[0-9]{1,3}){3}`)

// IPv4 replaces valid dotted-decimal addresses, including addresses inside
// URLs or adjacent to text. Invalid octets and longer numeric sequences remain.
func IPv4(value string) string {
	if strings.Count(value, ".") < 3 {
		return value
	}
	matches := ipv4.FindAllStringIndex(value, -1)
	var out strings.Builder
	offset := 0
	digit := func(b byte) bool { return b >= '0' && b <= '9' }
	for _, span := range matches {
		start, end := span[0], span[1]
		if start > 0 && (digit(value[start-1]) || value[start-1] == '.' && start > 1 && digit(value[start-2])) {
			continue
		}
		if end < len(value) && (digit(value[end]) || value[end] == '.' && end+1 < len(value) && digit(value[end+1])) {
			continue
		}
		address, err := netip.ParseAddr(value[start:end])
		if err != nil || !address.Is4() {
			continue
		}
		out.WriteString(value[offset:start])
		out.WriteString("[redacted]")
		offset = end
	}
	if offset == 0 {
		return value
	}
	out.WriteString(value[offset:])
	return out.String()
}
