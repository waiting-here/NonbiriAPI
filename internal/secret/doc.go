// Package secret protects recoverable upstream credentials with a process-wide
// master key.
//
// Persisted endpoint credentials use the strict contextual envelope
//
//	nbsec:v2:aes-256-gcm:<raw-base64url nonce>:<raw-base64url ciphertext-and-tag>
//
// AES-256-GCM authenticates purpose, envelope version, user id, endpoint id,
// key id, and the egress-canonical origin through a length-prefixed associated-
// data encoding. Every SealForContext call obtains a fresh 96-bit nonce from
// crypto/rand. OpenForContext rejects legacy, unknown, non-canonical, malformed,
// oversized, or context-mismatched envelopes without a plaintext fallback.
//
// Seal/Open retain the legacy nbsec:v1 format for the startup migration
// boundary. Outbound fetch and forwarding code must use the contextual methods
// exclusively.
//
// Only SealForContext output is suitable for new persistence. OpenForContext
// deliberately returns a byte slice so obtaining plaintext is explicit.
// Callers must keep that slice out of logs, errors, responses, caches, and
// long-lived objects and should clear it as soon as the outbound request has
// been constructed. Go's garbage collector and runtime may retain copies, so
// Close and caller-side clearing are best-effort reductions of exposure, not a
// zeroization guarantee.
package secret
