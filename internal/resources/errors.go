// Package resources owns user-scoped endpoints, physical endpoint keys,
// model catalogs, logical models, bindings, and caller-key metadata.
package resources

import "errors"

var (
	ErrInvalidRequest             = errors.New("resources: invalid request")
	ErrUnauthorized               = errors.New("resources: unauthorized")
	ErrForbidden                  = errors.New("resources: forbidden")
	ErrMaintenance                = errors.New("resources: maintenance")
	ErrNotFound                   = errors.New("resources: not found")
	ErrConflict                   = errors.New("resources: conflict")
	ErrResourceLocked             = errors.New("resources: resource locked")
	ErrResourceLimit              = errors.New("resources: resource limit exceeded")
	ErrUnavailable                = errors.New("resources: unavailable")
	errDiscoveryWorkerUnavailable = errors.New("resources: discovery worker unavailable")
)
