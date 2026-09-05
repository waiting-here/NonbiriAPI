# Changelog

All notable changes to NonbiriAPI are documented here.

Each version entry describes its source and compatibility boundary; a release tag is not an upgrade authorization.

## [Unreleased]

### Added

- Personal credit history with reason, time and income/expense filters, adjustable page sizes, direct page navigation, and links to the account's own request logs. Donation rewards do not expose another caller's requests.

### Fixed

- Periodic maintenance no longer finalizes active API requests as interrupted startup work, which could incorrectly record a 502 while the response continued streaming.
- Restored configured short-charity-request penalties, automatic bans and suspensions, and RPM automatic bans. Credit penalties and safe rejection logs commit together. Accounts without an active API key can also be banned successfully.
- Donation reviews display correctly for stewards and for approvals with an empty note. Administrator source groups have a separate tab.
- Request details use vertical attempt cards and wrap long fields on narrow screens.
- User and site usage totals now update atomically when requests finish. Existing uninitialized totals are repaired once from retained request logs; logs already removed by retention cannot be reconstructed.
- Account event streams release idle write deadlines and reconnect after session refreshes without retaining stale page subscriptions.
- Mobile navigation keeps its full-height background and locks page scrolling. Wider desktop layouts and responsive fishing tables keep leaderboard results visible.
- API access and personal request logs are reachable from the main navigation. Endpoint creation supports adding several keys, connection lists show notes, and model checks update automatically.
- Administrator settings retain drafts and save related fields together, with dependency guidance, conflict detection, and no revision increase for unchanged values.
- Game anonymity controls are available beside leaderboards and remain consistent after completing the RPS tutorial. Fishing uses the existing illustrated scene with a stationary ripple effect.

### Changed

- LinkLink uses distinct pictures and animated connections along the confirmed match path, with a stable board position. Active games use less space for explanatory text on mobile.
- Fishing catch effects reflect rarity. RPS adds gesture reveals, tie progress, and a visible hidden ending after six consecutive free ties. Viewed results remain on the current page until dismissed or another game begins.
- Simplified user-facing copy and reduced internal configuration details in routine settings workflows.

## [1.0.0-beta.1]

### Added

- Database Generation 2 with a fresh-only, manifest-validated schema; a central double-entry credit ledger and shared pools; crash-recoverable worker checkpoints; strict mutation replay; and bounded legal holds.
- Bilingual announcements, daily welfare, the Thursday pooled activity, account activity summaries, and shared user-station SSE updates with replay/gap recovery.
- LinkLink and server-authoritative three-player Rock Paper Scissors alongside batched Pond Fishing, with persistent recovery, privacy-aware leaderboards, pending-result acknowledgement, and account-lifetime aggregate fun statistics.
- Public credential-theft reporting with indistinguishable accepted responses, live-key plus time-bounded donation-tombstone matching, resumable administrator review, and safe donation lineage inspection.
- Administrator-managed mainstream channel templates, strict mainstream/custom endpoint creation, immutable endpoint provenance snapshots, and a user-facing endpoint creation guide.
- Per-donation-key authorization and effective expiry, same-channel mainstream auto-approval, account-wide key status views, and administrator-controlled bilingual donation guidance.
- Public charity capability lists currently routable enabled models with exact base pricing, effective promotional pricing and promotion windows, without revealing donated resources. Administrator and level-5 management views retain the rolling success count and rate for the most recent 100 completed calls.
- Bilingual React pages for the new activities, announcements, games, reports, legal holds, mainstream channels, donation-key overview, and recovery states. The game center now ships three original local illustrations created with ChatGPT assistance.

### Changed

- Account export is schema version 4. It adds safe endpoint origin, authorized/effective donation-key expiry and source provenance, activity/game state, while retaining hard collection and total-size bounds and excluding secrets, reports, other users, internal fingerprints, and administrative material.
- Donation routing filters eligibility and connector capability before freezing a cryptographically random, expiry-weighted, without-replacement candidate order. Entropy failure returns `service_unavailable` before any request, reservation, claim, or ledger write.
- Debug Hub uses a version-2 bounded SSE protocol with explicit dry/live outcomes, safe stop/replace cancellation, and no persisted request or response content.
- The API uses cursor pagination, strict closed JSON objects, explicit revisions, and idempotency keys for state-changing operations. Removed alpha routes have no compatibility shim.
- Frontend dependencies include `@eslint/js` 10.0.1, Vite 8.2.2, jsdom 30.0.1, and Testing Library React 16.3.3; TypeScript remains on 5.9.3.
- GitHub Actions dependencies are pinned to immutable commits for their documented release tags.

