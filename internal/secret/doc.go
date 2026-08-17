// Package secret protects recoverable upstream credentials with a process-wide
// master key.
//
// Ciphertexts use the strict textual envelope
//
//	nbsec:v1:aes-256-gcm:<raw-base64url nonce>:<raw-base64url ciphertext-and-tag>
//
// AES-256-GCM authenticates the versioned algorithm header as associated data.
// Every Seal call obtains a fresh 96-bit nonce from crypto/rand. Open rejects
// non-canonical encodings, unknown envelopes, malformed lengths, trailing data,
// and authentication failures without attempting a plaintext fallback.
//
// Only Seal output is suitable for persistence. Open deliberately returns a
// byte slice so obtaining plaintext is an explicit operation. Callers must keep
// that slice out of logs, errors, responses, caches, and long-lived objects and
// should clear it as soon as the outbound request has been constructed. Go's
// garbage collector and runtime may retain copies, so Close and caller-side
// clearing are best-effort reductions of exposure, not a zeroization guarantee.
package secret
