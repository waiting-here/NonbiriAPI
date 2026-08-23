// Package endpoint implements the user-scoped Endpoint and EndpointKey CRUD
// service and its mountable HTTP handler.
//
// It is the first persistence path for user upstream endpoints, so the
// ownership, SSRF, connector, secret, and cap invariants are enforced here
// rather than retrofitted later:
//
//   - Ownership: every read/write filters by the authenticated user id in SQL
//     (cross-user access is indistinguishable from not_found; no read-then-write).
//   - base_url: the only canonicalization boundary is internal/egress's
//     ValidateBaseURL. This package never re-parses or simplifies URLs.
//   - Connector: an authoritative Registry rejects unknown connector types
//     with no silent protocol fallback (alpha: openai-compatible only).
//   - Secret: the repository allocates the key id and seals the upstream key
//     with its authenticated user/endpoint/key/origin context in one database
//     transaction. Plaintext never enters SQL, logs, errors, responses, or
//     cache. Listings expose only persisted head/tail display fragments, never
//     the ciphertext or plaintext.
//   - Cap: endpoint creation does an atomic count-then-insert inside one
//     transaction against min(global default, per-user override).
//   - Cascade/invalidation: deleting an endpoint or key relies on the schema's
//     ON DELETE CASCADE to drop fetched_models and model_bindings immediately.
//
// The handler does not mount itself; a constructor is provided for the
// integration rail. User identity is injected via an IdentityResolver (wired by
// the identity rail from its session context), never read from a hidden button
// or untrusted header. A narrow FetchHook boundary is invoked after a
// committed endpoint save/edit or key add so the model-fetch rail (J) can fetch
// upstream models without this package implementing any network fetch.
package endpoint
