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
- Per-user economy balances: a signed consumption balance and a cumulative donor-reward balance on the account row, plus an append-only credit ledger whose audit row commits in the same transaction as every balance change. Administrator credit adjustments take canonical decimal-string deltas with a mandatory operation id and reason; retrying an operation id returns the first application's result instead of applying twice, and system and client operation namespaces are mutually isolated.
- Donation-driven user levels: a persisted automatic-level high-water mark that is raised lazily by a conditional update when the donor-reward balance reaches an administrator-configured threshold (and is never downgraded, including after threshold raises or negative corrections), a nullable manual level override (1..5; resetting it restores the automatic mark), threshold site-config keys cross-validated as a strictly increasing chain inside the same transaction as the write, and balance/level projections on `/api/session` and `/api/me`.
- A level-5 co-management prefix `/api/steward/` on the user station that re-resolves the live effective level on every request (a demotion or manual reset revokes an existing session on its next request), refuses the administrator station and administrator sessions in both directions, and now mounts the separated charity-management routes.
- A level-5 full-site request-log route `GET /api/steward/logs` on the user station, sharing the administrator log shape and bounded filters but applying a steward de-privacy projection that blanks the donor's endpoint key id, base URL, and upstream model id on charity rows, so a level-5 co-manager sees site-wide log/activity without donor resources, user-chosen model names, notes, or Discord identities.
- Economy, level, and check-in surfaces on the user station home: exact server-resolved balances and effective level rendered from canonical decimal strings through the shared big-number formatter (negative balances keep their sign, tooltips carry the precise milli-credit figure), a daily check-in card whose availability, status, award range, and threshold all come from the server with refusal messages shown verbatim, and a level-5 co-management navigation entry that opens the steward charity-management frame.
- Administrator economy management on the users screen: expandable per-user panels with idempotent credit adjustments (canonical decimal-string deltas, mandatory reason, one operation id per confirmed submission reused across network retries), a manual-level tri-state control, and permanent/preset/custom-duration ban controls; the user table gains level and balance columns with compact display and exact milli-credit tooltips.
- A dedicated economy-and-level card on the administrator settings screen covering the timezone offset (with unset-state warning and immutability notice), the three-way check-in switch, award bounds, the check-in credits cap, and the level thresholds as display-credit inputs converted through bounded big-integer arithmetic; these typed editors replace the generic key list for their keys.
- Charity routing through donated keys: `[公益]`-prefixed models resolve against administrator-curated charity models only (disjoint from personal models by construction) while the site-wide switch is on. One call takes exactly one user credit pre-reserve in a single transaction that also conditionally reserves the donated key's usage cap; retries across donated keys atomically swap only the key reserve, and per-key concurrency, RPM and usage-cap limits gate each candidate without feeding auto-ban statistics. The first response body byte is the accounting boundary; settlement uses the price snapshot taken at reservation time under the frozen sum-then-ceiling formula (actual charges may drive a balance negative or cross a key cap), unknown usage keeps the reserve with zero donor reward, successful calls feed the daily activity rollup, and every state transition is an idempotent compare-and-set that crash recovery and account deletion converge from persisted snapshots alone.
- Administrator and level-5 steward charity management surfaces built on one shared component with strictly separated frames: donation review (approve/reject with notes), enable/disable and soft deletion with per-key limit/cap/enabled editing, charity model CRUD with bindings and four-bucket/per-request pricing plus discount windows, the site-wide switches behind confirmation dialogs, and the anti-abuse parameters; every operation refetches server state, a demotion clears the frame's cached data on its next request, and secrets, ciphertexts, user notes, and Discord identities are never rendered.
- Automatic anti-abuse policy: an effective per-user RPM denial threshold (default five within twenty-four hours, zero disables) triggers a timed automatic ban that atomically invalidates sessions and caller keys, and charity requests whose counted message text falls under a configurable Unicode-rune minimum are refused before dispatch with optional credit deduction (allowed to drive the balance negative), single or windowed bans, and a charity-only suspension that lazily expires and blocks nothing outside the charity rail. Only effective per-user RPM denials count; shared, global, egress, and per-key refusals never feed violation statistics. The sliding windows are bounded process-local state that fails safe when full and is never persisted.
- A user-station charity page: the price table behind a new `/api/charity/models` projection (four-bucket and per-request original plus currently discounted prices as canonical decimal strings with exact tooltips, undiscounted donor rewards, discount windows, last-100 success counts, server-resolved availability; empty while the switch is off, never leaking donated resources) with readable states for every refusal cause, plus self-service donation management (list with review status and notes, submission against existing keys or a nested new-endpoint form whose secret fields are cleared before the request, pending editing, soft deletion with readable conflicts).
- User-submitted charity donations: one donation binds one owned endpoint with at least one key (existing or freshly entered), requires a donor statement, and is reviewed as a whole by administrators or level-5 co-managers with an append-only audit trail. A physical upstream key can serve at most one active donation at a time, enforced by an atomic claim constraint re-synchronized inside every state transition; nested submissions create the endpoint and seal fresh secrets inside the submission transaction and roll back completely on failure; endpoints and keys referenced by an active donation cannot be deleted; account deletion converges claims before cascading. Charity models with per-request or four-bucket per-million-token pricing in milli-credits, limited-time discounts, bindings to donated keys, and a last-100-request success-rate ring buffer are managed through parallel administrator and steward route frames. Charity routing and donation intake default to closed, and token-priced models fail closed until an explicit reserve price is configured. Account exports gain a donations section with safe metadata only (export schema v2).
- Daily check-in on the user station: one check-in per user per site-local day, enforced by a database uniqueness constraint inside the single transaction that also applies the uniformly drawn random award (crypto/rand rejection sampling over the configurable inclusive milli-credit range), appends the `checkin_award` ledger row and records the activity contribution. A three-way administrator switch (enabled / level-gated ≥3 / disabled) and a check-in credits threshold (0 = none; reached balances below level 3 are refused without consuming the day; level ≥3 bypasses; awards are never truncated) govern admission, and an unset timezone or any other unavailable cause returns the identical feature-disabled envelope without revealing why. New stable error codes `already_checked_in` (409) and `checkin_cap_reached` (403).

