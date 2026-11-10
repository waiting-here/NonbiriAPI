package egress

import (
	"errors"
	"net/url"
	"strings"
)

// A complete standard catalog retains a route configured for its former
// base. Exact full-address routes take precedence in newURLClient. Other
// custom paths require an exact route; there is no general prefix matching.
func (s *Stack) discoveryRoute(catalog *url.URL) (*cloudflareRoute, error) {
	base := standardCatalogBase(catalog)
	if base == nil {
		return nil, nil
	}
	plain := s.routes[base.String()]
	base.RawPath += "/"
	base.Path += "/"
	slash := s.routes[base.String()]
	if plain != nil && slash != nil && plain != slash {
		return nil, errors.New("catalog matches conflicting Cloudflare base routes")
	}
	if plain != nil {
		return plain, nil
	}
	return slash, nil
}

// Standard catalogs share the inference base's concurrency bucket without a
// trailing slash. Custom catalog paths retain their complete-address bucket.
func standardCatalogBase(catalog *url.URL) *url.URL {
	path := strings.TrimRight(catalog.EscapedPath(), "/")
	suffix := "/models"
	if strings.HasSuffix(path, "/config") {
		suffix = "/config"
	} else if !strings.HasSuffix(path, suffix) {
		return nil
	}
	base := *catalog
	base.RawPath = strings.TrimSuffix(path, suffix)
	var err error
	base.Path, err = url.PathUnescape(base.RawPath)
	if err != nil {
		return nil
	}
	base.RawQuery, base.ForceQuery = "", false
	return &base
}
