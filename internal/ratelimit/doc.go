// Package ratelimit provides bounded, in-memory admission primitives for a
// single application instance.
//
// The primitives are deliberately independent of HTTP, authentication,
// persistence, and network address parsing. Callers supply already-resolved
// opaque identities, and all mutating admission operations are serialized so
// a check cannot be separated from the counter update by another caller.
// State is process-local and is reset when the process exits.
package ratelimit