- Account exports now include charity reservation summaries (consumer view: model/state/pricing/tokens without donated-resource identifiers) and donor reward summaries (model/reward/timestamps without consumer identity).
- Terminal charity reservations (committed/released) are cleaned by the periodic maintenance sweep after 400 days; in-flight reservations are never removed by age.

### Fixed

- Charity donation self-service routes (`/api/donations`) now apply user-session middleware, fixing a wiring omission that caused authenticated requests to receive 401.

### Changed

- The configured site timezone offset is now permanently frozen by the first check-in or activity write itself (in the same transaction as that write), so retention cleanup or account deletion emptying the day-keyed tables can never make the offset mutable again.
- The administrator endpoint overview now groups endpoints by their stored canonical base URL with server-side pagination, literal-substring filtering, and per-user expandable counts; the administrator models overview API and page were removed.
- Large numbers across both stations render with K/M/B/T abbreviations (exact value available in a tooltip), and token labels are unified as "input/output tokens" in English and Chinese.
- The administrator user table now shows level and balance columns with row actions reduced to an expandable management panel and delete; the previous inline limits form and single-step ban dialog moved into the panel.
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
- Charity dispatch now compensates a zero-byte client sink failure (the first response-body write delivers no bytes, e.g. an HTTP/2 stream reset or early client disconnect) by reverting the reservation from `dispatched` back to `reserved`, so the user is refunded and the donated-key cap is freed instead of leaving a stuck `dispatched` row that recovery would later charge as unknown usage. The crash window stays conservative (the compare-and-set still runs before the first delegated byte), and a real short write that delivered bytes keeps the row `dispatched` and settles under the commit formula.
- The per-donation-key RPM admission limiter now reclaims idle entries (a time-aware release and a lazy sweep drop entries with no in-flight slot and no live RPM event within roughly one minute), enforces a hard entry cap that fails closed instead of growing unbounded, and exposes a lifecycle forget hook used on donor account deletion so a closed key refuses new admits while preserving in-flight slot accounting.
- The frozen level-5 full-site log co-management capability is now wired through the user-station steward frame (it was previously only a documented mount point), closing the gap between the frozen requirement and the implementation.

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
