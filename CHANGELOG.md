# Changelog

All notable changes to NonbiriAPI are documented here.

Each version entry describes its source and compatibility boundary; a release tag is not an upgrade authorization.

## 1.1.0 — 2026-10-21

### Added

- Service connections with shared versioned credentials, independent model
  directories and multi-service entries. OpenAI-compatible and Gateway entries
  explicitly select native services; model bindings retain ordered alternative
  targets without multiplying retry slots or budgets.
- Per-service donation approval, with pending replacements alongside older
  approved versions. Credential rotation and explicitly added services require
  review while preserving the existing donated-key budget and consumption.
- Multi-entry mainstream presets with independent discovery suggestions and
  per-model entry mappings, shared by personal setup and donation workflows.
- Direct HTTP/SSE Responses, legacy completions, image editing/variation, audio,
  moderation and rerank operations, plus exact model lookup. Persistent resource
  APIs and all WebSocket/Realtime/WebRTC interfaces remain outside this version.
- Dedicated Chat and System One connectors and explicit Cohere, Jina, SiliconFlow
  and Voyage rerank adapters. System One remains separate from Decisions.
- Explicit AI SDK Gateway v3/v4 text, image generation, embeddings and rerank
  support, with complete native base URLs and no automatic version switching.
  Both versions support float/base64 embeddings; v4 adds common reasoning and
  embedding dimensions. Image output defaults to base64, and standard rerank
  responses retain unknown usage instead of inventing token counts. The existing
  default-off cost-attribution setting applies to all four services in both
  versions.
- Search-unit pricing and independent input/output modality prices for caller
  charges, donor rewards and long-context tiers, with standard-usage validation
  and aggregate-price fallback per side.
- Separate light/heavy charity RPM windows and user concurrency pools shared by
  personal and charity calls. Light defaults are 30 RPM and 15 concurrent calls;
  existing heavy defaults remain 60 RPM and 5 concurrent calls. Administrators
  and strictly higher-level stewards may adjust all five nullable user inference
  limits, including the global user RPM override.

- Per-model Cloudflare forwarding with a generic authenticated Worker, without
  per-upstream mappings. Personal models default to enabled and charity models
  to disabled, including existing models after upgrade. Legacy AI Gateway
  configuration remains compatible when no Worker is configured.
- Endpoint-level supplemental backoff rules shared by all charity keys at the
  same connector and complete base URL. Administrators and level-6 stewards can
  configure ordered exact error codes or literal message fragments, including
  provider-specific credit exhaustion responses.
- Read-only management visibility into the egress routes actually loaded by the
  process, without gateway secrets or private gateway paths.
- Documented Google AI Studio and Vertex AI OpenAI-compatible routing, with
  isolated regional/global path, authentication and streaming coverage.

- Managed session headers for personal models, with the same settings fields as
  charity models. Both support UUID v4, hex and OpenCode session/message/part
  formats. Zero idle lifetime generates a value per logical request and keeps
  it for internal retries, without guaranteeing uniqueness.
- Anonymous global concurrency/RPM observations for administrator and level-5/6
  log pages, sampled every five seconds and refreshed every 15 foreground seconds.
  Minute samples approximate 24-hour peaks; observation start and all-time maxima
  survive restart. No request-log scan or extra per-request write is added.
- Three appearance intensities with immediate browser preferences, system-following
  theme/reduced-transparency defaults and manual-choice precedence.
- Refined Shared Daylight, A Notice to Steal Your Heart and Across the Mirror Sea
  SP scenery, lighting and motion, with dedicated responsive wallpapers and matching
  static fallbacks. Shared Daylight is the new title for Run Away with You.

### Changed

- Reduce CI transfer and setup work with compact station artifacts, shared browser
  fixtures and a dedicated race build cache. Schedule slower Go packages and
  balance race tests using measured durations while preserving complete coverage.
- Route preflight and snapshots use the existing read pool. Request-adaptation
  reads release database connections before decoding, and charity snapshots reuse
  each physical key's quota result within the same transaction. Configuration
  depth validation avoids building a second generic JSON tree, and keys without
  current recurring rules skip an empty epoch query. Dispatch still revalidates
  live limits before reserving resources.
- Backoff policy edits affect future dispatches while in-flight requests retain
  their rule snapshot. Existing waits, trial ownership and failure streaks are
  preserved.
- Chat and Responses share a model service selection, with explicit upstream
  protocol choice and bounded conversion in both directions. Other model
  services retain independent multi-selection.
- Storage schema 33 adds connection, credential-version, discovery and service
  authorization relationships, while preserving direct-inference, admission,
  managed-header and pressure changes. Only published schema 16–26 sources are
  accepted by the offline converter. Account export 29 includes safe owned
  connection metadata and donation uses; secret material, generated headers,
  internal authorizations and shared charity configuration remain excluded.
- Model settings and request adaptation have consistent entry points and header/body
  groups across personal and charity editors, with separate saves and existing
  permissions.
- Strictly verified original caller-parameter errors leave a donated key's failure
  streak unchanged, without clearing it. HTTP 400/422, generic error types and
  message wording alone never establish this result; errors, diagnostics and
  accounting retain their existing behavior.
- Batch settled request/claim validation and account-link detachment during
  account deletion while preserving active settlement and transaction rollback.
- Token output reservations account for enabled user modality and long-context
  output rates. Request charges aggregate precise usage before rounding;
  frozen discounts, replay and conservative unknown-usage behavior remain.

### Fixed

- Account deletion succeeds after related terminal request logs have naturally expired.
- Account downloads accept export versions 11–29 and preserve saved personal
  managed-header settings, while retaining identity and attachment-version checks.
- Improve glass-surface readability, management back controls and centered
  workbench widths on desktop and mobile. Focus-mode background viewing restores
  the selected reading surface on return; manual intensity choices remain effective
  when browser preference storage is unavailable.

- Keep sequential setup actions disabled until the preceding save and refresh settle.
- Keep donation review navigation fresh after returning from key binding to the list.
- Keep the charity page available when a usage-priced model has no configured credit reserve.
- Update Vite to 8.3.3 to address development-server editor-launch vulnerabilities.
- Credible standard usage survives unsuccessful Gateway finishes, including
  reasoning-only output. Failed generations remain failed, with diagnostics
  distinguishing unsuccessful termination from malformed protocol structure.
- Request accounting, logs and lifecycle projections recognize the complete
  registered inference route set. Split concurrency observations retain each
  pool's saturation for existing risk thresholds.

- Worker failures, including connection errors and unconfirmed Cloudflare edge
  responses, do not increase donated-key failure counts or trigger key backoff.
- Non-mainstream endpoint DNS identifiers are redacted in ordinary user request
  logs and in level-5 upstream error details, with current mainstream-address
  exemptions and complete administrator/level-6 diagnostics.
- Workflow browser coverage now has an explicit isolated suite selected by the
  shared local and CI verification plan.

## 1.0.6 — 2026-10-20

### Added

- Operator-configured Cloudflare AI Gateway forwarding for exact endpoint bases,
  with optional provider-header to query-credential adaptation, separate gateway
  authentication and secret protection before diagnostic capture.
- Existing-binding markers and a single donated-key operation spanning multiple
  charity models.
- Channel binding presets managed by administrators and level-6 stewards, with
  one operation applying a preset to all keys in an approved donation. Missing
  models, unavailable resources and existing bindings produce per-item results.
- Recurring weekly charity discounts with configurable time zones, weekdays,
  time windows and percentages, including cross-midnight periods. Complete price
  templates copy the schedule. An enabled fixed-window discount pauses it.

### Changed

- Charity smart backoff automatically recognizes supported recoverable quota
  errors. Per-key profiles and supplemental rules are removed; existing waiting
  deadlines, trial ownership and safe status remain available.
- Key details give upstream waits and smart-backoff state a dedicated visible
  area with separate deadlines, including when another restriction disables a key.
- Public pricing adds current payable prices and the next percentage change.
  Accepted requests retain their price across schedule changes and retries.
- Fishing leaderboard snapshots use the existing read pool so ranking queries
  no longer occupy the write connection. Ordering, privacy and expiry stay the same.

### Compatibility

- Storage schema 26 adds shared channel binding presets and removes obsolete
  smart-backoff configuration while preserving recovery state. It also adds
  disabled-by-default recurring discount settings. Account export 24
  omits the old profile and rule fields. Requests containing those fields are
  rejected; remove them from donation creation and failure-policy requests.
  Existing bindings, key enablement and level-5 model scope are preserved.
- Fixed-window discount fields retain their behavior and take priority whenever
  enabled, including outside their configured window. Existing `discounted_*`
  projections remain fixed-percentage calculations; use `current_user_*` for
  current payable prices. Template PUT without `recurring_discount` resets it
  to the disabled default.
- Cloudflare forwarding is disabled by default. It retains shared outbound
  protections and does not add gateway billing fallback or extra retries.

## 1.0.5 — 2026-10-20

### Added

- Optional donated-key smart backoff for five-hour/weekly, five-hour/weekly/monthly,
  daily and daily/monthly quotas. Built-in recognition and optional exact business
  codes or fixed message fragments leave recognized quota failures out of the
  consecutive-failure count. Waiting keys resume through one real charity trial;
  an initial HTTP 200 restores them without waiting for stream completion.
- Donor and authorized management controls for profiles, supplemental recognition
  and visible retry state. Existing level-5 key scope remains unchanged.
- A dedicated administrator price-template workspace with full pricing fields,
  search, state filters, numbered pagination, creation, editing and deletion.
  Model forms retain template selection and independent editable snapshots.
- Direct links from management request logs to the corresponding charity model
  and each attempt's donated-key settings, preserving the return filters and page.
  Missing historical resource relations remain unavailable instead of being
  inferred from names or endpoint snapshots.

### Changed

- Allow level-5 browser sessions to approve or reject all active pending
  donations, with a pending-only queue and redacted review receipts.
- Allow administrators and level-6 stewards to force-reject any approved
  donation, regardless of its original manual or automatic approval source.
  Existing review-material, revision and transaction safeguards still apply.