### Compatibility and deployment

- Beta.1 accepts only a completely absent database set or a validated Generation 2 database with SQLite `application_id=0x4E425249` and `user_version=2`. Alpha and Generation 1 databases are not migrated or imported.
- Upgrading from an earlier prerelease requires a deliberate fresh cutover after a verified complete snapshot. Rollback requires restoring the matching database/sidecars, release, environment, master key, unit, manifest, and checksums together.
- Startup environment-variable names are unchanged from alpha.3, but a fresh cutover resets every database-backed runtime setting and requires operator review or re-entry before opening the instance.
- The release is source-first and targets Linux/amd64. A `-tags dist -trimpath` build is required after building both embedded web stations.

### Security and privacy

- Endpoint-key deletion is claim-first, report-locked resources fail closed, and donated credentials are removed after claim settlement; an irreversible report fingerprint is retained for at most 90 days after a donation key ends so later theft reports can still match.
- Donation provenance, authorized expiry, terminal state, report-match deadline, and report fingerprint follow one-way database invariants. Missing physical keys make report approval a safe no-op while preserving the case decision and lineage.
- Maintenance, administrator, and live level-5 permissions are rechecked in the final transaction. Cross-station, owner, secret, diagnostic, stream, resource, and account-deletion boundaries remain enforced.

## [1.0.0-alpha.3] - 2026-08-28

### Added

- An `anthropic-compatible` upstream connector behind the existing OpenAI-compatible Chat Completions ingress, including strict text, image, tool, sampling, streaming, stop-reason, model-discovery, and four-bucket usage translation. Calls that omit both token-limit fields use the nullable administrator default, whose built-in fallback is 65,536 and is not a cap.
- A typed site-configuration catalog and raw/effective projections for endpoint, RPM, and per-user in-flight concurrency limits. The built-in concurrency fallback is 5; explicit values may be above or below defaults while remaining inside their independent hard ranges.
- Two disabled-by-default OpenAI-only experimental policies: an owner-controlled physical-key option that overwrites or inserts `store:false`, and a logical-model option that converts structured tool calls to a bounded text format and restores only complete, real tool-call/result pairs.
- A memory-only user Debug Hub. Every session starts in dry-run mode, live observation requires a one-time 60-second challenge plus confirmation, and captured request/response projections are bounded, redacted, never persisted, and detached from the real caller's flow control.
- A modular server-authoritative game framework and Pond Fishing. Starts are idempotent, entry and settlement accounting are transactional, abandoned paid rounds settle automatically, results require an explicit acknowledgement, and privacy-aware single-catch and 30-day payout leaderboards can show a user's current nickname, static Discord CDN avatar, and level-4 badge only when that user opts in.
- Frontend Vitest/React Testing Library and Playwright foundations, route-level game/debug data modules, local SVG/CSS fishing artwork, and expanded responsive, keyboard, reduced-motion, bilingual, theme, and browser acceptance coverage.

### Changed

- The database is now a fresh-only generation identified by SQLite `application_id=0x4E425249` and `user_version=1`. Existing or malformed files and unexpected sidecars are validated without modifying the source and refused before a writable open; fresh databases seed maintenance on, registration off, and games off.
- Public admission now acquires one per-user in-flight permit before RPM reservation and request parsing. A concurrency denial creates no RPM hit or automatic penalty, and one logical request retains one permit across silent retries.
- Account export schema is version 3 and includes the new limit, policy, guild-profile, game, and settlement-correlation fields while continuing to exclude credentials, ciphertext, request/response content, Debug Hub captures, and cross-party identities.
- Account ban and deletion now close or invalidate in-flight user work across the forwarding, debug, and game boundaries before lifecycle mutation; game settlement, retention, deletion, and late-write paths are integrated into the central maintenance/lifecycle flow.
- Frontend source builds now require Node.js 22.22.3 or newer with npm 12.0.1 so npm can bootstrap reliably before the normal install and build gates.

### Security and privacy

