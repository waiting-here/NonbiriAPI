package egress

import (
	"net/url"
	"sort"
)

// CloudflareRouteStatus exposes only the loaded route's non-secret settings.
type CloudflareRouteStatus struct {
	SourceBaseURL            string `json:"source_base_url"`
	GatewayOrigin            string `json:"gateway_origin"`
	CredentialHeader         string `json:"credential_header"`
	CredentialQueryParameter string `json:"credential_query_parameter"`
	RetainCredentialHeader   bool   `json:"retain_credential_header"`
}

type CloudflareStatus struct {
	Enabled bool                    `json:"enabled"`
	Routes  []CloudflareRouteStatus `json:"routes"`
}

// CloudflareStatus snapshots the immutable routes used by this Stack. It does
// not inspect environment variables or contact the gateway or providers.
func (s *Stack) CloudflareStatus() CloudflareStatus {
	routes := make([]CloudflareRouteStatus, 0, len(s.routes))
	for _, route := range s.routes {
		origin := &url.URL{Scheme: route.gateway.Scheme, Host: route.gateway.Host}
		routes = append(routes, CloudflareRouteStatus{
			SourceBaseURL:            routeStatusURL(route.source),
			GatewayOrigin:            routeStatusURL(origin),
			CredentialHeader:         route.credentialHeader,
			CredentialQueryParameter: route.credentialQuery,
			RetainCredentialHeader:   route.retainCredentialHeader,
		})
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].SourceBaseURL < routes[j].SourceBaseURL })
	return CloudflareStatus{Enabled: len(routes) > 0, Routes: routes}
}

func routeStatusURL(value *url.URL) string {
	defaultPort := value.Scheme == "https" && value.Port() == "443" || value.Scheme == "http" && value.Port() == "80"
	return displayBaseURL(value, !defaultPort)
}