- Validate ordinary offline upgrades against the registered migration's affected
  relationships instead of scanning unrelated history. Explicit `--check`
  retains credential, integrity and full foreign-key checks. Conversion progress
  and elapsed-time heartbeats use stderr; the final result remains JSON on stdout.

### Compatibility

- Storage schema 25 and account export 23 include smart-backoff settings and safe
  current state. The separate converter supports exact registered schema 16–24.
  Existing keys default to smart backoff disabled. Retry headers retain priority,
  ordinary failure thresholds remain effective, and billing rules are unchanged.

## 1.0.4 — 2026-10-20

Storage schema 24 requires the matching offline converter for exact registered
schema 16–23 databases. Account exports use format 22. Publishing this source
does not upgrade an instance.

### Changed

- Separate charity model lists from creation and editing workspaces. Add
  searchable, paginated model and upstream-candidate selection, preserve choices
  across pages and show selected-item summaries and a return to the same list.
- Add administrator-managed price templates for administrators and level-6
  stewards to copy into pricing forms, including rewards, Token reserves,
  discounts, long-context prices and penalties. Existing models do not follow
  later template edits.
- Give level-5 stewards a minimal list of approved active donations from all
  sources and permission to mark a whole donation processed. Existing review,
  model creation and pricing permissions stay in force.
- Add compact atomic binding-batch receipts and consistent donated-key state
  filters without increasing the 256-binding limit or broadening visible keys.
- Allow authorized donation managers to edit the shared physical key's RPM and
  concurrency with both donation and physical-key revision checks. These limits
  also apply to the donor's personal calls.
- Honor valid `Retry-After` and `X-Retry-After` hints on any HTTP status as a
  temporary donated-key cooldown. Initial HTTP 200 resets consecutive failures;
  a non-200 response with a valid hint does not increment them. Later errors,
  protocol completion and accounting keep their ordinary behavior.
- Add owner current/history/hidden donation views and resumable hide/restore
  batches. Hiding changes a private preference and keeps the 400-day retention.
- Retain credential-redacted request and physical-response headers for explicit
  management reads, with a separate storage budget and level-5 IPv4 redaction.
  Ordinary callers receive no full-header access.
- Reuse Lake Notes configuration and decoded state within a transaction and
  reuse canonical checkpoint bytes without changing save formats or economics.
  Restrict capacity checks to active casts so the existing partial index can
  exclude terminal history. Evaluate donated-key availability before expanding
  its model IDs to avoid repeating key checks for every candidate.
  Compress header facts before persistence and decode them after the authorized
  read transaction.
- Add anonymous connection-occupancy measurements for transactions, queries,
  direct execution and statement preparation, separately from connection waits.
  Fixed categories exclude SQL, parameters, resource identities and content.

### Fixed

- Avoid unsaved-change prompts when model editors load their site time zone;
  actual model, price and date edits still require confirmation before leaving.
  Return keyboard focus to the original model row after closing its editor.
- Accept current account-export downloads through format 22 while retaining
  account-identity, attachment-version and size checks.

## 1.0.3 — 2026-10-19

Database schema 21 and account export 20 are unchanged. This source-only release
preserves immediate model availability, the existing connection limits and
synchronous durable accounting.

### Changed

- Add model-discovery and key-management shortcuts to charity connection setup,
  preserving the selected source, key and candidates when returning.
- Add exact Discord ID filtering to management request logs, including pagination
  and exports. Keep it and the called charity model visible in common filters;
  move the upstream model into additional filters.
- Add daily mystery-shop bundles of ten standard or limited tickets for 10,000
  credits each, with independent daily limits and game-credit-first payment.
- Give level-5 stewards user-management reads and permission to ban level 1–4
  accounts, with current actor and target checks before writes and replay.
  Profile, limits, level, credit, unban and active-ban replacement remain denied.
- Commit attempt and request completion in synchronous batches of up to four,
  with per-item savepoints, durable acknowledgements, bounded pending work and
  independent recovery after a failed batch. Report queue time, batch sizes,
  overflow and shutdown separately from database pool waits.
- Reuse up to 128 prepared write and 64 SELECT statements per physical SQLite connection and
  release them on eviction or connection close. Move independent configuration
  reads to the existing read pool and reuse deterministic validation only for
  identical current contents, preserving immediate changes and full write checks.
- Reuse transaction-local Lake Notes checkpoint data and prepare request digests
  before acquiring the writer. Preserve atomic revisions, leases, activity facts
  and replay receipts. Move committed activity projections to the existing read
  pool without increasing connection limits.
- Refine Amber Vow scenery and character lighting, add independent banners,
  chandeliers, layered light and foreground books, and correct footwear contact.
  Its site appearance places the character in the desktop side margin, with
  matching responsive static fallback and refreshed standard/high-resolution art.
- Extend anonymous database timing to game checkpoints, request accounting,
  reward delivery, activity projections and lifecycle maintenance. Report
  concurrent elapsed time for work that spans minute windows, with fixed
  checkpoint subphases and no per-request data.

## 1.0.2 — 2026-10-17

Database schema 21 and account export 20 are unchanged. This source-only patch
keeps the existing runtime dependencies and single-instance deployment model.

### Changed

- Batch charity model availability and recent-success reads in one read-only
  snapshot, including the complete OpenAI list and legacy directory. Personal
  model lists and the paginated charity directory use the existing two-connection
  read pool. Availability remains immediate, without a complete-list cache or TTL.
- Keep expiry writes out of model browsing. Time predicates immediately exclude
  expired donations; existing lifecycle and business paths advance stored state.
- Scan fishing retention candidates through the existing settlement-time index,
  retaining the 30-day window, 100-row batch and terminal-state deletion checks.
- Report separate writer/reader pool waits, connection acquisition and transaction
  work, background maintenance outcomes, and exact process-wide HTTP concurrency.
  Metrics remain anonymous minute aggregates with no database log records.

## 1.0.1 — 2026-10-17

Database schema 21 and account export 20 are unchanged. This source-only patch
uses the same build instructions and supported upgrade paths as v1.0.0.

### Fixed

- Avoid accounting-summary timeouts on busy ledgers by reusing hourly totals
  and reading the smaller part of boundary hours. Opening and closing stocks
  retain exact integer precision and the existing five-second read limit.

## 1.0.0 — 2026-10-17

The first stable release uses database schema 21 and account export 20. The normal
service accepts fresh storage or schema 21 and contains no historical conversion
chain. The separate `nonbiri-upgrade-v1` command converts exact schema 16, 17, 18,
19 and 20 sources while preserving existing settings and stored facts. The GitHub
Release provides source code; build the server and converter from the same tag.

### Added

- Site-wide request logs, details, sources and existing exports for level-5
  stewards. Their upstream error details redact IPv4 addresses; other authorized
  fields retain their existing values. Audit and automation permissions are unchanged.
- A noncommercial aggregation gateway tag for endpoint classification.
- Optional authenticator MFA for all user accounts, with independent default-off
  requirements for each account level, seven-day browser proofs and administrator
  reset. Existing CallerKey model calls and automation retain their credentials.
- Four SP themes with matching layered artwork, appearance wallpapers and
  encounter replays. New SP pool templates contain no featured SR cards.
- A milk-crab fishing presentation with independently configurable probability,
  mutually exclusive with the blue fish and using the same length and reward rules.
- Gemini and OpenAI Responses chat upstreams, plus OpenAI Decisions upstreams
  and the `/v1/decisions` API. Endpoints can share a URL across protocol types.

### Changed

- Read credit-audit summaries and trends through bounded read snapshots without
  taking the business writer connection; batch operation posting reads.
- Let inactivity execution and policy-audit history choose 10, 20, 50 or 100
  rows, move both ways, jump to a page and recover after a failed load.
- Explain model-discovery failures consistently in setup and key management.
  Identify explicit Gemini server-region rejections and temporary upstream
  failures, retain restricted diagnostics, and offer retry and manual model entry.
- Optimize filtered request-log counts and pages with covering indexes and bounded
  read connections. Log queries no longer hold the sole business connection;
  deadlines return an explicit retryable error instead of an internal error.
- Preserve complete saved management logs, audit history, diagnostics and exports
  for administrators and level-6 stewards, including original binary error bodies.
  Keep ordinary-user visibility and safe output encoding unchanged.

- Fit fatfish editor, playtest and gameplay fields to both viewport dimensions,
  keeping the complete default field visible while retaining manual enlargement.
- Localize platform API errors to the request language and redact IPv4 addresses
  from ordinary user displays while preserving editable settings and normal model
  responses. Administrators and level-6 stewards retain complete saved log content.
- Require completion of first SSR/SP encounter scenes; owning a card once unlocks
  both encounter replays.
- Wake audit scans on demand, avoid charging transient database contention to the
  scan failure budget, batch model-page summaries and mount adaptation editors
  only when expanded.
- Document independent management MFA and private origin authentication without
  changing the deployment automatically.

- Reduce redundant homepage requests, idle game polling and empty game-worker
  transactions. Aggregate charity catalog success rates for the whole page in
  one snapshot, and cache content-hashed JavaScript and CSS across visits.
- Report anonymous request latency histograms and database-pool waiting once
  per minute through the existing process logger.

- Allow level-6 stewards to configure automatic bans on client rules and manage
  bound rules, using the existing session, revision and audit controls.

- Apply configured live client-rule automatic bans to personal and charity
  model calls, using original headers and trusted source IPs before dispatch.

- Remove prerelease migration chains and startup backfills from the service.
  Validate the encryption key once at startup; retain full credential checks in
  offline maintenance. Deployment tooling can read compiled schema metadata.
- Keep ledger operation rules in one registry and synchronize missing content
  templates without overwriting administrator settings or reopening activities.
- Batch request adaptation and gacha eligibility reads, reuse immutable request
  metadata, and separate application route registration from runtime setup.