- `store:false` is explicitly a best-effort upstream request, not a guarantee of zero retention: an upstream may ignore it or reject the field. Tool-call flattening can break normal structured tool workflows and is intended only for a specific compatibility need.
- Debug dry runs perform no candidate selection, credential access, DNS/egress, charity reservation, usage accounting, or persistent request/activity logging. Live observation exposes only caller-visible, sanitized projections and never headers, credentials, internal resource identifiers, base URLs, safety pseudonyms, or raw upstream diagnostics.
- Game costs and payouts use the same spendable credit balance as check-in and donor rewards. Game events never change the cumulative donor-reward statistic; anonymous leaderboard rows omit every identity field.
- Anthropic response credential-reflection detection now scopes exact-match fingerprints with a short-lived per-attempt HMAC-SHA-256 key and clears keyed state after use; literal, decoded-JSON, and cross-fragment rejection behavior is unchanged.

## [1.0.0-alpha.2] - 2026-08-22

### Added

- Temporary (deadline-based) user bans alongside permanent bans, with lazy atomic expiry on read; administrators can ban for a preset or custom duration, and manual bans always clear the automatic-ban provenance flag.
- An explicit site timezone offset configuration (30-minute multiples within UTC-12:00…+14:00). It must be set before any day-keyed feature can enable, becomes permanently immutable once any timezone-keyed data exists, and is never silently defaulted.
- User-side editing of platform models (provider, model, route strategy, silent retry) and inline editing/reordering of endpoint-key bindings, with endpoint and key notes shown in selection dropdowns.
- Upstream usage is normalized into four mutually exclusive token buckets (uncached input, cache write, cache read, output) for both streaming and non-streaming responses. A malformed or self-contradictory upstream `usage` object degrades that request's usage to an explicit unknown state instead of fabricating values, and a contradictory stream usage can never be resurrected by a later chunk.
- Daily product-activity aggregation per user and site-wide, keyed by the configured site timezone, exposed through a new administrator activity endpoint. Distinct-active-user counts are suppressed (JSON `null`) on days with fewer than five distinct users while request/token aggregates stay numeric; activity rows are retained 400 days and account deletion recomputes affected days inside the deletion transaction.
- Request-log screens for both stations sharing one accessible UX (configurable columns, filters with quick time ranges, pagination persisted in the URL, and a detail drawer with focus trap, text-only diagnostics, and bounded copy). The user station lists its own models and current resource notes; the administrator station lists user ids, endpoint base URLs, and upstream models without any user-chosen naming.
- Request logs now persist the dispatch-time canonical base URL of the endpoint that actually served each committed request (owner-visible metadata; overlong or malformed values are sanitized at the persistence boundary instead of failing accounting).
- Administrator log CSV/JSON export endpoints whose cells are sanitized against spreadsheet formula injection and whose row/byte bounds fail closed instead of truncating.
- Account exports now carry four-bucket usage totals, the owner's credit ledger, daily activity summary, and check-in history (export schema v2).
- Per-user economy balances: a signed consumption balance and a cumulative donor-reward balance on the account row, plus an append-only credit ledger whose audit row commits in the same transaction as every balance change. Administrator credit adjustments take canonical decimal-string deltas with a mandatory operation id and reason; retrying an operation id returns the first application's result instead of applying twice, and system and client operation namespaces are mutually isolated.
- Donation-driven user levels: a persisted automatic-level high-water mark that is raised lazily by a conditional update when the donor-reward balance reaches an administrator-configured threshold (and is never downgraded, including after threshold raises or negative corrections), a nullable manual level override (1..5; resetting it restores the automatic mark), threshold site-config keys cross-validated as a strictly increasing chain inside the same transaction as the write, and balance/level projections on `/api/session` and `/api/me`.
- A level-5 co-management prefix `/api/steward/` on the user station that re-resolves the live effective level on every request (a demotion or manual reset revokes an existing session on its next request), refuses the administrator station and administrator sessions in both directions, and now mounts the separated charity-management routes.
- A level-5 full-site request-log route and UI at `GET /api/steward/logs` on the user station, sharing the administrator log shape and bounded filters but applying a steward de-privacy projection that blanks the donor's endpoint key id, base URL, and upstream model id on charity rows. Resource filters run against that visible projection, so guessed donor values cannot select charity rows; the co-manager sees site-wide log/activity without donor resources, user-chosen model names, notes, Discord identities, or an export surface.
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
- Charity dispatch now passes the selected full model name into its final candidate revalidation and projects the endpoint/key identifiers required by the contextual credential envelope; this fixes a parameter/projection omission that otherwise stopped a real charity attempt before decryption and dialing.
- Wide log and management tables now stay inside their horizontal scroll container instead of expanding the whole page on narrow viewports.
- Both stations provide an explicit initial loading fallback for lazy page routes, avoiding the React Router hydration warning during a direct page load.

