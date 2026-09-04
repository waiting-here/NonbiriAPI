// Package secret protects recoverable upstream credentials with a process-wide
// AES-256 master key.
//
// Persisted Generation 2 endpoint credentials use one strict contextual
// envelope:
//
//	nbsec:v2:aes-256-gcm:<raw-base64url nonce>:<raw-base64url ciphertext-and-tag>
//
// The authenticated data is exactly
// F("generation_two") || F("endpoint-key-secret/v2") || FB(context_id),
// where each F/FB field is prefixed by its four-byte big-endian byte length.
// The context_id is the random 16-byte per-secret cryptographic context; it
// is not an owner, endpoint, key, query, log, or export identifier.
//
// Only SealForGenerationTwoContext output is suitable for new persistence.
// OpenForGenerationTwoContext deliberately returns a byte slice so obtaining
// plaintext is explicit. Callers must keep that slice out of logs, errors,
// responses, caches, and long-lived objects, and should clear it as soon as
// the outbound request has been constructed. Go's garbage collector and
// runtime may retain copies, so Close and caller-side clearing are best-effort
// reductions of exposure, not a zeroization guarantee.
package secret
