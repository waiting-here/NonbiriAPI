# Changelog

All notable changes to NonbiriAPI are documented here.

The project is currently in alpha. The public compatibility promise is limited to the documented `v1.0.0-alpha.1` release contract.

## [Unreleased]

- No unreleased changes yet.

## [1.0.0-alpha.1] - 2026-08-17

### Added

- Self-hosted Go service with embedded React user and administrator stations.
- Discord OAuth user authentication and environment-configured administrator authentication.
- User-owned API endpoints and endpoint keys with encrypted-at-rest upstream secrets.
- OpenAI-compatible model discovery, platform model aliases, endpoint-key bindings, ordered/random routing, and opt-in pre-commit retry.
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

[Unreleased]: https://github.com/waiting-here/NonbiriAPI/compare/v1.0.0-alpha.1...HEAD
[1.0.0-alpha.1]: https://github.com/waiting-here/NonbiriAPI/releases/tag/v1.0.0-alpha.1
