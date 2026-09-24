# Changelog

All notable changes to NonbiriAPI are documented here.

Each version entry describes its source and compatibility boundary; a release tag is not an upgrade authorization.

## Unreleased

The changes below include development toward 1.0.0-rc.3. The published rc.2 tag remains unchanged; no new release or deployment is implied. The supported upgrade source for this code is rc.2 maintenance commit `db959c64674afc531046a63066de0464725d439c`, preserving business data, configuration and instance legal text.

### Added

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
