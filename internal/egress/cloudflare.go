package egress

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/config"
)

type cloudflareRoute struct {
	source, gateway                          *url.URL
	token, credentialHeader, credentialQuery string
	gatewayBaseURL                           string
	retainCredentialHeader                   bool
}

func (*cloudflareRoute) String() string     { return "[private Cloudflare route]" }
func (r *cloudflareRoute) GoString() string { return r.String() }

func buildCloudflareRoutes(policy *EgressPolicy, entries []config.CloudflareRoute) (map[string]*cloudflareRoute, error) {
	if len(entries) > config.MaxCloudflareRoutes {
		return nil, errors.New("too many Cloudflare routes")
	}
	routes := make(map[string]*cloudflareRoute, len(entries))
	for i, entry := range entries {
		fail := func() (map[string]*cloudflareRoute, error) {
			return nil, fmt.Errorf("invalid Cloudflare route %d", i+1)
		}
		parse := func(raw string) (*url.URL, string, error) {
			u, err := url.Parse(raw)
			if err != nil || u == nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "?#") {
				return nil, "", errors.New("invalid route URL")
			}
			if _, err := policy.ValidateBaseURL(raw); err != nil {
				return nil, "", err
			}
			key, _, canonical, _, err := canonicalizeBaseURL(raw, false)
			if err != nil || unsafeRoutePath(canonical.Path) {
				return nil, "", errors.New("invalid route path")
			}
			return canonical, key, nil
		}
		source, key, err := parse(entry.SourceBaseURL)
		if err != nil {
			return fail()
		}
		gateway, _, err := parse(entry.GatewayBaseURL)
		if err != nil || gateway.Scheme != "https" || entry.GatewayToken() == "" || entry.CredentialHeader == "" {
			return fail()
		}
		if _, exists := routes[key]; exists {
			return nil, fmt.Errorf("duplicate Cloudflare route source at entry %d", i+1)
		}
		routes[key] = &cloudflareRoute{source: source, gateway: gateway, token: entry.GatewayToken(), credentialHeader: entry.CredentialHeader, credentialQuery: entry.CredentialQueryParameter, gatewayBaseURL: entry.GatewayBaseURL, retainCredentialHeader: entry.RetainCredentialHeader}
	}
	return routes, nil
}