- Remove the unused SMTP variables and the normal Gateway environment import.
- Share local and CI verification scope, balance complete browser specs across
  two smoke jobs, split backend integration suites and update race weights from
  measured runs. Serial conversion checks remain in the ordinary upgrade gate.
- Consolidate developer setup, configuration, database and deployment guidance.

### Fixed

- Keep credit-adjustment results visible after account refreshes in the
  administrator and steward consoles. Preserve rejected input and retry an
  unknown result with its original idempotent operation.
- Display input and output token quotas with token units in summaries and usage
  cards, preserving the server's exact counts and credit precision.
- Correct hand anatomy in the celestial pool cover and wandering character
  artwork, including animation layers and high-resolution derivatives.
- Align charity catalog and key-model availability with donor ban eligibility,
  and distinguish missing models from temporarily unavailable keys in logs.
- Keep gacha usable with negative account balances and calculate each wallet's
  spendable funds independently. Display decoders omit unused response fields
  and redundant settlement arithmetic while retaining exact amounts and action
  constraints.
- Recover in-flight requests through the active-state index, avoiding scans of
  completed request history. Startup failures identify the recovery domain
  without exposing private error details.
- Gacha entry survives ordinary session refreshes and retries the same accepted
  operation. Countdown updates no longer render the whole activity page.
- Activity streams recover silently from short interruptions and offer one
  inline retry after a prolonged loss of authoritative state.
- Link Link shows selection immediately while the server decides each match.
  Collapsed views defer expensive content and hidden ranking requests.
- Capability refusals clear affected private views without signing out the
  whole site. Route loading and obsolete deployment resources have explicit
  recovery controls.

### Added

- Site-wide request logs, details, sources and existing exports for level-5
  stewards. Their upstream error details redact IPv4 addresses; other authorized
  fields retain their existing values. Audit and automation permissions are unchanged.
- A noncommercial aggregation gateway tag for endpoint classification.

- Optional per-value sensitivity markers for request rewriting. Only marked
  strings present in the outgoing request participate in response leak checks;
  existing configurations and ordinary option values retain their behavior.
- Redemption-code abuse protection, defaulting to five counted errors in a
  rolling 24-hour window and a 24-hour ban. Unknown and already-claimed codes
  count immediately. An existing but unavailable or ineligible code receives
  one free refusal per account and code in the window; later attempts count.
  Retrying the same idempotent submission never counts twice.

## [1.0.0-rc.7] - 2026-10-12

This source prerelease supports fresh databases, the final rc.5 schema and registered rc.6/rc.7 schemas. SQLite remains Generation 2 with schema revision 16; account exports use schema 19. Build from the tagged source; no official prebuilt binaries are provided. See the deployment guide for supported upgrades and complete-snapshot restore requirements.

### Added

- Administrator reward bundles with nested eligibility conditions, durable site-wide delivery and redemption codes with optional quotas and schedules. Personal receipts include duplicate conversion and skipped items.
- Heartfelt moments site backgrounds selected from owned cards, unlocked illustrations and eligible pool covers, with character-only SSR/SP motion and remembered per-account artwork.
- Independent daily gacha check-ins for one standard ticket and one limited ticket; both may be claimed each site day.

- Charity request logs and exports show the original caller mode and actual automatic penalties, with an explicit unknown state for old records.
- Administrators and level-6 stewards can open a selected account’s retained call-IP audit from its profile, including accounts with only one observed IP.

- Personal-model parameter exclusions and encrypted header/body overrides, with per-section service inheritance and form examples.

- Managed charity-model session headers with persistent per-user reuse, configurable idle expiry and UUID or hexadecimal values. Concurrent requests and retries preserve their session; protected upstream headers retain authority.

- Optional charity-model long-context prices and an independent one-time extra-long penalty based on reported input, including cached tokens. Alternate prices keep existing discounts; penalties do not affect donor rewards.

- The Gacha Simulator with 57 cards across thirteen themes, fifteen animated SSR and two animated SP illustrations, seven covers, unlockable group illustrations and dedicated sound effects.
- A permanent pool and concurrent scheduled limited pools, configurable probabilities and hard guarantees, and cross-period progress shared by pools with identical rules.
- Standard and limited tickets, game-credit-first standard exchanges, general-credit-only limited exchanges, a one-time ten-ticket welcome gift with a first-ten-pull SSR guarantee, and daily activity check-ins.
- Duplicate-to-shard conversion, a mystery shop, limited-card redemptions during pool availability and shop-exclusive chibi cards. Theme completion grants tickets and ten cumulative donation credit, plus 10,000 game credits for permanent themes or 10,000 general credits for all other themes.
- Administrator pool, probability, price, quota and reusable preset controls. Opened pools retain accepted rules; permanent-pool changes protect current guarantee progress.
- A shared ten-card reveal board with SR focus, full-screen SSR/SP close-ups, first-encounter SP preludes, bilingual character lines, skippable results, animated duplicate conversion and optional card-back hints and device vibration.
- A background appreciation control for non-default user-site appearances, with page-state preservation and keyboard return.
- Two SP cards with matching light/dark site appearances, unlocked on acquisition and selected per account. Their separate rule group uses a 15% featured share within the SSR tier and a guarantee after two misses; direct redemption costs nine SSR shards.
- A manual, once-per-location Lake Notes collection reward of 50 cumulative donation credits and 100,000 game credits, including previously completed collections.
- A standalone activity preview with separate sample data, new-account and full-collection modes, appearances and SSR/SP presentation samples.

### Changed

- Existing card art, group illustrations and pool covers retain separate background and foreground layers; SSR card backgrounds stay static. The artwork guide defines background-first production for future themes.
- Card voices use more varied, directly affectionate dialogue. SP site wallpapers animate separate characters, clouds, water, celestial bodies and a screen-crossing whale, with static reduced-motion fallback.
- Enhanced card artwork reaches 2560×3840 and landscape art 3840×2160, loaded responsively for large displays. Light and dark appearance panels combine soft edges with readable central surfaces.
- The Fat Fish editor groups creation tools, canvas modes and properties, explains unfinished requirements, previews invalid geometry and fish collisions, and supports precise headings, viewport placement, zoom and touch navigation.
- Charity catalog filters remain visible. Both stations improve request-log labels, exact credit amounts, mobile controls, empty states and appearance contrast.
- The gacha activity entrances start hidden and can be enabled by administrators. The permanent banner sits below Cyber Loan; each currently open limited pool gets its own scheduled entrance and direct link. Limited-only themes stay hidden until a pool has opened or the user owns one of their cards.
- Database schema revision 16 adds remembered appearance artwork, reward campaigns, recipient receipts, independent daily ticket claims and transactional collection, guarantee and activity-journal storage, frozen context prices, temporary managed-header sessions, personal-model request configuration and safe request-mode/automatic-action facts while preserving supported predecessor data and existing legal overrides. Account export schema 19 includes the user's collection activity, appearance and safe personal request-configuration projections.

### Fixed

- Themed donation lists, filters, tables and older cards retain readable surfaces. Heartfelt moments gently tint the shared interface from the selected artwork while keeping light and dark text contrast stable.
- Mystery-shop cards show their individual shard costs. Reusing a reward code explicitly reports that it was already claimed and identifies the earlier receipt without issuing rewards again.
- Selecting or clearing a limited-card theme in the administrator pool editor no longer saves the pool or template automatically.
- Appearance settings hide unowned designs whose cards have never been offered, including future and never-open paused pools.
- Wallpaper portraits keep their faces, bodies and limbs fixed; only individually marked hair ends and loose fabric sway.
- SSR/SP card blinks follow individual eyelid contours without crossfading the surrounding face. Card animation now uses the same phase-preserving 60 Hz cadence as appearance wallpapers.
- Personal model requests remain available when general credits are negative; charity balance checks remain in effect.
- Gateway provider warnings no longer abort valid responses. Stream protocol failures retain a bounded rejected-event diagnostic under existing private-log permissions.

- Donation-only reward records can retain anonymous accounting facts after account deletion, while new rewards still require a live recipient.
- Charity client auto-ban checks now precede short-content, credit-balance, candidate-availability and Debug capture admission, so an early business rejection cannot bypass a matching restriction.
- Draw details last 180 days. Collection and reward eligibility persist for the account lifetime; activity journals and credit-ledger retention are independent. Account deletion removes personal state and de-identifies retained accounting facts.

## [1.0.0-rc.6] - 2026-10-09

This source prerelease supports fresh databases, the final rc.5 schema and registered rc.6 schemas. SQLite remains Generation 2 and account exports use schema 14. Build from the tagged source; no official prebuilt binaries are provided. See the deployment guide for exact upgrade and complete-snapshot restore requirements.

### Added

- OpenAI-compatible image generation at `/v1/images/generations`, including bounded image streams and existing charity settlement. Personal and charity models can select chat, embeddings and image generation separately; unsupported operations reject before upstream dispatch without increasing key failures. Existing models retain chat and embeddings, while new models default to chat.
- Administrator controls for registered users' Discord server and role checks, with a default-required global policy and per-user inherit/require/exempt choices. Bans and account restrictions still apply.
- Cache-balanced routing for personal models, using a 300-second owner/model affinity and physical-key load shared with charity calls. Ordered and random calls contribute actual dispatch counts without changing their selection order.
- Steady Catch with keyboard and touch controls, 134 meme entries, a configurable ticket and first-clear reward, and rolling scoreboards. It starts disabled with both amounts set to zero.
- AI Gwent with four factions, server-authoritative native Go rules, deck building, timed decisions and timeout play. Player matches use a configurable shared prize pool; rolling win rankings and private Elo records are separate from payments. The game starts disabled.
- Administrator-only endpoint tags, bulk tag changes and filtering by tag or site user ID. Raw-error storage now lists missing records and distinguishes read, queue and storage failures.
- Separate per-user charity and global request limits, with charity-first classification when both are exceeded.
- Bidding Duel AI challenges with four editable local strategies, multiple configurable opponents, situation previews, optional use of bounded personal match memory and one-time game-credit rewards. AI entry is disabled with zero prices by default; waiting is unpaid and running matches retain their accepted settings.
- Gwent AI challenges use the original local scorer and four stable faction opponents with administrator-selected original deck presets. AI entry starts disabled with zero tickets and first-clear rewards, retains accepted decks and prices, and has no personal memory or effect on PvP rankings.
- A versioned, game-independent decision interface with bounded scheduling and authoritative action submission. Account export v13 includes personal AI preferences, samples, used summaries and first-clear records; anonymous archives omit personal memory and identity links.

