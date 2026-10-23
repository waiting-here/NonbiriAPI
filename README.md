# NonbiriAPI

[简体中文](README.zh-CN.md)

NonbiriAPI is a self-hosted AI endpoint manager and OpenAI-compatible gateway. Users manage service connections, shared encrypted credentials, model discovery and routing, then use revocable CallerKeys for personal or shared charity calls. Optional activities and games share server-authoritative accounting and lifecycle rules.

**v1.0.0 is the first stable release.** GitHub Releases provide source code; build instructions are below. Version history is in [CHANGELOG](CHANGELOG.md).

## Architecture

One Go process serves two embedded React stations and one local SQLite database. User and administrator stations have separate hosts and authorization boundaries. Production targets Linux/amd64; Windows supports development and build verification.

Model directories evaluate current availability in read-only SQLite snapshots,
with batched candidate and success-rate queries. Personal model lists use the
same bounded read pool. There is no complete-list cache or TTL; caller identity,
level, donation ownership and current quotas still affect the result. See
[database access](docs/database-access.md) and [runtime metrics](docs/deployment.md).

Lake Notes checkpoints reuse authoritative values within their transaction, and
activity event snapshots use the existing read pool. Revision, replay and lease
writes remain atomic. The [mixed-load experiment](docs/performance-validation.md)
reports both pools, real checkpoint completions and scheduling delay.

Writer connections reuse a bounded set of prepared write and SELECT statements,
with exclusive ownership for open result cursors. Independent
configuration reads use the read pool and reuse validation only when the full
current contents match. Database values are still read on every call; accounting,
authorization and availability remain authoritative.

Attempt and request completion use small synchronous transaction batches when
work overlaps. Success still requires a durable commit, and anonymous queue
metrics distinguish application waiting from database pool waiting.

Level-5 stewards can inspect user accounts, adjust the five inference RPM/concurrency
overrides or revoke CallerKeys for strictly lower-level users, and ban level 1–4
users. They cannot change other account settings or credits, lift a ban, or replace
an active ban. They can approve or reject every pending donation. Administrators and level-6
stewards can force-reject any approved donation, including manual approvals.
Administrators can revoke any non-administrator user's CallerKey; level-6
stewards use the same strictly lower-level rule. Revocation leaves browser
sessions and upstream credentials usable, and the user may create a replacement.

Administrators manage complete price templates in a dedicated charity workspace,
with search, pagination, creation, editing and deletion. Administrators and
level-6 stewards can copy a template into a model form and adjust it before saving.
Models and templates support [recurring weekly discounts](docs/charity-discounts.md)
with configurable time zones and percentages. An enabled fixed-window discount
pauses the recurring schedule. Accepted requests keep their captured price.

Binding selectors show existing connections. Authorized managers can bind one
donated key to several charity models in one operation. Administrators and
level-6 stewards can maintain channel presets and apply them to every key in an
approved donation; missing models and existing bindings are reported individually.

Models can use a [Cloudflare Worker](docs/cloudflare-egress.md) to forward
requests across providers without adding a mapping for each upstream. Personal
models enable forwarding by default; charity models disable it by default.
Administrators and level-6 stewards can inspect the loaded configuration. Existing
AI Gateway mappings remain compatible when no Worker is configured.

| Area | Source and contract |
| --- | --- |
| Startup, recovery and HTTP composition | `main.go`, `internal/app/` |
| Stable schema and migrations | `internal/db/`, [database upgrades](docs/database-upgrades.md) |
| One-time database conversion | `cmd/nonbiri-upgrade-v1/` |
| Accounting, export, deletion and retention | `internal/ledger/`, `internal/lifecycle/`, [lifecycle](docs/data-lifecycle-checklist.md) |
| Connectors, forwarding and outbound policy | `internal/connector/`, `internal/forward/`, `internal/egress/` |
| Shared components and separate stations | `web/src/shared/`, `web/src/user/`, `web/src/admin/` |
| Verification and deployment examples | `scripts/`, `.github/workflows/`, `deploy/` |

