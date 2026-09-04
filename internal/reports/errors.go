// Package reports owns public credential-theft acceptance, report cases,
// fingerprint suspension, and the bounded report workers.
package reports

import "errors"

var (
	ErrInvalidRequest = errors.New("reports: invalid request")
	ErrUnauthorized   = errors.New("reports: unauthorized")
	ErrForbidden      = errors.New("reports: forbidden")
	ErrNotFound       = errors.New("reports: not found")
	ErrConflict       = errors.New("reports: conflict")
	ErrRateLimited    = errors.New("reports: rate limited")
	ErrUnavailable    = errors.New("reports: unavailable")
	ErrInvariant      = errors.New("reports: invariant violation")
	ErrClosed         = errors.New("reports: closed")
)
