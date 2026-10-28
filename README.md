# NonbiriAPI

[简体中文](README.zh-CN.md)

NonbiriAPI is a self-hosted AI endpoint manager and OpenAI-compatible gateway. Users manage services, endpoints and service-scoped API keys, maintain model lists per endpoint/key pair, and configure personal or shared charity routing. Revocable CallerKeys authenticate gateway calls. Optional activities and games share server-authoritative accounting and lifecycle rules.

**v1.3.1 (unreleased)** adds Responses and ChatGPT assistant sources, personal memories and separate management operation records. Existing databases require an explicit upgrade to schema 41; account export remains format 31. GitHub Releases provide source code; build instructions are below. Version history is in [CHANGELOG](CHANGELOG.md).

## Architecture

One Go process serves two embedded React stations and one local SQLite database. User and administrator stations have separate hosts and authorization boundaries. Production targets Linux/amd64; Windows supports development and build verification.

Model directories evaluate current availability in read-only SQLite snapshots,
with batched candidate and success-rate queries. Personal model lists use the
same bounded read pool. There is no complete-list cache or TTL; caller identity,
level, donation ownership and current quotas still affect the result. See
[database access](docs/database-access.md) and [runtime metrics](docs/deployment.md).
Availability counts each binding once and stops at its first eligible target,
avoiding repeated permission and quota checks for equivalent targets.

Lake Notes checkpoints reuse authoritative values within their transaction, and
activity event snapshots use the existing read pool. Revision, replay and lease
writes remain atomic. The [mixed-load experiment](docs/performance-validation.md)
reports both pools, real checkpoint completions and scheduling delay.

Writer connections reuse a bounded set of prepared write and SELECT statements,
with exclusive ownership for open result cursors. Independent
configuration reads use the read pool and reuse validation only when the full
current contents match. Database values are still read on every call; accounting,
authorization and availability remain authoritative.

Charity binding and candidate checks query only the selected donation/key catalog.
This avoids repeatedly aggregating unrelated catalog history while preserving
current authorization, revisions and atomic binding changes.

Attempt and request completion use small synchronous transaction batches when
work overlaps. Success still requires a durable commit, and anonymous queue
metrics distinguish application waiting from database pool waiting.

Level-5 stewards have 51 per-account action permissions, six numeric limits and
a four-position donation scope, configured by administrators or actual level-6
stewards. Standard scope, the default, requires at least one live donated key and every live
key to have a mainstream source or a current binding to a mainstream charity
model. Enhanced scope also includes entire donations with any tagged endpoint;
none closes donation access, and full uses the level-6 resource range.
Review, handling and maintenance share that scope and retain their individual
action grants. Identity visibility is separate. Actual account hierarchy and
administrator-only operations remain unchanged; revoked grants affect current
sessions, CallerKeys and pending work. See the
[action catalog, scopes and limits](docs/steward-permissions.md).

Level 6 can use shared endpoint and endpoint-policy management. Level 5 needs
explicit endpoint grants. Tags include User-built gateway, Steward-built gateway
and Incorrect entry alongside existing classifications.

Management donation lists support exact donor account and Discord ID filters.
User details provide complete retained donation history, including hidden owner
history, through a safe summary without credentials or key controls. Source
browsers initially show all retained sources, with an active-only filter.

Administrators manage complete price templates in a dedicated charity workspace,
with search, pagination, creation, editing and deletion. Administrators and
stewards with price-template read permission can copy a template into a model
form and adjust it before saving.
Models and templates support [recurring weekly discounts](docs/charity-discounts.md)
with configurable time zones and percentages. An enabled fixed-window discount
pauses the recurring schedule. Accepted requests keep their captured price.

Binding selectors show existing connections. Authorized managers can bind one
donated key to several charity models in one operation. Administrators and
stewards with the required preset and binding permissions can maintain channel
presets and apply them to every key in an approved donation. Custom-endpoint
donations can choose other channels' templates, with matching addresses and
protocols suggested first. Missing models and existing bindings are reported individually.

Models can use a [Cloudflare Worker](docs/cloudflare-egress.md) to forward
requests across providers without adding a mapping for each upstream. Personal
models enable forwarding by default; charity models disable it by default.
Administrators and stewards with egress-read permission can inspect the loaded
configuration. Existing AI Gateway mappings remain compatible when no Worker is configured.