[Service connections](docs/service-connections.md) group explicitly authorized entries around shared credentials and an independent model directory. OpenAI-compatible and Gateway entries select their native services; each model selects its applicable targets. Credential rotation preserves approved charity versions until explicit review.

Donation lists summarize decisions across their service uses. Reviewers can
approve or reject all pending uses in one donation or key, confirm the exact
selection and recover partial or uncertain results without expanding it.

Outbound requests include a short-lived, authenticated opaque origin header.
A valid returned marker creates an unresolved administrator alert while the
request continues. Recording uses a bounded queue and deduplication; it does
not change rewards, keys or account status. A gateway can remove the header,
so this is a review signal rather than proof of resource ownership.

When creating a custom entry, matching mainstream presets show their current
details. Users may explicitly adopt one entry or continue with the custom draft;
suggestions grant no credential or donation authorization.

The registry has 13 connector types covering OpenAI-compatible, dedicated Chat and Responses, Anthropic, Gateway v3/v4, Gemini, Decisions, System One and four explicit rerank protocols. Both Gateway versions support native text, image generation, embeddings and rerank with complete base URLs and no version probing. Direct HTTP operations include text, images, audio, embeddings, moderation and rerank, with SSE where supported. Chat and Responses share one model service selection and have bounded conversion paths; other services remain independently selectable. See [direct inference](docs/direct-inference.md) for the operation matrix and excluded persistent/realtime resources, and the [API contract](docs/api-contract.md) for ownership, credentials, SSRF, accounting and cancellation rules.

