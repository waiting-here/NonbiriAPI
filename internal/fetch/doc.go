// Package fetch implements the upstream model discovery rail: fetching and
// parsing OpenAI-compatible GET /v1/models responses through the shared
// egress boundary, atomically replacing the per-(Endpoint, Key) cache,
// flagging failed endpoints, recording bounded user issues, and serving the
// user-station model list / manual refresh routes.
//
// Security boundaries:
//
//   - Every outbound request goes through the shared egress Stack (DNS and
//     SSRF checks, no redirects, no environment proxy, request timeout,
//     response ceiling, concurrency gate, cancellation); no direct
//     http.Client exists in this package.
//   - The upstream key is read only through the ownership-scoped ciphertext
//     accessor, decrypted with secret.Codec.Open, and the plaintext is
//     cleared as soon as the egress request returns. Ciphertext or
//     plaintext never reach metadata, responses, logs, issue messages, or the
//     cache.
//   - Upstream JSON is parsed with hard bounds (count, id/provider rune
//     limits, byte ceiling, duplicate/control/truncation rejection); failure
//     clears the combo cache, flags the endpoint, and writes a bounded,
//     sanitized user issue. Upstream identifiers and error text appear only
//     after the shared diagnostic boundary.
//   - Refresh work is bounded: a fixed worker pool with a bounded queue, a
//     per-combo in-flight dedup, and a per-user refresh frequency limiter.
//     There is no background periodic refresh (frozen contract).
package fetch
