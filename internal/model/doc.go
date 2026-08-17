// Package model implements the user-station platform model and model-binding
// CRUD domain (Phase 2 track K): list/get/create/patch/delete of a user's
// platform models and their upstream bindings.
//
// The external name of a platform model is the opaque string "provider/model".
// provider and model are free strings that allow '/' but are otherwise bounded
// (no control characters, no leading/trailing whitespace, at most 64 runes
// each); the repository stores and matches only the derived full_name so no
// code ever splits the name back into fields. Route strategy is strictly
// ordered|random. A model may hold zero bindings (a draft); calling it later
// maps to 503 by the forwarding rail.
//
// Bindings are validated atomically in SQL: the model must belong to the
// caller, the endpoint key must be enabled on an enabled endpoint of the same
// caller, and upstream_model_id must exist in that key's fetched-model cache
// (the J rail owns the cache; this package only consumes it read-only).
// Bindings therefore can never be pointed at another user's resources, at a
// disabled key/endpoint, or at an upstream id the cache does not vouch for.
//
// The HTTP boundary (handler.go) is user-session-only: identity comes from an
// injected resolver, and SessionIdentity derives it from auth.UserFromContext,
// which never accepts a caller-key (bearer) principal.
package model
