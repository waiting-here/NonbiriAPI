package db

import "errors"

// Endpoint-rail sentinel errors. The package-wide not-found sentinel
// ErrNotFound is owned by the identity rail (users.go) and is reused here so
// cross-user or missing endpoint access maps to the same not_found contract
// everywhere; this file only adds the endpoint-cap and site-config sentinels
// that the identity rail does not need. None of these carry request or secret
// material.
var (
	// ErrNotFound is returned when an ownership-scoped row does not exist. All
	// repositories share this sentinel so cross-user and missing resources are
	// indistinguishable at the API boundary.
	ErrNotFound = errors.New("resource not found")

	// ErrEndpointCap is returned when a user's endpoint count has reached the
	// effective cap (min of the global default and the per-user override) and a
	// new endpoint was refused. Existing endpoints are retained.
	ErrEndpointCap = errors.New("db: endpoint cap reached")

	// ErrInvalidSiteConfig is returned when a site_config value used by an
	// endpoint invariant is present but malformed (non-integer or negative
	// where a non-negative integer is required).
	ErrInvalidSiteConfig = errors.New("db: invalid site config")
)