### Changed

- Require Go 1.26.9 for builds, incorporating standard-library HTTP and TLS security fixes.

- New personal and charity models default to cache-balanced routing. Existing strategies and omitted PATCH fields retain their saved settings.
- Request logs open without a preset time window. The charity model directory filters by supported operation type, and the new charity model form can collapse while preserving its draft.
- Service overview badges follow the current enabled mainstream subscription and API-platform channel configuration, including manually entered matching addresses.
- Shared controls, pagination, exact amount displays, responsive tables and terminology are consistent across both stations. Model forms expose supported operations; administration pages simplify defaults, alerts and log filtering.
- Steady Catch adds preparation countdowns, miss and combo feedback, clearer hazards, a cloud companion, round collections and richer results. Mobile play and help use less space while preserving the existing game rules and payment behavior.
- Account export v14 includes the current Discord sign-in policy and personal model operation types while preserving earlier safe projections.
- Gwent, Lake Notes and Steady Catch restore their supplied original interfaces, artwork, menus and interactions. Gwent includes all 16 standard deck presets, local custom decks and free AI spectator demonstrations; account games keep server-authoritative progress and settlement.
- Cyber Loan stays first in permanent activities, spans the desktop row and shows its rules without expanding a panel. All nine games provide a consistent return link, and games without per-game credit rewards show that fact beside entry controls.
- Administrator and steward request logs explain preflight rejections with safe field and validation details; older records explicitly report when details were not recorded.
- Lake Notes moves from limited activities to permanent free games, preserving profiles, unfinished casts and payment records. The expanded fishing catalog adds progression and equipment choices; all four credit exchanges start disabled after migration.
- Charity requests stop retrying and clear consecutive key failures when an upstream returns HTTP 200. An actual upstream 200 stream consumes quota even if it later fails or returns no content; missing usage settles against the reserved amount. Protocol errors remain visible in request logs and sanitized caller responses.
- Plain HTTP upstream endpoints remain available with a credential and content exposure warning.
- Game history downloads use one streaming ZIP. Anonymous exports include recent records after anonymization; date filters exclude older archives whose dates were removed.
- AI strategies have a dedicated administration page, and shared side forms expand within their pages. Bidding Duel separates player matches from a compact opponent selector and labels local strategies as first-generation AI players.

- New battle matches use triple base speed costs including images, stackable Subscription Squeeze with recoverable locked burst balance, and SOTA Pressure based on pre-cast enemy debuff layers. Historical matches retain their original rules; the field guide, quota display and tutorial follow the new balance.

- Database startup accepts fresh databases, final rc.5 and registered rc.6 schemas. Earlier migration chains are removed; supported additive upgrades retain offline full-audit tooling without replaying all history at startup.
- Fresh databases use consolidated schema definitions. Explicit schema/data revisions apply forward migrations atomically, with target configuration validated before commit; stable v1.x releases will retain direct upgrades from v1.0.0 onward.
- Lake Notes versions saved profiles and cast formats independently of catalog identity, while unfinished casts retain exact simulation rules. Obsolete bootstrap backfills and repeated checks for historical table availability are removed.
- Covering indexes and bounded detail loading speed up credit history. Request-log totals avoid display-only account joins and use an index that also covers unfinished requests.
- Mutations refresh visible query windows together and leave older pages stale until revisited. Response adapters avoid repeated text-length and pagination-total checks; obsolete full-collection donation reads are removed.
- Activity configuration uses batched reads, game configuration reuses compiled snapshots, and unchanged session timestamps no longer cause writes. Date formatting reuses the browser time zone, game screens load on demand, and level initialization avoids repeated validation.
- Bilingual READMEs focus on setup and development, and release documentation specifies source-only distribution.

- Credit transaction details and account exports retain the last 30 days. Six-hour maintenance replaces expired entries with opening balances and non-personal audit totals, preserving current balances, pending settlement, held evidence and minimal receipts needed to prevent duplicate rewards. Historical audit ranges use complete site hours after detail compaction.

### Fixed

- Lake Notes keeps rendering while a debris catch awaits confirmation, so the result and retry controls remain usable without reloading the page.

- Lake Notes bounds result confirmation requests and offers an in-page retry when the outcome is uncertain, so a stalled confirmation no longer requires reloading the whole page.
- Completed responses retain their successful result when a client disconnects during settlement. Final stream flush failures remain visible through the billing response wrapper.
- Fishing checkpoints survive same-account session refreshes. Returning to a fishing page refreshes saved cast revisions before allowing play to resume.
- Home game continuations accept Gwent, Steady Catch and Lake Notes records without rejecting the whole section.
- Fishing and catch sessions drain pending checkpoints, save pauses before resuming, and recover correctly when their pages remount. Fishing storage failures now include bounded server diagnostics.
- Thursday contribution counts stay together on one line, with readable adjacent amounts and units.
- Discord callbacks accept the issuer parameter and tolerate additional provider metadata while validating login state and issuer identity.
- The first visit to legal settings no longer inherits a hidden ordinary-settings search. Discord blacklists use stable newest-first ordering.
- CallerKey acceptance rechecks revocation in the dispatch transaction. Lowered concurrency limits apply to new admissions immediately, implicit HTTP transport retries cannot replay request bodies, and image downloads have bounded write deadlines.
- New game layouts keep primary controls accessible at desktop and mobile sizes; Lake Notes catch results no longer expand the fishing viewport.
- Dark-mode AI cards use the game's theme colors.
- Donations rejected before any approval show that they were never approved.

- Terminal charity reservation cleanup uses end-time indexes, and Lake Notes cleanup reads active and expired casts through separate indexes, avoiding scans of retained history within each maintenance batch.
- Completed image tasks publish their downloadable results before a status reader can observe success, preventing a brief missing-image response.
- The charity ranking is visible beside the shared-model catalog on desktop and below it on mobile.
- Inactivity-policy previews support page sizes, previous and next pages, and direct page selection. An optional execution time uses the site's configured time zone; leaving it blank preserves elapsed-time scheduling.

## [1.0.0-rc.5] - 2026-10-03

This entry describes the rc.5 Linux/amd64 source prerelease. Build from the tagged source; no official prebuilt binaries are provided. Supported upgrade sources are the complete rc.4 database at `8a46c72d911a914eabcb7ef17c537e7ac12d6969` and the exact preceding deployed source at `77e7f41646d6c720b6ae4ddc6dbb4dd9e0b31604`. SQLite remains Generation 2 and account exports use schema 12. Instance publication, legal text and deployment remain separate operator actions.

### Added

- Personal and charity model `transport_rule` with default passthrough, forced nonstream upstream and forced stream upstream, preserving the caller format and frozen retry policy.
- Administrator and level-6 log filtering by literal, ASCII case-insensitive call-time charity model name across lists, pagination and exports.
- Read-only `maintenance verify` for full historical audits of a stopped database or trusted consistent copy.
- Standalone bilingual Nginx maintenance pages and JSON API errors while the application is unavailable.

- Online administrator Gateway capability configuration, exact-target matching and request snapshots, with a one-time import of existing environment settings.
- Gateway V3 prompt-cache markers at request, text-block and tool positions, explicit lifetime mapping, adaptation defaults and the `cache_write_tokens` usage field.
- Exact daily, weekly and monthly charity reset times with rule-zone previews, month-end recovery and preserved in-flight accounting.

- Gateway V3 preserves `max_completion_tokens` and `max_tokens`, validates conflicting budgets, and supports administrator-configured model reasoning and storage policies. Unsupported controls reject before reservation, with field and stage diagnostics.

- Per-model ordinary message-role policies for personal and charity models, with native compatibility, explicit passthrough/mapping/rejection and independent tool-message handling.
- Personal CallerKey reads, partial batch key imports and model-connection appends with owned-secret deduplication, default manual or strict fresh-discovery mode, fixed 24-hour recovery and shared automation admission. L6 CallerKeys gain bounded charity resource/catalog/model/connection reads; L5 retains personal automation and existing scoped browser permissions.
- Lake Notes as a separate server-backed limited activity: full fishing progression, one recoverable cast, per-period once-only general-credit entry, exact four-way coin/general/game-credit exchanges and administrator periods. The activity and all exchanges start disabled; profiles persist across devices and periods.
- Current denied-sign-in explanations after verified Discord identity, immutable historical request identity and rolling multi-address auditing across accounts of the same Discord identity. Retained logs survive all account-deletion sources until their original deadline or hold; re-registration never grants old-log access.
- Forced rejection of automatically approved mainstream donations, stable secret-text renewed-review requirements and managed donation-member manual model candidates.
- Fat Fish v3 motion, explicit source-bound offline legacy cleanup, current-playtest recovery/abandonment, automatic terminal submission and visual polygon/period-graph editing. Private battle presets gain optional names and readable summaries.

### Changed

- Both stations use consistent navigation, compact tables and mobile cards, grouped forms, detail drawers, secondary-action menus and clear save feedback. Home and API access show setup progress and client examples; personal services/models, charity, games, records and account preferences use the shared presentation. Legal documents gain a directory without changing instance text.

- Ordinary startup retains database ownership, identity/schema/credential checks and unfinished-work recovery while avoiding full copies and repeated historical scans. Application composition and lifecycle reside in `internal/app`; the root entrypoint and `go build .` remain supported.
- Charity accounting uses validated upstream generation and actual or existing conservative unknown usage even when a buffered JSON response is not delivered; caller disconnect still immediately cancels upstream.

- Administrator and steward forms group optional controls, explain defaults and units, use percentages for fees, and clear old save notices when edited. Consequential actions use one confirmation.
- API access presents a newly issued full CallerKey once, with copy/save actions and a client setup guide. Normal key views show masked metadata.

