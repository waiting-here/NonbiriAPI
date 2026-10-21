package textprivacy

import (
	"net/netip"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

type dnsNode struct {
	next map[byte]*dnsNode
	end  bool
}

// DNSRedactor matches only hostnames and labels supplied by the current read.
// It does not infer public suffixes, Unicode aliases, or names from URL paths.
type DNSRedactor struct {
	root dnsNode
}

// NewDNSRedactor derives display identifiers from bounded endpoint URLs.
// IP addresses retain their separate existing presentation policy.
func NewDNSRedactor(baseURLs ...string) *DNSRedactor {
	r := &DNSRedactor{}
	for _, base := range baseURLs {
		if len(base) > 4096 {
			continue
		}
		u, err := url.Parse(base)
		if err != nil || u.Scheme != "http" && u.Scheme != "https" {
			continue
		}
		host := strings.ToLower(strings.TrimRight(u.Hostname(), "."))
		if host == "" || len(host) > 253 {
			continue
		}
		if _, err := netip.ParseAddr(host); err == nil {
			continue
		}
		labels := strings.Split(host, ".")
		valid := true
		for _, label := range labels {
			if len(label) == 0 || len(label) > 63 {
				valid = false
				break
			}
			for i := range len(label) {
				b := label[i]
				if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
					valid = false
					break
				}
			}
		}
		if !valid {
			continue
		}
		r.add(host)
		for _, label := range labels[:len(labels)-1] {
			switch label {
			case "www", "api", "com", "net", "org", "edu", "gov", "mil", "int", "co", "ac":
				continue
			}
			r.add(label)
		}
	}
	if len(r.root.next) == 0 {
		return nil
	}
	return r
}

func (r *DNSRedactor) add(term string) {
	node := &r.root
	for i := range len(term) {
		if node.next == nil {
			node.next = make(map[byte]*dnsNode)
		}
		if node.next[term[i]] == nil {
			node.next[term[i]] = &dnsNode{}
		}
		node = node.next[term[i]]
	}
	node.end = true
}

func dnsWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }

// Redact replaces complete identifiers, preferring the longest match.
// Hyphens and underscores can delimit an identifier but are never used to
// split a configured label. Invalid UTF-8 bytes remain intact for binary views.
func (r *DNSRedactor) Redact(value string) string {
	if r == nil {
		return value
	}
	var out strings.Builder
	written := 0
	for start := 0; start < len(value); {
		before, _ := utf8.DecodeLastRuneInString(value[:start])
		end := start
		if start == 0 || !dnsWordRune(before) {
			node := &r.root
			for i := start; i < len(value); i++ {
				b := value[i]
				if b >= 'A' && b <= 'Z' {
					b += 'a' - 'A'
				}
				node = node.next[b]
				if node == nil {
					break
				}
				if node.end {
					after, _ := utf8.DecodeRuneInString(value[i+1:])
					if i+1 == len(value) || !dnsWordRune(after) {
						end = i + 1
					}
				}
			}
		}
		if end > start {
			out.WriteString(value[written:start])
			out.WriteString("[redacted]")
			written, start = end, end
			continue
		}
		_, size := utf8.DecodeRuneInString(value[start:])
		start += size
	}
	if written == 0 {
		return value
	}
	out.WriteString(value[written:])
	return out.String()
}