func unsafeRoutePath(path string) bool {
	if strings.Contains(path, "\\") {
		return true
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

// prepare changes only the private clone; no URL containing a credential is
// retained by a connector, response observer, or error chain.
func (r *cloudflareRoute) prepare(outbound *http.Request) (*routeSecrets, error) {
	path := outbound.URL.EscapedPath()
	prefix := strings.TrimSuffix(r.source.EscapedPath(), "/")
	if path != prefix && !strings.HasPrefix(path, prefix+"/") || unsafeRoutePath(outbound.URL.Path) {
		return nil, errors.New("routed request path does not match its endpoint")
	}
	values := []string{}
	for name, entries := range outbound.Header {
		if strings.EqualFold(name, r.credentialHeader) {
			values = append(values, entries...)
		}
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" || len(values[0]) > 4096 || hasUnsafeControl(values[0]) {
		return nil, errors.New("routed request requires one provider credential")
	}
	credential := values[0]
	secrets := newRouteSecrets(r.token, credential, r.gateway.String(), r.gatewayBaseURL)
	if r.gateway.Port() == "443" {
		withoutDefaultPort := *r.gateway
		withoutDefaultPort.Host = withoutDefaultPort.Hostname()
		if strings.Contains(withoutDefaultPort.Host, ":") {
			withoutDefaultPort.Host = "[" + withoutDefaultPort.Host + "]"
		}
		secrets.add(withoutDefaultPort.String())
	}
	if scheme, value, ok := strings.Cut(credential, " "); ok && strings.EqualFold(scheme, "Bearer") {
		if strings.TrimSpace(value) == "" {
			return nil, errors.New("routed request requires a provider credential")
		}
		secrets.add(value)
	}
	gateway := *r.gateway
	gateway.RawPath = strings.TrimSuffix(gateway.EscapedPath(), "/") + strings.TrimPrefix(path, prefix)
	var err error
	gateway.Path, err = url.PathUnescape(gateway.RawPath)
	if err != nil {
		return nil, errors.New("routed request path is invalid")
	}
	gateway.RawQuery, gateway.ForceQuery = outbound.URL.RawQuery, outbound.URL.ForceQuery
	if r.credentialQuery != "" {
		query, err := url.ParseQuery(gateway.RawQuery)
		if err != nil {
			return nil, errors.New("routed request query is invalid")
		}
		if _, exists := query[r.credentialQuery]; exists {
			return nil, errors.New("routed request credential query conflicts with input")
		}
		if gateway.RawQuery != "" {
			gateway.RawQuery += "&"
		}
		gateway.RawQuery += url.QueryEscape(r.credentialQuery) + "=" + url.QueryEscape(credential)
		if !r.retainCredentialHeader {
			for name := range outbound.Header {
				if strings.EqualFold(name, r.credentialHeader) {
					delete(outbound.Header, name)
				}
			}
		}
	}
	removeCloudflareControls(outbound.Header)
	if outbound.Header == nil {
		outbound.Header = make(http.Header)
	}
	outbound.Header.Set("Cf-Aig-Authorization", "Bearer "+r.token)
	outbound.Header.Set("Cf-Aig-Collect-Log", "false")
	outbound.Header.Set("Cf-Aig-Collect-Log-Payload", "false")
	outbound.Header.Set("Cf-Aig-Skip-Cache", "true")
	outbound.Header.Set("Cf-Aig-Max-Attempts", "1")
	outbound.Header.Set("Cf-Aig-No-Wholesale", "true")
	outbound.URL = &gateway
	outbound.Host = gateway.Host
	return secrets, nil
}

func removeCloudflareControls(headers http.Header) {
	for name := range headers {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "cf-aig-") || lower == "cf-cache-ttl" || lower == "cf-skip-cache" || lower == "cf-aig-byok-alias" {
			delete(headers, name)
		}
	}
}

func safeRouteRequest(request *http.Request, credentialHeader string) *http.Request {
	safe := request.Clone(request.Context())
	safe.Body, safe.GetBody = nil, nil
	removeCloudflareControls(safe.Header)
	removeWorkerHeaders(safe.Header)
	for name := range safe.Header {
		if strings.EqualFold(name, credentialHeader) || workerCredentialHeader(name) {
			delete(safe.Header, name)
		}
	}
	return safe
}

// Preserve classifications and cancellation identity, but never retain a
// transport's arbitrary message, URL, or wrapped request in the error graph.
func safeRouteError(err error) error {
	if err == nil {
		return nil
	}
	var tooLarge *ResponseTooLargeError
	if errors.As(err, &tooLarge) {
		return &ResponseTooLargeError{Limit: tooLarge.Limit}
	}
	var lineTooLarge *SSELineTooLargeError
	if errors.As(err, &lineTooLarge) {
		return &SSELineTooLargeError{Limit: lineTooLarge.Limit}
	}
	var eventTooLarge *SSEEventTooLargeError
	if errors.As(err, &eventTooLarge) {
		return &SSEEventTooLargeError{Limit: eventTooLarge.Limit}
	}
	class := ExecutionFailure(err)
	if class == "" {
		class = ExecutionFailure(networkFailure(err))
	}
	var cause error
	switch {
	case errors.Is(err, context.Canceled):
		cause = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		cause = context.DeadlineExceeded
	case errors.Is(err, ErrRedirectBlocked):
		cause = ErrRedirectBlocked
	default:
		cause = errors.New("routed upstream request failed")
	}
	return executionFailure(class, cause)
}