- New battles use 100/500 starting coins, 2.5× rounded-up token/energy speed costs, 2× skill likes, one/two-turn overload, Thunder self-overload and deliberate image-shortage submission. Harness changes include Claude Code's independent successful-debuff likes, Antigravity's +2 normal attack and Copilot's conditional +2. Saved older games/replays retain their exact rules.
- Account export schema 12 adds safe Lake Notes progress/receipts, model-role policies, private preset names and retained automation outcomes, keeping existing whole-file limits and secret exclusions. Instance legal overrides remain an operator publication action.
- Optional connection onboarding can stop and resume without blocking charity or deleting completed resources. Resource, account, diagnostic and management workflows use shared operation/confirmation behavior, structured fields and bilingual guidance.
- Free width/height image choices with no fallback price use only valid exact priced pairs; stale choices require reselection without losing other input.
- Risk-based checks retain full final-candidate coverage while browser integration reuses one compiled backend fixture and race selection follows actual concurrency boundaries.

### Fixed

- Startup idempotency recovery uses indexed accepted and expired-record queues, avoiding scans of large live response records while preserving replay windows, ordering and bounded transaction batches.
- Donation details accept exhausted usage totals that exceed a configured cap, including output-heavy calls.
- Credit auditing recognizes Fat Fish and Lake Notes ledger channels and resumes historical projection without discarding existing totals.
- OpenAI-compatible SSE normalizes an invalid first string completion ID into one stable ID per attempt, preserving valid IDs, reasoning, tools, usage and terminal checks. Credential reflection is checked before normalization.
- The model request-body boundary accepts the configured maximum, including 64 MiB, and rejects larger known-length or chunked requests before upstream dispatch or credit reservation.

- Exact-target Gateway `anthropic_explicit` caching lowers root controls onto the final eligible prompt block, preserves caller markers and supports tool-result text arrays, with explicit rejection of conflicting TTLs, invalid TTL order, excess breakpoints or unrepresentable positions.
- Gateway accepts valid empty content-filter/refusal termination in JSON and SSE while retaining actual usage.

- Accepted streaming chat requests send idle SSE comments during upstream waits and retries, without counting them as output or consumption. Final failures after headers use one SSE error.
- Incorrect administrator re-verification passwords preserve the current form, and successful steward maintenance actions show their saved state immediately.
- Re-verification accepts the server's session-bound tokens for legal holds, exports and account deletion.
- Administrator account deletion reaches the existing cleanup workflow with revision and retry protection. Manual model candidates retain their own identifiers when listed and removed.

- Lake Notes accepts paused checkpoint recovery at the last saved tick when a control lease expires or the activity closes.
- Lake Notes accepts held Space from the game background without scrolling, while menus and form controls keep their normal keyboard behavior.
- My Donations accepts current review metadata and forced rejections when opening donated keys and donation details.

- Anthropic-compatible and Gateway v3 tool flattening now support complete bounded history and streaming, and OpenAI nonstream chat accepts the one supported success/data wrapper while rejecting ambiguous/error forms.
- Donation failure streaks count confirmed upstream failures and reset on protocol success; cancellation, downstream failure, platform rejection and unknown recovery are neutral. Accounting remains independent.
- Fat Fish playtest recovery and submission no longer leave a completed local game blocking a new preparation.
- Resource conflict recovery, Debug structured-message rendering and browser regression fixtures preserve authoritative response fields and current page/filter restoration.

## [1.0.0-rc.4] - 2026-09-28

This entry describes the rc.4 source prerelease dated 2026-09-28 UTC for Linux/amd64. No official prebuilt binaries are provided. Source publication does not by itself deploy an instance.

### Added

- Administrators can optionally make general- and game-credit check-ins mutually exclusive per site day while retaining each type's availability settings. The user page displays both cards together with a clear daily-choice notice; transactional admission also enforces the choice for concurrent requests and returning identities.

- The picture-book activity now uses a built-in integration and automatic model/capability discovery. Administrators manage only the activity endpoint and key, model enablement, and each model's paper/brush default, tier, and exact-size prices. Readiness is read-only; quotes remain free, and accepted tasks keep their captured revisions and settings.
- Fat Fish adds a deterministic local game and a shared continuous field/workbench for editing and play. Administrators can place opening tools in either area, use keyboard movement with Shift fine adjustment, and undo/redo; players can move tools after a challenge starts, with optional zoom and pan. Deleting a draft removes it from the library and blocks new nodes and playtests from binding its source while preserving published versions and existing activity, challenge, and score references. The activity also includes revisioned level and period editing, condition-aware node maps, eight hash-checked example levels, import/export, and version-bound no-charge administrator playtests. Formal challenges use explicit General-credit unlock and ticket confirmations, original-tab recovery, server replay verification, and period leaderboards.
- Fat Fish includes an illustrated rice-and-obstacle cover and optional looping background music, "Monkeys Spinning Monkeys" by Kevin MacLeod under CC BY 4.0, with local delivery and an off-by-default music control.
- Risk and audit screens can page through all six result lists. Alert details cover twelve alert kinds with safe current-target resolution, related-log links, and explicit resolution or worker status.
- Game presentation now includes full-row activity covers, anonymous large-donor display, avatars on leaderboard rows, a seven-day Bidding net-profit board, and live Blackjack identities for seated players and spectators while keeping those identities out of history and account exports.
- Turn-based Battle Minigame (Test) adds up to 10 private Custom preset slots per account, saved across devices with explicit overwrite; loading only fills pre-match choices and never queues or charges. New matches use 30-second planning periods in both modes.
- Account protection adds configured automatic bans for eligible new charity requests, same-Discord continuity for eligible once-only rewards and active enforcement windows, and long-lived minimal deleted-account snapshots for administrator and level-6 review. Manual policy, role, and game progress are not restored to a re-registered account. Self-deletion during an active restriction, charity suspension, or negative general/game balance creates a lasting blacklist entry; only administrators can remove it, and role boundaries prevent peer-level management.
- Charity dispatch gains expiring key affinity and continuous expiry-weighted selection. Scoped request adaptations can add or replace outbound headers and JSON-body fields at authorized endpoint, model, and binding levels.
- SQLite lock and shared-memory handles now follow one coordinated lifecycle. Startup and shutdown apply bounded lock and recovery budgets, readiness reflects critical worker failure, and diagnostics preserve the primary initialization or worker-failure stage.

### Changed

- Deleting a charity binding compacts the remaining order. Reordering preserves binding IDs and keeps each binding's request-adaptation settings and revision attached to it. An existing binding can remain readable with `source_types:[]` when no current source is available; the management page marks it unavailable. New candidates still require a non-empty list of known source types, and unknown values remain invalid.
- Picture-book upstream status checks are scheduled six seconds apart for the first and ordinary subsequent polls. A valid bounded `Retry-After` that requests a longer wait can extend either delay; the shared request limit and original task deadline still apply. The user page's refresh schedule is independent. The Renge cover has a four-frame, two-second-per-frame waiting animation with reduced-motion, manual-pause, and background-tab behavior.
- Fat Fish records engine and scoring versions on immutable level versions. New levels and all eight example levels use engine version 2 with scoring version 1. Existing immutable versions, active challenges, retained history, and period node selections remain bound to what they already reference. Administrators can explicitly convert an older draft to version 2, save and publish it, playtest the new immutable version, then manually select it for a node; conversion does not publish or switch a node automatically.
- Automatic bans caused by configured client rules now show a readable reason such as `识别到违规第三方客户端特征：Tavo`. Multiple matching names are combined within the reason limit, while complete rule revisions remain in the separate administrative receipt and prior active-ban evidence is preserved.
- SQLite remains Generation 2. The account export advances to schema 11 with typed safe `request_adaptations`, `continuity`, and `fat_fish` projections plus optional owner-only `likes.loadouts`; full play inputs, prompts, credentials, unrevealed seeds, and other players' private results remain excluded. Oversized exports fail as a whole instead of truncating.
- The formal rc.4 upgrade source is rc.3 repair commit `37e060ab0d0f29d632fe6b8036839b413388812a` at tree `4b44e6fb11ab6d72cea7fecf1ea45ea615594274`. The exact preceding deployed source `4e06025c6bf23fbb0f34db96673b45ed01c42e97` (tree `6af9d8349d9049197366f29984e2e413090b7814`) is a separately verified compatibility case; no other intermediate schema is covered. Upgrades preserve existing account and game data, credentials, configuration, and instance legal text. Instance legal-text publication remains a separate operator action.

### Fixed

- Alert lists and details accept complete historical and current account-deletion snapshots, fixing invalid-response errors when viewing all alert types or filtering by account deletion.
- Automatic blacklisting after self-deletion during a ban now includes the original ban reason captured inside the deletion transaction. Existing first reasons and actors remain intact, and administrator/steward blacklist views preserve multiline notes.
- Deployment guidance now covers restoring Cloudflare visitor addresses on both virtual hosts before forwarding a canonical client IP to the application. Proxy regressions cover IPv4/IPv6, forged and malformed headers, and exclusion of raw forwarding headers from audit metadata; missing historical visitor addresses are not inferred.
- A reported token total that disagrees with valid individual buckets is now marked in administrator and steward request logs, with a matching filter. Billing continues from those buckets. Invalid or ambiguous individual buckets remain unknown; Gateway usage with duplicate token fields is rejected.
- OpenAI streaming requests normalize null stream options to request usage and reject invalid option shapes before dispatch. Usage-only streams cannot become successful completions, and named completion events accept the standard `[DONE]` marker.

## [1.0.0-rc.3] - 2026-09-25

Source prerelease for Linux/amd64, without official precompiled attachments. Supported data-preserving upgrade sources include rc.2 maintenance commit `db959c64674afc531046a63066de0464725d439c` and the complete administration maintenance schema at `84018acbd594765c563cc0ee4083d206e0bd6a77`, preserving business data, configuration and instance legal text. Unknown intermediate schemas remain unsupported; a downgrade requires a complete matching stopped snapshot.