### Changed

- The configured site timezone offset is now permanently frozen by the first check-in or activity write itself (in the same transaction as that write), so retention cleanup or account deletion emptying the day-keyed tables can never make the offset mutable again.
- The administrator endpoint overview now groups endpoints by their stored canonical base URL with server-side pagination, literal-substring filtering, and per-user expandable counts; the administrator models overview API and page were removed.
- Large numbers across both stations render with K/M/B/T abbreviations (exact value available in a tooltip), and token labels are unified as "input/output tokens" in English and Chinese.
- The administrator user table now shows level and balance columns with row actions reduced to an expandable management panel and delete; the previous inline limits form and single-step ban dialog moved into the panel.
- Platform model providers starting with the reserved charity prefix (`[公益]`) are rejected on create and update.
- Automatic model discovery now runs only after an endpoint's upstream path actually changes, after a disabled endpoint is enabled, or after an enabled key is added; empty, unchanged, and note-only endpoint updates no longer enqueue fetches.
- The user and administrator log list APIs were redesigned around strict single-value query parameters with exact-match filters; log paging clamps `page_size` to `[1,100]` on both stations.
- `/api/me/usage` and the administrator usage responses now include the four authoritative token buckets alongside the legacy prompt/completion totals.
- Frontend economic-number tests now run through the package-level `npm test` command and in CI; both stations load page modules as route-level chunks so the production build stays below the configured large-chunk warning without relaxing the threshold.

### Security

- Bind each encrypted endpoint credential to its user, endpoint, key id, and canonical origin with a contextual AES-256-GCM envelope. Existing credentials migrate transactionally before serving traffic; fetch and forwarding no longer accept legacy envelopes at runtime.
- Prevent an endpoint with any stored upstream key from changing to a different canonical origin; changing origins now requires deleting the old keys and entering credentials again after the endpoint is updated.
- Enforce maintenance mode as a server-side authoritative gate that is applied atomically and live, instead of a client-side page notice; JSON and SSE errors carry a stable `source: platform|upstream` field while in-flight streams keep their correct error shape.
- Replace the publicly enumerable sequential-ID SHA-256 user identifiers with `nbu_v3_` purpose-bound HMAC-SHA-256 pseudonyms scoped to the consumer user and dispatch-time canonical upstream origin. Paths do not affect the value, while scheme, canonical host, or effective-port changes do; personal and charity calls by the same consumer use the same value only when they reach the same origin. Caller values are overwritten, invalid/non-canonical origins fail closed, and neither v2 nor the enumerable v1 form is sent. The v3 rollout therefore rotates the prior development identifier once and does not carry old upstream risk history automatically.
- Create the SQLite database, WAL, and shared-memory files with owner-only filesystem permissions.
- Charity dispatch now compensates a zero-byte client sink failure (the first response-body write delivers no bytes, e.g. an HTTP/2 stream reset or early client disconnect) by reverting the reservation from `dispatched` back to `reserved`, so the user is refunded and the donated-key cap is freed instead of leaving a stuck `dispatched` row that recovery would later charge as unknown usage. The crash window stays conservative (the compare-and-set still runs before the first delegated byte), and a real short write that delivered bytes keeps the row `dispatched` and settles under the commit formula.
- The per-donation-key RPM admission limiter now reclaims idle entries (a time-aware release and a lazy sweep drop entries with no in-flight slot and no live RPM event within roughly one minute), enforces a hard entry cap that fails closed instead of growing unbounded, and follows donation/key enable, disable, deletion, approval, and expiry changes immediately. Closing a key refuses new admission while preserving in-flight and RPM accounting; re-enabling the same key reopens that state instead of resetting its rate history.
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

[1.0.0-beta.1]: https://github.com/waiting-here/NonbiriAPI/releases/tag/v1.0.0-beta.1
[1.0.0-alpha.3]: https://github.com/waiting-here/NonbiriAPI/releases/tag/v1.0.0-alpha.3
[1.0.0-alpha.2]: https://github.com/waiting-here/NonbiriAPI/releases/tag/v1.0.0-alpha.2
[1.0.0-alpha.1]: https://github.com/waiting-here/NonbiriAPI/releases/tag/v1.0.0-alpha.1
