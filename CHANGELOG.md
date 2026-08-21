# Changelog

All notable changes to NonbiriAPI are documented here.

The project is currently in alpha. The public compatibility promise is limited to the documented `v1.0.0-alpha.1` release contract.

## [Unreleased]

### Added

- Temporary (deadline-based) user bans alongside permanent bans, with lazy atomic expiry on read; administrators can ban for a preset or custom duration, and manual bans always clear the automatic-ban provenance flag.
- An explicit site timezone offset configuration (30-minute multiples within UTC-12:00…+14:00). It must be set before any day-keyed feature can enable, becomes permanently immutable once any timezone-keyed data exists, and is never silently defaulted.
- User-side editing of platform models (provider, model, route strategy, silent retry) and inline editing/reordering of endpoint-key bindings, with endpoint and key notes shown in selection dropdowns.
- Upstream usage is normalized into four mutually exclusive token buckets (uncached input, cache write, cache read, output) for both streaming and non-streaming responses. A malformed or self-contradictory upstream `usage` object degrades that request's usage to an explicit unknown state instead of fabricating values, and a contradictory stream usage can never be resurrected by a later chunk.
- Daily product-activity aggregation per user and site-wide, keyed by the configured site timezone, exposed through a new administrator activity endpoint. Distinct-active-user counts are suppressed (JSON `null`) on days with fewer than five distinct users while request/token aggregates stay numeric; activity rows are retained 400 days and account deletion recomputes affected days inside the deletion transaction.
- Request-log screens for both stations sharing one accessible UX (configurable columns, filters with quick time ranges, pagination persisted in the URL, and a detail drawer with focus trap, text-only diagnostics, and bounded copy). The user station lists its own models and current resource notes; the administrator station lists user ids, endpoint base URLs, and upstream models without any user-chosen naming.
- Request logs now persist the dispatch-time canonical base URL of the endpoint that actually served each committed request (owner-visible metadata; overlong or malformed values are sanitized at the persistence boundary instead of failing accounting).
- Administrator log CSV/JSON export endpoints whose cells are sanitized against spreadsheet formula injection and whose row/byte bounds fail closed instead of truncating.
- Account exports now carry four-bucket usage totals and the owner's daily activity summary (export schema v2).

### Changed

- The administrator endpoint overview now groups endpoints by their stored canonical base URL with server-side pagination, literal-substring filtering, and per-user expandable counts; the administrator models overview API and page were removed.
- Large numbers across both stations render with K/M/B/T abbreviations (exact value available in a tooltip), and token labels are unified as "input/output tokens" in English and Chinese.
- Platform model providers starting with the reserved charity prefix (`[公益]`) are rejected on create and update.
- Automatic model discovery now runs only after an endpoint's upstream path actually changes, after a disabled endpoint is enabled, or after an enabled key is added; empty, unchanged, and note-only endpoint updates no longer enqueue fetches.
- The user and administrator log list APIs were redesigned around strict single-value query parameters with exact-match filters; log paging clamps `page_size` to `[1,100]` on both stations.
- `/api/me/usage` and the administrator usage responses now include the four authoritative token buckets alongside the legacy prompt/completion totals.

### Security

- Bind each encrypted endpoint credential to its user, endpoint, key id, and canonical origin with a contextual AES-256-GCM envelope. Existing credentials migrate transactionally before serving traffic; fetch and forwarding no longer accept legacy envelopes at runtime.
- Prevent an endpoint with any stored upstream key from changing to a different canonical origin; changing origins now requires deleting the old keys and entering credentials again after the endpoint is updated.
- Enforce maintenance mode as a server-side authoritative gate that is applied atomically and live, instead of a client-side page notice; JSON and SSE errors carry a stable `source: platform|upstream` field while in-flight streams keep their correct error shape.
- Replace the publicly enumerable sequential-ID SHA-256 user identifiers with deployment-scoped, purpose-bound HMAC-SHA-256 identifiers that are stable within a deployment, differ across deployments, and carry no legacy v1 fallback.
- Create the SQLite database, WAL, and shared-memory files with owner-only filesystem permissions.

## [1.0.0-alpha.1] - 2026-08-19

### Added

- Self-hosted Go service with embedded React user and administrator stations.
- Discord OAuth user authentication and environment-configured administrator authentication.
- User-owned API endpoints and endpoint keys with encrypted-at-rest upstream secrets.
- OpenAI-compatible model discovery, user-owned platform model names, endpoint-key bindings, ordered/random routing, and opt-in pre-commit retry.
- OpenAI-compatible `/v1/models` and `/v1/chat/completions` CallerKey APIs, including non-streaming and SSE responses.
- SSRF, DNS-rebinding, redirect, proxy, timeout, response-size, concurrency, cancellation, and stream-flow safeguards.
- Per-user/global RPM admission control and per-endpoint concurrency limits.
- Request metadata logs, usage accounting, retention cleanup, user issues, administrator alerts, account export, and account deletion.
- English and Chinese user/admin interfaces with responsive layouts, themes, and accessibility states.
- SQLite persistence using `modernc.org/sqlite`, encrypted secret storage, and single-binary `go:embed` deployment.

### Compatibility and limitations

- Only the `openai-compatible` connector is enabled in this alpha.
- Only `/v1/models` and `/v1/chat/completions` are exposed as OpenAI-compatible exit routes.
- The database schema is initialized idempotently but has no versioned migration framework in this alpha. Back up the database before every update.
- Discord OAuth, upstream success paths, and deployment-specific reverse-proxy behavior must be verified in the operator's staging environment.

### Changed

- Upgraded the web routing runtime from React Router 7.18.2 to 8.3.0.
- Raised the frontend build requirement from Node.js 22.12 to 22.22 to match React Router 8.

### Security

- Refuse unsafe cross-origin browser requests to cookie-authenticated user and administrator APIs, including requests between the sibling station hosts.
- Reject upstream credentials reflected across multiple semantic JSON/SSE string fragments, not only contiguous wire bytes.
- Apply one aggregate five-minute deadline across route resolution, silent retries, and backoff.
- Key administrator login throttling by the single configured account instead of attacker-controlled candidate usernames.
- Purge expired sessions at startup and during the existing six-hour maintenance sweep.
- Create missing database directories owner-only and align the systemd/key-file guidance with the runtime's strict permission checks.

[1.0.0-alpha.1]: https://github.com/waiting-here/NonbiriAPI/releases/tag/v1.0.0-alpha.1
