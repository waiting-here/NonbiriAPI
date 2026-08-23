package db

import "errors"

// Shared repository sentinels for ownership, uniqueness, endpoint limits and
// runtime configuration. None of these carry request or secret material.
var (
	// ErrNotFound is returned when an ownership-scoped row does not exist. All
	// repositories share this sentinel so cross-user and missing resources are
	// indistinguishable at the API boundary.
	ErrNotFound = errors.New("resource not found")
	// ErrConflict is returned for a uniqueness collision across any domain.
	ErrConflict = errors.New("resource conflict")

	// ErrEndpointCap is returned when a user's endpoint count has reached the
	// effective cap (min of the global default and the per-user override) and a
	// new endpoint was refused. Existing endpoints are retained. The repository
	// actually returns a *CapError wrapping this sentinel so the API boundary can
	// surface the exact effective cap and resource name; the plain sentinel
	// exists for errors.Is matching in tests.
	ErrEndpointCap = errors.New("db: endpoint cap reached")

	// ErrEndpointKeyCap is returned when the key count on one endpoint has
	// reached the effective cap (the default_endpoint_key_limit site_config
	// value or DefaultEndpointKeyLimit) and a new key was refused. The
	// repository actually returns a *CapError wrapping this sentinel so the
	// API boundary can surface the exact effective cap and resource name; the
	// plain sentinel exists for errors.Is matching in tests.
	ErrEndpointKeyCap = errors.New("db: endpoint key cap reached")
	// ErrModelCap is returned when a user's platform-model count has reached
	// the effective cap (the default_model_limit site_config value or
	// DefaultModelLimit) and a new model was refused. A *CapError wraps it.
	ErrModelCap = errors.New("db: platform model cap reached")
	// ErrBindingCap is returned when the binding count on one platform model
	// has reached the effective cap (the default_binding_limit site_config
	// value or DefaultBindingLimit) and a new binding was refused. A *CapError
	// wraps it.
	ErrBindingCap = errors.New("db: model binding cap reached")

	// ErrDonationKeyClaimConflict reports that a physical endpoint key is
	// already claimed by ANOTHER donation's key while that donation is pending
	// or approved+enabled. The claim constraint decided the race; nothing was
	// merged and no limit was stacked.
	ErrDonationKeyClaimConflict = errors.New("db: endpoint key is already claimed by another donation")
	// ErrInvalidValue reports that a caller-supplied value failed a bounded
	// shape/range/membership rule decidable from the value ALONE (length,
	// charset, sign, enum membership, request-local interval sanity). API
	// boundaries map it to invalid_request; ErrConflict stays reserved for
	// refusals that depend on current stored state or another row.
	ErrInvalidValue = errors.New("db: invalid value")
	// ErrResourceInActiveDonation reports that an endpoint/key deletion was
	// refused because a pending or approved+enabled donation still references
	// it. The API boundary maps it to a stable conflict envelope.
	ErrResourceInActiveDonation = errors.New("db: resource is referenced by an active donation")
	// ErrCharityTokenReserveMissing reports that a per-token charity model was
	// enabled (or created enabled) while charity_token_reserve_milli is unset
	// or non-positive: the token mode fails closed until the administrator
	// configures an explicit reserve price.
	ErrCharityTokenReserveMissing = errors.New("db: charity token reserve price is not configured")

	// ErrInvalidSiteConfig is returned when a site_config value used by a
	// cap invariant is present but malformed (non-integer or negative where a
	// non-negative integer is required).
	ErrInvalidSiteConfig = errors.New("db: invalid site config")

	// ErrEndpointCredentialUnavailable collapses invalid stored envelope or
	// authenticated-context projections without carrying any protected value.
	ErrEndpointCredentialUnavailable = errors.New("db: endpoint credential unavailable")
)

// Stable resource names carried by CapError and the resource_limit_exceeded
// envelope. They are short machine identifiers, never request or secret
// material.
const (
	ResourceEndpoint    = "endpoint"
	ResourceEndpointKey = "endpoint_key"
	ResourceModel       = "model"
	ResourceBinding     = "binding"
	ResourceDonation    = "donation"
)

// CapError reports that a per-parent resource count reached its configured cap
// and a new row was refused. It wraps one of ErrEndpointCap / ErrEndpointKeyCap /
// ErrModelCap / ErrBindingCap (so errors.Is still matches the sentinel) and
// carries the resource name and the exact effective cap in effect at the
// refusal, so the API boundary can surface a stable resource_limit_exceeded
// envelope with the limit and resource fields without a second, racy read. It
// never carries request or secret material.
type CapError struct {
	Resource string
	Limit    int
	target   error
}

// Error implements the error interface.
func (e *CapError) Error() string { return "db: " + e.Resource + " cap reached" }

// Unwrap allows errors.Is to match the underlying cap sentinel.
func (e *CapError) Unwrap() error { return e.target }

// newCapError builds a CapError wrapping target with the given resource name
// and effective cap.
func newCapError(target error, resource string, limit int) *CapError {
	return &CapError{Resource: resource, Limit: limit, target: target}
}