| Area | Source and contract |
| --- | --- |
| Startup, recovery and HTTP composition | `main.go`, `internal/app/` |
| Stable schema and migrations | `internal/db/`, [database upgrades](docs/database-upgrades.md) |
| One-time database conversion | `cmd/nonbiri-upgrade-v1/` |
| Accounting, export, deletion and retention | `internal/ledger/`, `internal/lifecycle/`, [lifecycle](docs/data-lifecycle-checklist.md) |
| Connectors, forwarding and outbound policy | `internal/connector/`, `internal/forward/`, `internal/egress/` |
| Experimental management assistant | `internal/aiagent/`, `internal/agentrequest/`, [API contract](docs/api-contract.md#78-experimental-ai-assistant) |
| Shared components and separate stations | `web/src/shared/`, `web/src/user/`, `web/src/admin/` |
| Verification and deployment examples | `scripts/`, `.github/workflows/`, `deploy/` |

[Services](docs/service-connections.md) group endpoints and service-owned keys. Keys can be explicitly reused within a service; fetched and manual model lists belong to each endpoint/key pair. New or edited service and donation submissions authorize the selected protocol's complete service set. Existing donations retain their original authorization until the donor submits an edit; approved snapshots are not expanded automatically. Personal model binding selects endpoints, keys and models, preserving applicable call types. Credential rotation preserves approved charity versions until explicit review.

Default service setup asks for an address and API key, with additional keys and
advanced protocols available as needed. One save starts model fetching;
a failure preserves completed resources for recovery. Failed fetches retain the
last successful list, and manual entries remain separately editable. Continue
directly to personal model setup or donation.

Reviewers approve or reject the whole current pending donation submission with
one decision and an optional note. The complete versioned pending set, audit and
receipt commit together; a changed item applies nothing. Unknown results recover
with the original request. Previous approved versions remain intact when a
replacement is rejected. Successful reviews continue into authorized charity-model
configuration, where bindings retain each selected service subset.

Outbound requests include a short-lived, authenticated opaque origin header.
A valid returned marker creates an unresolved administrator alert while the
request continues. Recording uses a bounded queue and deduplication; it does
not change rewards, keys or account status. A gateway can remove the header,
so this is a review signal rather than proof of resource ownership.

When creating a custom entry, matching mainstream presets show their current
details. Users may explicitly adopt one entry or continue with the custom draft;
suggestions grant no credential or donation authorization.

The registry has 13 connector types covering OpenAI-compatible, dedicated Chat and Responses, Anthropic, Gateway v3/v4, Gemini, Decisions, System One and four explicit rerank protocols. Both Gateway versions support native text, image generation, embeddings and rerank with complete base URLs and no version probing. Direct HTTP operations include text, images, audio, embeddings, moderation and rerank, with SSE where supported. Chat Completions and Responses are separate model interface and binding choices, with bounded conversion paths where supported; other services remain independently selectable. See [direct inference](docs/direct-inference.md) for the operation matrix and excluded persistent/realtime resources, and the [API contract](docs/api-contract.md) for ownership, credentials, SSRF, accounting and cancellation rules.

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
Safe validation diagnostics also identify the rejected field, recorded reason
and corrective guidance in the caller's own log and authorized management views,
including JSON/CSV exports. They expose no submitted value or secret; older
records without a saved cause remain explicitly unknown.

Browser appearance controls offer light/dark/system themes and three glass intensities, with manual choices taking priority over system preferences. Focus mode supports viewing the background and returning to the previous reading surface.

Anonymous minute metrics separate pool acquisition from transaction work for
game checkpoints, request accounting and background maintenance. In-flight
elapsed time remains visible across reporting windows; see the runtime metrics
guide before comparing completed latency with current occupancy.

The optional Gacha Simulator includes layered SP artwork. Tomorrow, Still
Unwritten adds a single ChatGPT card with animated light/dark site appearances;
its limited-pool template remains unpublished. Amber Vow keeps its
character beside desktop content, with matching static fallback and enhanced
artwork; composition and source details are in the [art guide](docs/gacha-art.md).
R-18 built-in artwork is disabled across the site by default and can be enabled
by an administrator. Candidate lists label rated artwork, while permitted individual viewing
stays unobstructed. Sweet Hide-and-Seek includes two adult-only R-18 limited SSR
cards, a theme illustration and an independent pool cover with adult nudity and
transparent clothing. These assets and their derivatives are included in the
public source distribution. Its unpublished pool
template gives the two featured cards equal weight. Collections and saved choices
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
Administrators and stewards with endpoint-backoff permission can configure
additional recognition rules per complete endpoint, shared by its donated keys.
Inspecting loaded egress routes requires the separate egress-read permission. [Google routing](docs/cloudflare-egress.md#google-official-channels)
covers AI Studio and Vertex AI's OpenAI-compatible Chat endpoints.

The experimental [management assistant](docs/api-contract.md#78-experimental-ai-assistant)
supports Chat Completions APIs, external Responses APIs and a connected ChatGPT
account. Administrators configure a default source, purpose overrides and individual
steward overrides; removing an override restores inheritance. Source selection
does not change the operator's permissions, and failures never silently switch to
another source. Existing API settings remain usable. Local context and response
reserve budgets are separate from an upstream output cap.

To connect ChatGPT, download an instance target in administrator AI settings,
then run this repository's helper on the computer with your browser:

```sh
go build -o nonbiri-chatgpt-login ./cmd/nonbiri-chatgpt-login
./nonbiri-chatgpt-login --target instance-target.json --output connection.json
```

On Windows, use `nonbiri-chatgpt-login.exe`. Open the printed link, authorize
NonbiriAPI, then import the encrypted `connection.json` into the same instance
over HTTPS and delete the transfer file. Keep the local host-ID file; choose a
new output filename on another attempt. One ChatGPT connection serves the
instance, with request-triggered token refresh and account-specific model
discovery. Availability depends on the account and current preview policy.
Connection status shows update and token-expiry times. Remaining
account quota and reset time are unavailable through the public API; use
[ChatGPT usage settings](https://chatgpt.com/settings/usage). Local 24-hour usage
counts only retained completed ChatGPT requests, including earlier connections,
and decreases when those details are cleared. See the [connection guide](docs/chatgpt-agent.md)
and OpenAI's [self-hosted instructions](https://developers.openai.com/siwc/token-sharing-open-source/self-hosted-vms)
and [preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations).

[Built-in presets](docs/ai-agent-presets.md) provide editable messages, memories
and tool descriptions. Default chat messages include `{{interlocutor}}` and
`{{personal_memory}}`; existing custom templates stay unchanged. Chat remains off
by default. Each authorized operator has
one conversation with editable context and compaction. Shared chat and review
memories are editable only by administrators; personal memory is maintained by
its authorized interlocutor and administrators. It remains available to administrators after
account deletion and is excluded from ordinary personal exports. Conversation
changes require approval of an exact plan, recheck current authority and versions,
then commit together or roll back.

Automatic review is separate and off by default. Preview saves advice; automatic
mode can approve and bind existing charity models atomically after deterministic
rules have run. It handles only new or modified pending submissions, with no
backlog scan or manual enqueue. Each actual model HTTP call joins the shared FIFO
queue and counts against global limits, without personal RPM or credit charges.
Automatic reviews record the administrator who authorized their configuration.
Donation review pages and assistant tools show the submitted mainstream
subscription/API category and the endpoint's current effective tags.

Administrators can inspect full assistant request/response logs and clear a
confirmed selection. Conversations, memories and cumulative token statistics
remain independent administrator assets; review evidence follows the donation
lifecycle. Ordinary request logs retain their existing rules.

Separate management records cover writes from the upgrade onward, with success,
failure, refusal and partial or unchanged outcomes. They distinguish browser
actions, CallerKey automation, mainstream rules, automatic AI reviews and approved
AI plans, retaining actor and authorizer snapshots, safe targets and results.
Ordinary reads are excluded. Administrators can search grouped summaries and
paged details and clear a filter, time range or all retained records through a
resumable batch job. Records have no automatic expiry, survive account/resource
deletion and are excluded from personal exports. Cleanup retains its own summary
and records created after its cutoff.

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

Fresh databases start in maintenance with public features closed and use schema 41.
The service opens only the exact current schema; older, newer, unknown or
incomplete storage is rejected before migration, repair or recovery writes.
For exact registered schema 16–26 and 33–40 sources, preserve a complete recovery
set before deliberately running the matching standalone
[upgrader](cmd/nonbiri-upgrade-v1/README.md) with `--check` or `--apply`.
The service, startup scripts and deployment helpers must not apply upgrades
implicitly. Account export uses format 31. Schema versions change when
storage requires it, independently of software patch releases.
Use the matching binaries' schema descriptors to check accepted sources.
Upgrades are one-way: a server expecting an older or different schema cannot
open the upgraded database, and there is no schema downgrade migration. Do not overwrite newly accepted data with an
old snapshot.
Source documentation does not imply an instance deployment.

[Deployment](docs/deployment.md) covers systemd, proxies, immutable artifacts, upgrades and complete-snapshot recovery. Startup variables and online settings have one reference in [configuration](docs/configuration.md).

## Technical guides

| Topic | Documentation |
| --- | --- |
| HTTP and automation | [API contract](docs/api-contract.md), [steward permissions](docs/steward-permissions.md), [steward integration](docs/steward-automation.md) |
| Gateway and charging | [Gateway controls](docs/gateway-model-controls.md), [context prices](docs/charity-context-pricing.md), [managed headers](docs/charity-session-headers.md) |
| Game integration | [Game modules](docs/game-modules.md), [randomness](docs/game-randomness.md), [duels](docs/duel-games.md), [AI players](docs/ai-players.md) |
| Game storage and rules | [Blackjack](docs/blackjack.md), [Steady Catch](docs/steady-catch.md), [AI Gwent](docs/ai-gwent.md), [Lake Notes](docs/lake-notes.md), [Fat Fish editor](docs/fatfish-editor.md) |
| Collections and activities | [Gacha](docs/gacha.md), [art assets](docs/gacha-art.md), [periods](docs/limited-activities.md), [image activity](docs/image-activity.md), [rewards](docs/rewards.md) |
| Candidate verification and publication | [Release checklist](docs/release-checklist.md), [repository settings](docs/github-settings.md) |

Instance operators maintain privacy and terms text for their actual data handling. Upstream providers apply their own policies; `store:false` does not guarantee zero retention. Restricted error diagnostics can contain upstream-echoed content; access and retention follow the lifecycle guide.

Report vulnerabilities through [SECURITY.md](SECURITY.md). Code is licensed under [AGPL-3.0](LICENSE). Attribution is in [NOTICE](NOTICE), [frontend notices](web/THIRD_PARTY_NOTICES.md) and [audio credits](web/src/shared/assets/game-audio/NOTICE.md).
