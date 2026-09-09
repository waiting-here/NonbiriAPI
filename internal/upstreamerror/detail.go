// Package upstreamerror extracts a bounded, source-free description of a
// failed upstream request. Raw responses and headers never leave this boundary.
package upstreamerror

import (
	"bytes"
	"encoding/json"
	"html"
	"io"
	"log/slog"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const MaxBodyBytes = 64 << 10

const redacted = "[redacted]"

// Detail can only be populated through the extraction boundary. Its zero
// value requests the caller's ordinary generic error message.
type Detail struct {
	message string
	code    string
}

func (d Detail) Message() string { return d.message }
func (d Detail) Code() string    { return d.code }

// Context holds source identifiers and an exact secret detector backed by
// the connector's fingerprints, never by retained plaintext credentials.
// ContainsSecret must start with fresh rolling state on every call.
type Context struct {
	BaseURL        string
	PrivateModel   string
	ContainsSecret func([]byte) bool
}

func (Context) String() string         { return "[redacted error context]" }
func (Context) GoString() string       { return "[redacted error context]" }
func (c Context) LogValue() slog.Value { return slog.StringValue(c.String()) }

var (
	urlPattern     = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s<>"'` + "`" + `]+`)
	domainPattern  = regexp.MustCompile(`(?i)(?:[\p{L}\p{N}_-]+\.)+[\p{L}][\p{L}\p{N}_-]*(?::[0-9]+)?`)
	emailPattern   = regexp.MustCompile(`[^\s<>"'@]+@[^\s<>"'@]+`)
	ipv4Pattern    = regexp.MustCompile(`\b[0-9]{1,3}(?:\.[0-9]{1,3}){3}(?::[0-9]+)?\b`)
	ipv6Pattern    = regexp.MustCompile(`(?i)[0-9a-f:.]*:[0-9a-f:.]+(?:%[a-z0-9_-]+)?`)
	authPattern    = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[^\s,;<>"']+|\b(?:api[_ -]?key|authorization|access[_ -]?token)\s*[:=]\s*[^\s,;<>"']+|\bsk-[a-z0-9_*-]+`)
	encodedPattern = regexp.MustCompile(`%[0-9a-fA-F]{2}|&#(?:[0-9]+|[xX][0-9a-fA-F]+);`)
	codePattern    = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
)

// Read limits error-body work independently of the successful response
// budget. A truncated or unreadable response has no caller-facing detail.
func (c Context) Read(reader io.Reader, limit int64) Detail {
	if reader == nil || limit < 1 {
		return Detail{}
	}
	limit = min(limit, MaxBodyBytes)
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	defer clear(body)
	if err != nil || int64(len(body)) > limit {
		return Detail{}
	}
	return c.Parse(body)
}

// Parse selects only message and machine-code scalars. HTML, arrays, malformed
// JSON and ambiguous objects fall back to the ordinary status-based message.
func (c Context) Parse(body []byte) Detail {
	if c.ContainsSecret == nil || len(body) > MaxBodyBytes || !utf8.Valid(body) {
		return Detail{}
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return Detail{}
	}
	var message, code string
	if body[0] == '{' {
		if strictjson.ValidateObject(body) != nil {
			return Detail{}
		}
		var root map[string]json.RawMessage
		if json.Unmarshal(body, &root) != nil {
			return Detail{}
		}
		fields := root
		if raw, ok := root["error"]; ok {
			if json.Unmarshal(raw, &message) == nil {
				code = machineCode(root)
			} else {
				if json.Unmarshal(raw, &fields) != nil || fields == nil {
					return Detail{}
				}
			}
		}
		if message == "" {
			_ = json.Unmarshal(fields["message"], &message)
			if message == "" {
				_ = json.Unmarshal(fields["error_description"], &message)
			}
		}
		if code == "" {
			code = machineCode(fields)
		}
	} else {
		// A plain-text error is useful, but an HTML proxy page, JSON scalar or
		// array is not a safe substitute for the documented error fields.
		if bytes.ContainsAny(body, "<>\x00") || bytes.ContainsAny(body[:1], "[\"") {
			return Detail{}
		}
		message = string(body)
	}
	message = c.clean(message)
	cleanCode := c.clean(code)
	if cleanCode != code || !codePattern.MatchString(cleanCode) {
		cleanCode = ""
	}
	return Detail{message: boundUTF8(message, 1024), code: cleanCode}
}

// IsEvent reports an explicit error envelope without interpreting an ordinary
// completion as a failure merely because it contains text resembling an error.
func IsEvent(body []byte) bool {
	if !bytes.Contains(body, []byte(`"error"`)) && !bytes.Contains(body, []byte(`\u`)) {
		return false
	}
	if len(body) > MaxBodyBytes || strictjson.ValidateObject(body) != nil {
		return false
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return false
	}
	_, present := root["error"]
	return present && !bytes.Equal(bytes.TrimSpace(root["error"]), []byte("null"))
}

func machineCode(fields map[string]json.RawMessage) string {
	for _, key := range []string{"code", "type"} {
		raw := fields[key]
		var value string
		if json.Unmarshal(raw, &value) == nil && value != "" {
			return value
		}
		var number json.Number
		if json.Unmarshal(raw, &number) == nil {
			if _, err := number.Int64(); err == nil {
				return number.String()
			}
		}
	}
	return ""
}

func (c Context) clean(value string) string {
	if len(value) > MaxBodyBytes || !utf8.ValidString(value) {
		return ""
	}
	for range 4 {
		next := html.UnescapeString(value)
		if decoded, err := url.PathUnescape(next); err == nil {
			next = decoded
		}
		if next == value {
			break
		}
		value = next
	}
	if encodedPattern.MatchString(value) || !utf8.ValidString(value) {
		return ""
	}
	value = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		case '\u3002', '\uff0e', '\uff61':
			return '.'
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, value)
	value = c.hideSecrets(value)
	if value == "" {
		return ""
	}
	value = authPattern.ReplaceAllString(value, redacted)
	value = urlPattern.ReplaceAllString(value, redacted)
	value = emailPattern.ReplaceAllString(value, redacted)
	if parsed, err := url.Parse(c.BaseURL); err == nil && parsed.Hostname() != "" {
		value = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(parsed.Hostname())).ReplaceAllString(value, redacted)
	}
	if c.PrivateModel != "" {
		value = strings.ReplaceAll(value, c.PrivateModel, "[model]")
	}
	value = domainPattern.ReplaceAllString(value, redacted)
	value = ipv4Pattern.ReplaceAllString(value, redacted)
	value = ipv6Pattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if _, err := netip.ParseAddr(strings.TrimRight(candidate, ".")); err == nil {
			return redacted
		}
		return candidate
	})
	return strings.TrimSpace(c.hideSecrets(value))
}

func (c Context) hideSecrets(value string) string {
	if c.ContainsSecret == nil || c.ContainsSecret(nil) {
		return ""
	}
	for range 8 {
		if !c.ContainsSecret([]byte(value)) {
			return value
		}
		// Find the first complete reflected secret, then its exact start,
		// using only fresh fingerprint scans of this bounded response text.
		low, high := 1, len(value)
		for low < high {
			middle := low + (high-low)/2
			if c.ContainsSecret([]byte(value[:middle])) {
				high = middle
			} else {
				low = middle + 1
			}
		}
		end := low
		low, high = 0, end-1
		for low < high {
			middle := low + (high-low+1)/2
			if c.ContainsSecret([]byte(value[middle:end])) {
				low = middle
			} else {
				high = middle - 1
			}
		}
		value = value[:low] + redacted + value[end:]
	}
	if c.ContainsSecret([]byte(value)) {
		return ""
	}
	return value
}

func boundUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