### Added

- Resumable, cancellable client-clue scans with frozen rules and filters, visible progress, numbered result pages, adjustable page sizes and URL restoration. Temporary results expire after 24 hours and never trigger automatic penalties.
- Exact internal user-ID search combined with existing management filters, and retained image-discovery request metadata alongside the bounded original response diagnostics.

- Configurable model-request body size in administrator settings, defaulting to 10 MiB, with a finite 1–64 MiB range shared by chat and embedding calls.
- Administrator account-deletion alerts with retained Discord identity and economic balances, unresolved negative-balance review, alert-type filters and bulk resolution.
- An administrator Discord blacklist that prevents registration and atomically permanently bans existing accounts, revokes sessions and caller keys, and prevents unbanning while listed.

- Level-5 trainee stewards with scoped mainstream charity-model maintenance, alongside full level-6 stewards and explicit appointment boundaries. Donation-key management includes donor and review notes, immutable public-thanks choices and shared-configuration impact.
- Restricted original upstream-error diagnostics and request-source facts, with 30-day ordinary retention, 1 MiB per error, and a configurable 1 GiB default total payload cap. Raw upstream failures can contain upstream-echoed inputs or credentials; personal exports exclude these diagnostics.
- Manual abuse auditing for sustained RPM/concurrency use, shared IPs, bounded client rules and authenticated auxiliary access events. Anonymous probe counts remain separate and observations do not automatically punish users.
- Administrator economy auditing across general credits, game credits, draft paper and brushes; issuance, retirement, transfers, balances and data coverage remain distinct. Optional inactivity policies support previews, seven-day grace, asset-specific decay and reversible protective bans.
- Lifetime and recurring input/output Token limits alongside existing totals, with explicit split reservations, conservative missing-usage settlement and preserved historical totals. TPM remains deferred.
- Twenty-four-hour charity success rates, protected top-level request-parameter exclusions and a local half-price donor-reward form action.
- A reusable limited-activity framework and the picture-book activity, with separate exchange currencies, declarative private adapters, per-image prices, FIFO server scheduling, at-most-once generation submission, bounded recovery and ten-minute memory-only results. Image generation remains unavailable through ordinary personal or charity API models.
- Rolling seven-day net-profit boards for all games, Fishing and Blackjack; configurable blue-fat-fish probability conditional on a legendary catch.

### Changed

- Existing manual level-5 stewards become level 6. New trainees inherit each model's former level-4 admission initially, while former level-5 admission moves to level 6. Current full stewards may manage level-5 users within existing user-management permissions.
- Charity routing excludes a caller's own donated keys except for current level-6 stewards, whose ordinary donation rewards and cumulative credit remain unchanged.
- Account export v10 includes safe activity wallets, exchanges, image-task outcomes and inactivity actions. Four-asset account deletion keeps each balancing entry in its own asset and prevents late image work from restoring deleted identity.
- Loan presentation shows only principal, fee, game credits received, interest and general credits deducted. Existing financial receipt fields remain available for accounting.

- Administrators and stewards can fetch model lists for one available donated key, all available keys in one donation, or all available donated keys across pages and filters. Batches show progress and support pausing and resuming uncertain requests; live permission and eligibility checks protect dispatch and catalog updates.

### Fixed

- Image-service HTTPS requests now align TLS ALPN with their non-replayable HTTP/1.1 transport. Connection failures retain bounded, safe diagnostic categories instead of being reported as malformed model catalogs; certificate verification and uncertain-submission protections remain intact.

- Keep credit-audit entries on an ordered ledger query and batch their entry reads, preserving asset filters and exact amounts. Economy charts now show axes, units, data points, tooltips, keyboard-selected values and an exact data table through an administrator-only Chart.js module.
- Use browser-local time on ordinary user pages and site time in administrator/steward forms and displays. Group time-zone notices and clearly identify a different browser zone. Donation and charity-model forms use the available desktop width and collapse coherently on narrow screens.
- Apply SQLite foreign-key and busy-wait settings on every physical connection, including replacements. Complete bounded ranking backfill and expiry settlement before game recovery, preserving committed progress across interruption.

- Group Fishing and Blackjack leaderboards into accessible tabs. Show Card master first in Blackjack, followed by Profit, and name the global net-profit board Game fortune.
- Explain image-download origins and adapter mappings, and show dedicated model-discovery failures with retained HTTP status, actionable guidance and a fresh retry.

- Level-6 stewards can use general and game check-ins, including level-gated mode, with unchanged daily and balance limits.

- Show the requested charity model in administrator and full-steward log lists, details and exports, using the historical request snapshot while retaining private self-use model boundaries.

- Use server-relative audit windows, accept healthy capture summaries with no gaps, and provide working retries and guided client rules. Align audit and inactivity controls with the shared theme, with responsive layouts and ordinary credit/percentage inputs.

- Keep game payments restricted to general/game accounts after introducing activity assets, and display whole activity-currency units correctly in personal credit history.
- Preserve long-cache facts through battle presentation and distinguish persistent cache from short cache without changing game rules. Use the available glossary layout space and consistently name legendary fish in Chinese.

- Give Cyber loan a full-width promotion and a prominent nominal amount, with a compact fee-details star in the confirmation description. Keep welfare and Thursday activities together below it.
- Keep charity privacy information and the True Charity leaderboard in an independent sidebar, fill incomplete catalog rows and show paging controls above the results.
- Centralize game leaderboard anonymity in the game lounge, with direct links from Fishing and Rock–Paper–Scissors, and let shared page descriptions use available desktop space.

## [1.0.0-rc.2] - 2026-09-20

### Added

- Thirteen once-only newcomer tasks for Bidding, Turn-based Battle Minigame (Test) and Blackjack, bringing six-game rewards to 22 tasks and 60,000 general credits. Multiple qualifications settle atomically with the game and never repeat on retries or restart.
- Optional game-credit loans with exact terms, owner-bound expiring quotes, immediate general-credit repayment, immutable receipts and replay protection. Loans default to disabled and can leave general credits negative while game credits remain usable.
- Cumulative charity, seven-day game-spending, Bidding profit and Blackjack profit leaderboards. Charity visibility is independent and defaults to anonymous; banned users retain rank with anonymous identity on every board.
- Persistent abuse windows, safe account restriction summaries, authorized penalty history and evidence, and zero-usage logs for authenticated pre-handler refusals. Current windows recover across restart without repeating penalties.
- Character passives, step-snapshot scoring and independent per-layer debuff resistance, with compatible saved catalogs and a refreshed ten-round 66–61 tutorial.
- Complete-page resource filters, owner model search across all connections and authorized donated-key-to-model reverse lookup. New and pending donation descriptions must be nonblank; historical blank descriptions remain usable.

### Changed

- Blackjack starts at :00 and :30 with 5/20/5-second stages, a 3×3 seating grid and 0–8 configurable quick-stake buttons. Selecting a button does not join or charge; early completion extends the result display without advancing the next start.
- Bidding uses consistent red hearts/diamonds and black spades/clubs. Six games have synchronized, deduplicated feedback; turn-based follow-ups build across the round and normal, partial and full resistance have distinct cues. Mute and reduced-motion choices preserve the same facts.
- Account export v9 includes safe loan receipts, ranking contributions, penalty actions and pending newcomer qualifications, with a correct v9 download filename. All collection and total-size limits fail without truncation, and lazy game settlement agrees with exported balances.
- Bilingual API, configuration, lifecycle, deployment and built-in legal texts describe the new data and behavior. Existing instance legal overrides remain unchanged during upgrade.

### Compatibility

- Validated data-preserving upgrade from formal rc.1, retaining wallets, ledger, credentials, saved game catalogs, configuration and legal overrides. Charity achievement order comes from the original ledger; new game statistics start once at upgrade, without fabricated historical rewards or penalties.
- Generation 2 remains unchanged. Fresh and upgraded schemas converge; failed upgrades roll back, repeat startup is stable, and the old rc.1 binary rejects the new manifest without modifying it. Downgrade requires a complete matching stopped snapshot.
- Source prerelease for Linux/amd64 with no official precompiled attachments.

## [1.0.0-rc.1] - 2026-09-18

### Added

- Per-donation-key consecutive failure thresholds, default 10. Zero records errors without automatically disabling the key; donor, administrator and steward pages warn persistently. Saving preserves counts and immediately recalculates the error-disabled state, independently of enablement and reset.
- Native AI SDK Gateway v3 chat, true streaming, function tools, image input, text embeddings and model discovery/manual configuration, with strict compatibility checks before reservations or credential access.
- Administrator-controlled Gateway cost attribution, disabled by default, and debug metadata showing only whether the tag was sent. Steward CallerKey automation can read and update failure policies with revision and idempotency protection.
- Two-player Bidding Duel with 13 simultaneous card rounds, and Turn-based Battle Minigame (Test) with quick/standard modes, five characters, eight harnesses and the complete skill/buff catalog.
- Bidding reward draws, paired card reveals and directional collection of the whole prize pool. Turn-based battles show full-page speed and overload effects for your character, with static reduced-motion alternatives.
- Separate per-game queues, frozen entry terms, mixed-wallet admission, original-asset refunds and atomic winner/fee settlement. Both games start disabled and do not add newcomer awards.
- Synchronized, event-paced turn resolution without a fixed total duration with score breakdowns, resource changes, round-start refills, independent stun/overload artwork, reduced-motion feedback and a paginated round log. All 127 character, skill and harness slots have dedicated illustrations.
- Dedicated covers for the three new games, 35 Fishing catch illustrations with SVG fallback, synchronized Likes music and sampled effects for Likes, Bidding and Blackjack. Sound and music preferences are separate and off by default; browser-local music quality offers lightweight MP3 or lossless FLAC.
- Thirty-day player and administrator match history, long-term anonymous traces and resumable bounded administrator NDJSON export. Account export v8 includes safe new-game records.
- Private per-game randomness for all six games, with opening SHA-256 commitments, HMAC-SHA256 rejection sampling, terminal seed disclosure, bounded proofs, independent browser/Node verification and safe account export. Active responses never disclose seeds or future draws; retained legacy games explicitly lack proofs.
- One shared nine-seat Blackjack table with minute-aligned seating/decisions/results, persistent FIFO waiting, simultaneous batched actions, six-deck rules, splitting and doubling. Frozen mixed-wallet stakes settle per hand into general credits after fees; the game starts disabled.
- The user station supports wide desktop layouts while preserving the mobile layout. Turn-based Battle Minigame (Test) uses server-authored event-paced settlement steps without a fixed total-duration promise.
- Bidding uses fixed 13-card slots and public remaining reward-card sets without exposing future order. Blackjack documents the nine-seat table, and bundled music provenance distinguishes CC0 source material from project arrangements and original effects.