[Decisions](docs/content-protocols.md#decisions) supports up to 400 questions,
text/image/JSON question content and structured JSON evidence in user messages.
Native JSON values and numbers are preserved within bounded requests.

Charity pricing supports request, token and search-unit billing, with independent input/output modality prices, donor rewards and long-context tiers. Missing standard classifications fall back to aggregate prices per side. [Pricing rules](docs/charity-context-pricing.md) explain exact units, reservation and failed-generation usage. Light and heavy requests have separate concurrency pools shared by personal and charity calls, and separate charity RPM windows; all calls still share global limits.

Personal and charity models share [managed session-header controls](docs/charity-session-headers.md), including five formats and a zero idle lifetime that generates a value per logical request while preserving it for internal retries, without guaranteeing uniqueness. Model settings and request adaptation save separately.

Authorized request-adaptation configuration reads show saved fixed-header and
body values without response-protection markers. Marked items stay hidden as a
whole. Mutation receipts and account exports continue to omit those values.

Administrator and level-5/6 log pages show [global concurrency and RPM](docs/api-contract.md#global-pressure), with approximate 24-hour peaks and persistent observed maxima. Narrowly verified original caller-parameter errors leave donated-key failure streaks unchanged; ambiguous errors keep their existing treatment.
Short charity-request rejections explain the minimum-length requirement in
English or Chinese, with the existing status, code, counts and penalty rules.

Browser appearance controls offer light/dark/system themes and three glass intensities, with manual choices taking priority over system preferences. Focus mode supports viewing the background and returning to the previous reading surface.

Anonymous minute metrics separate pool acquisition from transaction work for
game checkpoints, request accounting and background maintenance. In-flight
elapsed time remains visible across reporting windows; see the runtime metrics
guide before comparing completed latency with current occupancy.

The optional Gacha Simulator includes layered SP artwork. Amber Vow keeps its
character beside desktop content, with matching static fallback and enhanced
artwork; composition and source details are in the [art guide](docs/gacha-art.md).
Administrators can disable R-18 built-in artwork across the site; this is on by
default. Candidate lists label rated artwork, while permitted individual viewing
stays unobstructed. Current assets are general. Collections and saved choices
remain intact when artwork is disabled; see [artwork availability](docs/gacha.md#artwork-availability).

The mystery shop offers daily bundles of ten standard or limited tickets for
10,000 credits each, using game credits first. Each bundle has its own daily limit.
Management request logs support an exact Discord ID filter alongside the called
charity model; additional filters include the upstream model used.
Authorized managers can open the corresponding charity model or each attempt's
donated-key settings from a log, then return to the same filters and page.
After choosing a key for a charity service connection, fetch its model list again
or open its key settings directly, then return to the same selection.

Charity calls automatically wait after recognized recoverable quota errors and
recover through one real charity request. No per-key setup is required.
Retry headers take precedence; other errors retain the consecutive-failure policy. See the
[smart-backoff contract](docs/api-contract.md#413-smart-donated-key-backoff).
Administrators and level-6 stewards can configure additional recognition rules
per complete endpoint, shared by its donated keys, and inspect loaded egress
routes. [Google routing](docs/cloudflare-egress.md#google-official-channels)
covers AI Studio and Vertex AI's OpenAI-compatible Chat endpoints.

## Build and develop

Use **Go 1.26.9**, **Node.js 22.23.4+ (22.x), 24.22.1+ (24.x), or 26.12.1+ (26.x)**, **npm 12.0.1** and Bash; Windows uses Git Bash. The finished binary needs neither Node.js nor a C runtime.

```sh
npm --prefix web ci
npm --prefix web run build
CGO_ENABLED=0 go build -buildvcs=true -tags dist -trimpath -o nonbiriapi .
```

`-tags dist` embeds both built stations; without it, the binary serves development placeholders. [CONTRIBUTING](CONTRIBUTING.md) is the canonical entry for local checks, browser setup, shared fixtures, CI selection and protected-branch changes. Update implementation and affected tests together.

## Configure and deploy

[First-run setup](docs/first-run-setup.md) covers private paths, the encryption key, Discord OAuth and public origins. Copy [admin.env.example](admin.env.example) outside the checkout, replace placeholders and load it before starting. Preserve the database with its matching key.

Fresh databases start in maintenance with public features closed. Fresh storage uses schema 35. Exact published schema 33 and 34 databases receive atomic startup upgrades; schema 33 adds the history indexes, and schema 34 updates the donation catalog authorization rule. Current schema 35 opens directly. Account export remains 29. Existing published schema 16–26 databases use the separate offline converter; historical conversion code is excluded from the service. Later v1.x releases retain an upgrade path from every earlier stable v1.x schema. Older binaries need not read newer databases. Source documentation does not imply that an instance has released or deployed the update.

[Deployment](docs/deployment.md) covers systemd, proxies, immutable artifacts, upgrades and complete-snapshot recovery. Startup variables and online settings have one reference in [configuration](docs/configuration.md).

## Technical guides

| Topic | Documentation |
| --- | --- |
| HTTP and automation | [API contract](docs/api-contract.md), [steward integration](docs/steward-automation.md) |
| Gateway and charging | [Gateway controls](docs/gateway-model-controls.md), [context prices](docs/charity-context-pricing.md), [managed headers](docs/charity-session-headers.md) |
| Game integration | [Game modules](docs/game-modules.md), [randomness](docs/game-randomness.md), [duels](docs/duel-games.md), [AI players](docs/ai-players.md) |
| Game storage and rules | [Blackjack](docs/blackjack.md), [Steady Catch](docs/steady-catch.md), [AI Gwent](docs/ai-gwent.md), [Lake Notes](docs/lake-notes.md), [Fat Fish editor](docs/fatfish-editor.md) |
| Collections and activities | [Gacha](docs/gacha.md), [art assets](docs/gacha-art.md), [periods](docs/limited-activities.md), [image activity](docs/image-activity.md), [rewards](docs/rewards.md) |
| Candidate verification and publication | [Release checklist](docs/release-checklist.md), [repository settings](docs/github-settings.md) |

Instance operators maintain privacy and terms text for their actual data handling. Upstream providers apply their own policies; `store:false` does not guarantee zero retention. Restricted error diagnostics can contain upstream-echoed content; access and retention follow the lifecycle guide.

Report vulnerabilities through [SECURITY.md](SECURITY.md). Code is licensed under [AGPL-3.0](LICENSE). Attribution is in [NOTICE](NOTICE), [frontend notices](web/THIRD_PARTY_NOTICES.md) and [audio credits](web/src/shared/assets/game-audio/NOTICE.md).
