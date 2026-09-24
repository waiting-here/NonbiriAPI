// Package observability owns private request diagnostics and bounded source facts.
package observability

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const MaxSourceBytes = 8192

type FieldQuality struct {
	Truncated bool `json:"truncated,omitempty"`
	Multiple  bool `json:"multiple,omitempty"`
	Invalid   bool `json:"invalid,omitempty"`
}

// Source is management-only. Explicit JSON encoding is reserved for the
// diagnostic repository and authorized DTOs; ordinary formatting is redacted.
type Source struct {
	EffectiveIP       string                  `json:"effective_ip"`
	IPQuality         string                  `json:"ip_quality"`
	UserAgent         string                  `json:"user_agent,omitempty"`
	Origin            string                  `json:"origin,omitempty"`
	Referer           string                  `json:"referer,omitempty"`
	HTTPReferer       string                  `json:"http_referer,omitempty"`
	OpenRouterTitle   string                  `json:"openrouter_title,omitempty"`
	LegacyTitle       string                  `json:"legacy_title,omitempty"`
	SDKLang           string                  `json:"sdk_lang,omitempty"`
	SDKVersion        string                  `json:"sdk_version,omitempty"`
	SDKRuntime        string                  `json:"sdk_runtime,omitempty"`
	SDKRuntimeVersion string                  `json:"sdk_runtime_version,omitempty"`
	Quality           map[string]FieldQuality `json:"quality,omitempty"`
}

func (Source) String() string         { return "[private request source]" }
func (s Source) GoString() string     { return s.String() }
func (s Source) LogValue() slog.Value { return slog.StringValue(s.String()) }

func CaptureSource(r *http.Request) Source {
	s := Source{IPQuality: httpmw.ClientIPQuality(r), Quality: make(map[string]FieldQuality)}
	if ip, err := netip.ParseAddr(httpmw.ClientIP(r)); err == nil {
		s.EffectiveIP = ip.Unmap().String()
	}
	if r == nil {
		return s
	}
	fields := []struct {
		name, header string
		limit        int
		target       *string
		origin       bool
	}{
		{"user_agent", "User-Agent", 2048, &s.UserAgent, false},
		{"origin", "Origin", 512, &s.Origin, true},
		{"referer", "Referer", 512, &s.Referer, true},
		{"http_referer", "HTTP-Referer", 512, &s.HTTPReferer, true},
		{"openrouter_title", "X-OpenRouter-Title", 256, &s.OpenRouterTitle, false},
		{"legacy_title", "X-Title", 256, &s.LegacyTitle, false},
		{"sdk_lang", "X-Stainless-Lang", 128, &s.SDKLang, false},
		{"sdk_version", "X-Stainless-Package-Version", 128, &s.SDKVersion, false},
		{"sdk_runtime", "X-Stainless-Runtime", 128, &s.SDKRuntime, false},
		{"sdk_runtime_version", "X-Stainless-Runtime-Version", 128, &s.SDKRuntimeVersion, false},
	}
	for _, field := range fields {
		values := r.Header.Values(field.header)
		if len(values) == 0 {
			continue
		}
		q := FieldQuality{Multiple: len(values) > 1}
		value := values[0]
		if field.origin {
			value, q.Invalid = originOnly(value)
		} else {
			q.Invalid = !utf8.ValidString(value)
			value = cleanText(value)
		}
		value, q.Truncated = boundText(value, field.limit)
		*field.target = value
		if q != (FieldQuality{}) {
			s.Quality[field.name] = q
		}
	}
	// JSON escaping can expand otherwise bounded Unicode/control-free text.
	// Remove the largest fields deterministically if that expansion hits budget.
	for _, field := range fields {
		encoded, _ := json.Marshal(s)
		if len(encoded) <= MaxSourceBytes {
			break
		}
		*field.target = ""
		q := s.Quality[field.name]
		q.Truncated = true
		s.Quality[field.name] = q
	}
	return s
}

func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
}

func boundText(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	s = s[:limit]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s, true
}

func originOnly(value string) (string, bool) {
	if !utf8.ValidString(value) || len(value) > 16384 || cleanText(value) != value {
		return "", true
	}
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", true
	}
	host := strings.ToLower(u.Hostname())
	if strings.ContainsAny(host, " \\/?#@") {
		return "", true
	}
	port := u.Port()
	if port != "" && !(u.Scheme == "http" && port == "80") && !(u.Scheme == "https" && port == "443") {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, false
}

// ParseSource rejects arbitrary/unbounded headers in persisted source JSON.
func ParseSource(data []byte) (Source, error) {
	var s Source
	if len(data) > MaxSourceBytes || !utf8.Valid(data) || strictjson.ValidateObject(data) != nil {
		return s, errors.New("invalid source")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&s) != nil {
		return Source{}, errors.New("invalid source")
	}
	if s.IPQuality != "direct_peer" && s.IPQuality != "trusted_forwarded" && s.IPQuality != "peer_fallback" {
		return Source{}, errors.New("invalid source")
	}
	if s.EffectiveIP != "" {
		ip, err := netip.ParseAddr(s.EffectiveIP)
		if err != nil || ip.Unmap().String() != s.EffectiveIP {
			return Source{}, errors.New("invalid source")
		}
	}
	limits := map[string]int{"user_agent": 2048, "origin": 512, "referer": 512, "http_referer": 512, "openrouter_title": 256, "legacy_title": 256, "sdk_lang": 128, "sdk_version": 128, "sdk_runtime": 128, "sdk_runtime_version": 128}
	values := map[string]string{"user_agent": s.UserAgent, "origin": s.Origin, "referer": s.Referer, "http_referer": s.HTTPReferer, "openrouter_title": s.OpenRouterTitle, "legacy_title": s.LegacyTitle, "sdk_lang": s.SDKLang, "sdk_version": s.SDKVersion, "sdk_runtime": s.SDKRuntime, "sdk_runtime_version": s.SDKRuntimeVersion}
	for key, value := range values {
		if len(value) > limits[key] || cleanText(value) != value {
			return Source{}, errors.New("invalid source")
		}
		if value != "" && (key == "origin" || key == "referer" || key == "http_referer") {
			origin, bad := originOnly(value)
			if bad || origin != value {
				return Source{}, errors.New("invalid source")
			}
		}
	}
	for key := range s.Quality {
		if _, ok := limits[key]; !ok {
			return Source{}, errors.New("invalid source")
		}
	}
	return s, nil
}

type sourceKey struct{}

func WithSource(ctx context.Context, source Source) context.Context {
	// Clone the map so concurrent attempts cannot change captured ingress facts.
	copySource := source
	copySource.Quality = make(map[string]FieldQuality, len(source.Quality))
	for key, value := range source.Quality {
		copySource.Quality[key] = value
	}
	return context.WithValue(ctx, sourceKey{}, copySource)
}
func SourceFromContext(ctx context.Context) (Source, bool) {
	if ctx == nil {
		return Source{}, false
	}
	s, ok := ctx.Value(sourceKey{}).(Source)
	return s, ok
}