### Fixed

- Closed games and modes consistently disable entry and matching. Configuration refreshes while visible and after rejected entry, with specific admission feedback; learning, history and ongoing matches remain available. Turn-based battles correctly list two modes and Chinese bidding labels consistently use Joker.
- Skill, harness, passive and status cards show effect summaries. Linked details distinguish original/distilled versions, explain player mechanics and separate flavor quotes from rules. A skippable local tutorial walks through the fixed loadout and ten authoritative rounds to a 66–64 victory without matchmaking, wallet changes or match records.
- Live turn warnings add visual and optional sound cues below five seconds. Overload events record the actual energy or token shortage and highlight the corresponding resources during settlement, without guessing from later balances or changing payment rules.
- Banned Discord sign-ins redirect to the branded public 403 page; ordinary API denials retain JSON responses. The charity catalog removes the redundant provider/model line.
- LinkLink match effects wait for valid layout measurements and ignore resize frames after their board is removed, preventing invalid SVG coordinates during transitions.
- Browser clients can use the three public CallerKey model routes across origins. Valid OPTIONS preflights return 204 without authentication or model usage, and actual responses expose errors and streams through CORS. Cookie-authenticated APIs retain their same-origin boundary.
- Administrator dashboard endpoint totals use the bounded numbered endpoint page, including addresses shared by more than 100 users.
- Shared battery overload affects only positive-energy plans when the other plan costs zero; exact remaining energy is allowed and Flash keeps its independent check.
- Uncapped API reserve and gold use numeric displays. Still-stunned plans show only skip; cleansing plans require a cast and restore confirmation.
- Game availability badges reflect partial mode availability, ten-catch results keep long names and amounts readable, and welfare-pool labels avoid the wrong currency.
- Periodic recovery preserves live duels while a real restart cancels and refunds unfinished matches.

### Compatibility

- Generation 2 now has 117 tables. The validated release upgrade is populated beta.4 → rc.1; unreleased intermediate schemas are outside the release upgrade guarantee. Existing wallets, old games, configuration and legal overrides are preserved; failed upgrades roll back and older binaries reject the new manifest.
- Source prerelease for Linux/amd64. Build from the tagged source; no official precompiled attachments. Overload shortage metadata is optional for compatibility with retained older records. The local tutorial adds no database migration or real-game write endpoint.

## [1.0.0-beta.4] - 2026-09-12

This source prerelease supports Linux/amd64 and atomic upgrades from complete beta.3 and ten exact earlier Generation 2 schemas. Existing general balances, settled fees, configuration, custom legal text and saved game rules remain intact. New game wallets start at zero; rollback requires a matching complete snapshot. Build from the tagged source; no official precompiled binaries, container images, or installers are provided.

### Added

- Independent game-credit check-in, daily game welfare, both-wallet displays/history/statistics and export schema 6.
- Game-first mixed payments, original-asset refunds, per-round Rock Paper Scissors funding and nine once-only newcomer rewards totalling 17,000 general credits.
- Fishing cuts for platform, welfare and Thursday pools, with gross/net results and net-payout rankings. Fresh gross RTP is 100%; configured existing values are preserved.
- LinkLink hint/refresh opportunities and six size/window leaderboards, using each user's best score and earliest achievement for equal scores.
- Effective-level user filtering and shared user/announcement management for current L5 stewards. User mutations exclude self, L5 and administrator targets, cumulative donor credit and account deletion.
- Donor and management failure-streak resets, including bounded batches across all selected results and exact replay after uncertain responses.
- A server-controlled next-Thursday preview.

### Fixed

- Account API key replacement now displays every valid generated key; the client no longer rejects valid base64url endings before showing the one-time value and Copy control.
- Trusted actual charity chat and embedding charges can exceed their initial reserve; historical terminal charges remain unchanged.
- Thursday multiline text accepts LF and normalizes CRLF while preserving tabs.
- Model discovery merges duplicate normalized IDs after validating every source entry, retaining the first entry's metadata and order.
- User limit inputs preserve canonical strings, inheritance and useful field-specific range errors.
- LinkLink no longer reloads wallet and game-center state after every ordinary match; the next pair can be selected while a connection animation finishes.

### Development

- Complete race coverage runs across twelve CI shards, with four bounded test processes per shard. Independent test groups share runner capacity, while packages with process-wide test setup remain intact. Repeated equivalent database-fixture setup is reused within the affected test matrices.
- A merge to protected `master` reuses the complete PR result after tree verification. The workflow remains available for manual runs without automatically repeating the same full checks after every merge.

## [1.0.0-beta.3] - 2026-09-11

This prerelease targets Linux/amd64 and preserves existing data from the complete beta.2 schema and the nine previously supported Generation 2 schemas. Build from the tagged source commit; no official precompiled binaries, container images, or installers are provided.

### Added

- Two dedicated CallerKey controls let current stewards atomically create and approve their own donated keys, then synchronously discover or add exact upstream models and bind those keys to an existing charity model. Creation supports all key limits, including recurring rules, with atomic replay; binding preserves per-key successes and reports failures or incomplete work. Calling rules are documented for administrators.
- Administrators and stewards can open a bound donation key from a charity model's service connection order, with its key page selected and the original model filters and page preserved on return.
- OpenAI-compatible `POST /v1/embeddings` for personal and charity models: single or batch text and Token ID inputs, float/base64 output and optional dimensions. Existing model connections, shared limits, logs and memory-only Debug apply without a model-purpose field. Rerank and Anthropic embedding support remain deferred.
- Embedding charity billing supports per-request batches and input-Token pricing. Explicit zero usage is distinct from unknown usage; missing usage follows existing conservative reserves and earns no donor reward. Only minimum-content penalties are exempted. Requests use a server-generated, user-and-origin-scoped `user` pseudonym.

### Changed

- The user-station manual catalog labels its optional display metadata as “Note”; it remains independent of the exact upstream model ID and connection identity.
- Creating a Thursday activity automatically uses the next Thursday at 00:00 Beijing time. The administrator page shows the activity window and no longer asks for a period key or opening time; edits preserve an existing period's schedule.
- Fishing, LinkLink and Rock Paper Scissors now register through a common backend game host with shared transactions, recovery and lifecycle handling. Existing routes, saved games, configuration, prices, rewards, timing and exports remain compatible.
- New LinkLink games use varied constructive layouts with a verified complete matching sequence. Existing boards and free deadlock reshuffling remain intact.
- Request and log type constraints now admit personal and charity embeddings. The database remains Generation 2 with 99 business tables and account export version 5. Log clients with closed request-type enums must accept the new values.

### Fixed

- Donation lists and details retain automatic or manual approval after expiry or termination and accept legitimate unreviewed terminal records, including when a reviewer has been deleted.

### Upgrade notes

Take a fresh complete stopped-service snapshot and validate an isolated upgrade before switching releases. Custom legal text and existing business data are preserved. The old binary rejects the new schema, so rollback requires its complete matching snapshot. Default bilingual privacy and terms templates describe embedding processing; operators remain responsible for their effective custom text.

## [1.0.0-beta.2] - 2026-09-10

This release is source-first for Linux/amd64 and keeps the documented Generation 2 compatibility boundary.

### Added

- Fishing now opens on an independent rolling 30-day largest-length board, alongside lifetime records and the existing rolling payout board. Legendary catches can become a transparent-background, white-rice-themed blue fat fish Easter egg with exponentially rarer lengths, while preserving the original fish species, reward and payout rate. Length-board rows use a compact original species name; result details retain the original legendary species explanation.
- Bounded numbered pagination across the resource, activity, log, donation, model, report, legal-hold and administration lists, with 10/20/50/100 page sizes, direct page navigation, preserved filters, filter and page restoration after returning or refreshing, and a separate browser-local preference for each list.
- A complete charity model catalog with plain-text descriptions, allowed-level sets and availability reasons. Three independent filters select a level, personal access and current availability; the default shows accessible, available models without a level filter, and active filters are highlighted. The public API returns only usable models. Authorized administrators and level-5 stewards can browse donation sources and keys.
- Independent donation-key recurring quota rules for calls, tokens, or credits, using reset or sliding windows over 1 hour, 5 hours, days, weeks, or months in a selected business time zone. Reservations, successful-response settlement, edits, expiry, recovery, and deletion remain transactionally bounded.
- Token-priced charity models can carry an optional per-model credit reserve before a call. Blank inherits the global setting, per-request pricing keeps its existing per-request reserve, and accepted requests retain their chosen reserve through in-flight settlement.
- Browser-local time-point parsing and display with a server-resolved daylight-saving gap/fold policy, while recurring-rule time zones remain separate from ordinary timestamp display.
- Home announcement summaries with severity and safe Markdown detail handling, plus beta.2's expanded game presentation, responsive LinkLink layout, and generated short sound cues.

### Changed

