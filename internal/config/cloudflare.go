package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const MaxCloudflareRoutes = 32

// CloudflareRoute is a deployment-only exact endpoint mapping. Provider
// credentials remain request-owned; the independent gateway token is private.
type CloudflareRoute struct {
	SourceBaseURL            string `json:"source_base_url"`
	GatewayBaseURL           string `json:"gateway_base_url"`
	TokenEnv                 string `json:"token_env"`
	CredentialHeader         string `json:"credential_header"`
	CredentialQueryParameter string `json:"credential_query_parameter,omitempty"`
	RetainCredentialHeader   bool   `json:"retain_credential_header,omitempty"`
	token                    string
}

func (CloudflareRoute) String() string                 { return "[private Cloudflare route]" }
func (r CloudflareRoute) GoString() string             { return r.String() }
func (r CloudflareRoute) LogValue() slog.Value         { return slog.StringValue(r.String()) }
func (r CloudflareRoute) MarshalJSON() ([]byte, error) { return json.Marshal(r.String()) }

// GatewayToken is consumed only by the private outbound request constructor.
func (r CloudflareRoute) GatewayToken() string { return r.token }

// ParseCloudflareRoutes resolves token references once, during startup. Errors
// never include a configuration value, environment value, or parser excerpt.
func ParseCloudflareRoutes(raw string, lookup func(string) (string, bool)) ([]CloudflareRoute, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if len(raw) > 256<<10 {
		return nil, errors.New("route configuration is too large")
	}
	// Wrapping admits only an array while sharing the duplicate-field and
	// structural bounds of the existing strict JSON parser.
	wrapped := []byte(`{"routes":` + raw + `}`)
	if strictjson.ValidateObject(wrapped) != nil {
		return nil, errors.New("routes must be an unambiguous JSON array")
	}
	var entries []struct {
		CloudflareRoute
		RetainCredentialHeader json.RawMessage `json:"retain_credential_header"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&entries) != nil || entries == nil || len(entries) > MaxCloudflareRoutes {
		return nil, errors.New("routes must be an array of at most 32 route objects with known fields")
	}
	routes := make([]CloudflareRoute, len(entries))
	for i, entry := range entries {
		routes[i] = entry.CloudflareRoute
		route := &routes[i]
		fail := func(reason string) ([]CloudflareRoute, error) { return nil, fmt.Errorf("route %d: %s", i+1, reason) }
		if len(entry.RetainCredentialHeader) != 0 {
			switch string(entry.RetainCredentialHeader) {
			case "true":
				route.RetainCredentialHeader = true
			case "false":
			default:
				return fail("retain_credential_header must be a boolean")
			}
		}
		for _, field := range []struct {
			value   string
			gateway bool
		}{{route.SourceBaseURL, false}, {route.GatewayBaseURL, true}} {
			u, err := url.Parse(field.value)
			if err != nil || u == nil || len(field.value) > 4096 || strings.TrimSpace(field.value) != field.value ||
				u.Host == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
				(u.Scheme != "http" && u.Scheme != "https") || (field.gateway && u.Scheme != "https") || strings.ContainsAny(field.value, "?#") {
				return fail("source and gateway must be absolute base URLs without credentials, query or fragment; gateway requires HTTPS")
			}
		}
		if !validEnvironmentName(route.TokenEnv) || lookup == nil {
			return fail("token_env must name a gateway token environment variable")
		}
		token, ok := lookup(route.TokenEnv)
		if !ok || token == "" || len(token) > 4096 || strings.TrimSpace(token) != token || strings.ContainsAny(token, " \t\r\n") || validateStartupAuthValue("", token, 4096, false) != nil {
			return fail("gateway token environment value is missing or invalid")
		}
		if !validHTTPToken(route.CredentialHeader) || len(route.CredentialHeader) > 128 || reservedCredentialHeader(route.CredentialHeader) {
			return fail("credential_header must be a provider authentication header")
		}
		route.CredentialHeader = http.CanonicalHeaderKey(route.CredentialHeader)
		if route.CredentialQueryParameter != "" && (!validHTTPToken(route.CredentialQueryParameter) || len(route.CredentialQueryParameter) > 128) {
			return fail("credential_query_parameter is invalid")
		}
		if route.RetainCredentialHeader && route.CredentialQueryParameter == "" {
			return fail("retain_credential_header requires credential_query_parameter")
		}
		route.token = token
	}
	return routes, nil
}

func validEnvironmentName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for i, c := range []byte(value) {
		if c != '_' && !(c >= 'A' && c <= 'Z') && !(c >= 'a' && c <= 'z') && !(i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range []byte(value) {
		if c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || bytes.ContainsRune([]byte("!#$%&'*+-.^_`|~"), rune(c)) {
			continue
		}
		return false
	}
	return true
}
func reservedCredentialHeader(value string) bool {
	value = strings.ToLower(value)
	if strings.HasPrefix(value, "cf-") {
		return true
	}
	switch value {
	case "host", "content-type", "content-length", "transfer-encoding", "connection", "trailer", "te", "upgrade", "accept", "accept-encoding", "proxy-authorization", "proxy-authenticate":
		return true
	}
	return false
}
