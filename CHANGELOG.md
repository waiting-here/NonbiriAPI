# Changelog

All notable changes to NonbiriAPI are documented here.

The project is currently in alpha. The public compatibility promise is limited to the documented `v1.0.0-alpha.1` release contract.

## [Unreleased]

### Added

- Temporary (deadline-based) user bans alongside permanent bans, with lazy atomic expiry on read; administrators can ban for a preset or custom duration, and manual bans always clear the automatic-ban provenance flag.
- An explicit site timezone offset configuration (30-minute multiples within UTC-12:00…+14:00). It must be set before any day-keyed feature can enable, becomes permanently immutable once any timezone-keyed data exists, and is never silently defaulted.
- User-side editing of platform models (provider, model, route strategy, silent retry) and inline editing/reordering of endpoint-key bindings, with endpoint and key notes shown in selection dropdowns.

### Changed

- The administrator endpoint overview now groups endpoints by their stored canonical base URL with server-side pagination, literal-substring filtering, and per-user expandable counts; the administrator models overview API and page were removed.
- Large numbers across both stations render with K/M/B/T abbreviations (exact value available in a tooltip), and token labels are unified as "input/output tokens" in English and Chinese.
- Platform model providers starting with the reserved charity prefix (`[公益]`) are rejected on create and update.
- Automatic model discovery now runs only after an endpoint's upstream path actually changes, after a disabled endpoint is enabled, or after an enabled key is added; empty, unchanged, and note-only endpoint updates no longer enqueue fetches.

### Security

- Bind each encrypted endpoint credential to its user, endpoint, key id, and canonical origin with a contextual AES-256-GCM envelope. Existing credentials migrate transactionally before serving traffic; fetch and forwarding no longer accept legacy envelopes at runtime.
- Prevent an endpoint with any stored upstream key from changing to a different canonical origin; changing origins now requires deleting the old keys and entering credentials again after the endpoint is updated.

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