- The daily check-in balance threshold now applies to every level. Zero still disables the threshold, and the level-gated mode still requires level 3 or above.
- Current level-5 stewards can read the same request logs and donation review information as administrators, including donor identities, routing key identifiers, user filters and bounded log exports. Credentials and ordinary-user projections remain protected.
- Account export is schema version 5. It adds safe projections of donation-key recurring rules, Fishing display lengths and the requester's rolling best, and the requester's own RPS buy-in/cash-out values while continuing to exclude secrets, other users, reports, holds, and internal scheduling data.
- Generation 2 browsing indexes and beta.2 sidecars are added only through the exact validated additive update paths; existing data and historical facts are preserved and unsupported schemas remain zero-write refusals.
- The optional model-level Token reserve is sparse: a missing override inherits the global setting, model deletion removes its override, and the setting does not change public catalog/API or owner-export projections or in-flight accounting snapshots.
- Race checks keep all six shards, complete test coverage, original assertions and timeouts, while redistributing measured test weights to reduce the longest shard's wait.

### Fixed

- Public configuration, administrator branding and startup remain available after the last Thursday period settles, and when the activity master switch is paused with individual settings preserved. Enabling Thursday still requires a configured period.
- Request log details show the actual logical-request charge and each attempt's routed key identifier; attempts no longer display a misleading zero charge.
- Fishing keeps the oldest unacknowledged catch visible until confirmation, blocks repeated starts while revealing, and preserves reveal and acknowledgement timing across balance refreshes.
- Automatic RPM bans apply only to charity requests exceeding the site's per-user limit. Rate-limited personal resource calls, shared key limits and upstream rate-limit responses do not trigger this policy.
- Personal and charity calls preserve recognizable upstream error messages, safe machine codes and HTTP error statuses while hiding source addresses and sensitive values. Errors after streaming starts use a single bounded error frame; unreadable or unsafe bodies retain a generic fallback.

### Compatibility and deployment

- Beta.2 continues Generation 2 (`application_id=0x4E425249`, `user_version=2`). A fresh deployment requires an absent database/WAL/SHM set. Four exact earlier Generation 2 manifests and five exact previously deployed beta.2 structures are supported for additive updates; the latest extension widens recurring-quota interval checks to allow one hour without rewriting stored rules, counters or receipts. Alpha and Generation 1 require a fresh cutover, and an incompatible binary-only downgrade remains unsupported.
- The release remains source-first for Linux/amd64 and has no official precompiled binary, container image, or installer.

## [1.0.0-beta.1] - 2026-09-06

### Added

- Database Generation 2 with a manifest-validated schema; a central double-entry credit ledger and shared pools; crash-recoverable worker checkpoints; strict mutation replay; and bounded legal holds.
- Bilingual announcements, daily welfare, the Thursday pooled activity, account activity summaries, and shared user-station SSE updates with replay/gap recovery.
- LinkLink and server-authoritative three-player Rock Paper Scissors alongside batched Pond Fishing, with persistent recovery, privacy-aware leaderboards, pending-result acknowledgement, and account-lifetime aggregate fun statistics.
- Public credential-theft reporting with indistinguishable accepted responses, live-key plus time-bounded donation-tombstone matching, resumable administrator review, and safe donation lineage inspection.
- Administrator-managed mainstream channel templates, strict mainstream/custom endpoint creation, immutable endpoint provenance snapshots, and a user-facing endpoint creation guide.
- Per-donation-key authorization and effective expiry, same-channel mainstream auto-approval, account-wide key status views, and administrator-controlled bilingual donation guidance.
- Public charity capability lists currently routable enabled models with exact base pricing, effective promotional pricing and promotion windows, without revealing donated resources. Administrator and level-5 management views retain the rolling success count and rate for the most recent 100 completed calls.
- Bilingual React pages for the new activities, announcements, games, reports, legal holds, mainstream channels, donation-key overview, and recovery states. The game center now ships three original local illustrations.
- Owner-configurable per-key concurrency and rolling-minute request limits. Personal and charity model calls share the limits, skip busy connections automatically, and expose the owner's settings read-only to authorized administrators and stewards.
- Per-model charity routing choices: saved order, uniform random, or expiry-weighted random. Existing models keep expiry weighting through a compatible database update.
- Separate user IDs and copyable Discord IDs in administrator user management, with a compact mobile layout.
- Personal credit history with reason, time and income/expense filters, adjustable page sizes, direct page navigation, and links to the account's own request logs. Donation rewards do not expose another caller's requests.

### Changed

- Account export is schema version 4. It adds safe endpoint origin, authorized/effective donation-key expiry and source provenance, activity/game state, while retaining hard collection and total-size bounds and excluding secrets, reports, other users, internal fingerprints, and administrative material.
- Donation routing filters eligibility and connector capability before freezing the selected saved-order, uniform-random, or expiry-weighted candidate order without replacement. Random strategies fail closed on entropy failure before any request, reservation, claim, or ledger write.
- Debug Hub uses a version-2 bounded SSE protocol with explicit dry/live outcomes, safe stop/replace cancellation, and no persisted request or response content.
- The API uses cursor pagination, strict closed JSON objects, explicit revisions, and idempotency keys for state-changing operations. Removed alpha routes have no compatibility shim.
- Frontend dependencies include `@eslint/js` 10.0.1, Vite 8.2.2, jsdom 30.0.1, and Testing Library React 16.3.3; TypeScript remains on 5.9.3.
- GitHub Actions dependencies are pinned to immutable commits for their documented release tags.
- Upstream response-header waits allow 900 seconds and the logical request budget is 1200 seconds, including retries and streaming. Deployment proxy examples use matching 1200-second timeouts.
- Donation guidance, descriptions, review reasons, and activity text render common Markdown formatting with inert HTML and safe links.
- LinkLink uses distinct pictures and animated connections along the confirmed match path, with a stable board position. Active games use less space for explanatory text on mobile.
- Fishing catch effects reflect rarity. RPS adds gesture reveals, tie progress, and a visible hidden ending after six consecutive free ties. Viewed results remain on the current page until dismissed or another game begins.
- Simplified user-facing copy and reduced internal configuration details in routine settings workflows.

### Fixed

- Charity attempts that fail before any validated successful output now refund the caller reservation and consume no donation quota or reward. Persisted response-start evidence keeps interrupted-request recovery consistent with live settlement.
- Upstream response-header timeouts retain their timeout cause instead of being mislabeled as caller cancellation.
- Administrator charity source groups accept current key-limit fields and display them read-only.
- Administrator login pages load the configured site name and icon from a minimal public branding projection.
- API access uses a clearer key icon, and the charity service-quality notice clarifies that its examples are non-exhaustive.
- Separate identifiers in review, channel, legal-hold and report tables, with readable field labels on narrow screens. Large connection lists support bounded scrolling and page filters; charity access supports model-name and pricing filters.
- The home donation total is labeled as accumulated donation rewards.
- Streaming providers that report cumulative token counts now update usage and billing correctly. Repeated snapshots are not summed, and malformed or regressing values remain unknown.
- Model connections can be selected across donations, endpoints, keys, filters and pages. Charity connection order can be adjusted before saving once.
- Charity and activity pages use the centered desktop width. Charity models, donation history and submission have separate tabs, and model creation previews the API model name.
- LinkLink starts a new paid game from completed summaries with a closable confirmation, and clears both selections after a failed pair.
- Mobile navigation keeps its close button visible and preserves the page position when opened after scrolling.
- Mobile request details fit the available page width when system scrollbars are present.
- Administrator setting descriptions use the displayed credit amounts and give a readable label for related charity pricing.
- Embedded asset manifests are reproducible and exclude their previous output, allowing verified rebuilds of an unchanged release.
- Existing endpoint credentials with an omitted default port survive normal restarts. Startup validation uses the same stored URL form as endpoint creation while still rejecting mismatched credential targets.
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
- Empty endpoints can be deleted safely. Resource lists distinguish mainstream channel names from Connector protocols, and closing a completed setup step clearly preserves the saved resource.
- Charity prices show struck-through original amounts, prominent effective discounts, accurate unlimited-offer labels, and browser-local promotion deadlines across mobile and desktop layouts.

### Compatibility and deployment

- Beta.1 accepts only a completely absent database set or a validated Generation 2 database with SQLite `application_id=0x4E425249` and `user_version=2`. Alpha and Generation 1 databases are not migrated or imported.
- Upgrading from alpha requires a deliberate fresh cutover after a verified complete snapshot. Three exact earlier beta.1 schemas support a normal additive update for charity routing, per-key request limits, and successful-response checkpoints, preserving existing data. An incompatible downgrade requires restoring the matching database/sidecars, release, environment, master key, unit, manifest, and checksums together.
- Startup environment-variable names are unchanged from alpha.3, but a fresh cutover resets every database-backed runtime setting and requires operator review or re-entry before opening the instance.
- The release is source-first and supports Linux/amd64 production deployments. Go 1.26.6, Node.js 22.22.3 or newer, and npm 12.0.1 are the build baseline; use `CGO_ENABLED=0 -tags dist -trimpath` after building both embedded web stations. No official precompiled binaries, containers, or installers are published.
- Public ingress remains limited to `/v1/models` and `/v1/chat/completions`; Anthropic compatibility is an upstream translation subset. Other API families, Dify App API, additional production platforms, multi-instance deployment, Gamepad support, and accessibility certification remain outside this release.

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

[1.0.0-beta.4]: https://github.com/waiting-here/NonbiriAPI/compare/v1.0.0-beta.3...v1.0.0-beta.4
[1.0.0-beta.3]: https://github.com/waiting-here/NonbiriAPI/compare/v1.0.0-beta.2...v1.0.0-beta.3
[1.0.0-beta.2]: https://github.com/waiting-here/NonbiriAPI/compare/v1.0.0-beta.1...v1.0.0-beta.2
[1.0.0-beta.1]: https://github.com/waiting-here/NonbiriAPI/releases/tag/v1.0.0-beta.1
[1.0.0-alpha.3]: https://github.com/waiting-here/NonbiriAPI/releases/tag/v1.0.0-alpha.3
[1.0.0-alpha.2]: https://github.com/waiting-here/NonbiriAPI/releases/tag/v1.0.0-alpha.2
[1.0.0-alpha.1]: https://github.com/waiting-here/NonbiriAPI/releases/tag/v1.0.0-alpha.1
