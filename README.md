# NonbiriAPI

[简体中文](README.zh-CN.md)

NonbiriAPI is a self-hosted API endpoint manager and OpenAI-compatible ingress gateway. It lets each user manage their own upstream endpoints and credentials, discover upstream models, define user-owned platform model names, and call those models through a single `CallerKey`.

> **Current release:** [v1.0.0-rc.4](https://github.com/waiting-here/NonbiriAPI/releases/tag/v1.0.0-rc.4), a Linux/amd64 source prerelease dated 2026-09-28 UTC. Build from the tagged source; no official precompiled binaries are provided. Review the deployment, privacy and security documentation before exposing an instance to users.
>
> **rc.4 highlights:** This source release adds image-model capability and size-pricing controls, expanded audit and account-protection workflows, a Fat Fish editor and local game, updated game boards and live presentation, private battle presets, charity dispatch and request-adaptation controls, and typed schema-11 account exports. See the [changelog](CHANGELOG.md) for the full scope. Feature availability on a running instance depends on its deployed build; publication does not itself deploy an instance.
>
> **Compatibility:** The formal rc.4 upgrade source is the complete rc.3 repair database at commit `37e060ab0d0f29d632fe6b8036839b413388812a`, preserving existing data, credentials, configuration and instance legal text. A separate verified path supports the exact preceding deployed source at commit `4e06025c6bf23fbb0f34db96673b45ed01c42e97` (tree `6af9d8349d9049197366f29984e2e413090b7814`); other intermediate schemas are unsupported. Generation 2 (`application_id=0x4E425249`, `user_version=2`) and Linux/amd64 remain the target. Alpha/Generation 1 requires a fresh cutover. See the [deployment guide](docs/deployment.md#database-compatibility-and-version-changes).
>
> Source repository: [github.com/waiting-here/NonbiriAPI](https://github.com/waiting-here/NonbiriAPI)

See [Unreleased](CHANGELOG.md#unreleased) for development changes. Feature availability depends on the deployed build. Lake Notes and its four exchange directions start disabled.

## Highlights

- Optional game-credit loans show principal, fee, game credits received, interest and general credits deducted. Rolling seven-day net-profit boards cover all games, Fishing, Blackjack and Bidding alongside the existing spending and profit boards; losses offset gains, and charity visibility keeps its own opt-in setting.
- Six user levels separate full level-6 stewards from level-5 trainees. Administrators appoint full stewards; administrators and full stewards appoint trainees. Trainees maintain only marked mainstream charity models and eligible donated keys, with shared-key impact shown before changes.
- Administrators and full stewards can inspect bounded original upstream failures and request-source facts, and review manual risk signals for sustained quota use, shared IPs, client clues and selected API probes. These signals are evidence for review, not proof of a client's identity or automatic punishment.
- Administrator-only economy auditing separates issuance, retirement, transfers and stock for four assets. Optional inactivity policies start disabled and support previews, grace periods, general/game balance decay and reversible protective bans.
- A reusable limited-activity framework launches the picture-book activity with separate draft-paper and brush balances, private administrator configuration, a server-controlled queue, and memory-only images. Users can review an exact price without generating or spending currency. The built-in integration and model capabilities are automatic; administrators manage the endpoint, model availability, and paper/brush prices. See the [activity guide](docs/image-activity.md); image generation is confined to this activity, not ordinary personal or charity API routes.
- The picture-book activity uses an illustrated Renge cover and a four-frame waiting animation at two seconds per frame. The animation respects reduced-motion settings and can pause manually or when its tab is in the background. Upstream status checks normally run every six seconds; a longer valid bounded `Retry-After` may extend the wait.
- New donations require an immutable yes/no choice about manual Discord public thanks. Charity calls exclude the caller's donated keys except for current level-6 stewards. Charity models support top-level parameter exclusions, a half-price donor-reward form action and a public 24-hour success rate with sample counts.
- Automatic penalties have durable violation windows, safe personal summaries and authorized management history. Search and filters cover complete paginated resources; authorized managers can trace a donated key back to its associated models.
- New character passives, layer-by-layer resistance, clear suit colors, mobile quick stakes and synchronized feedback across six games preserve server-authoritative results. Battle follow-up cues grow across a round and distinguish normal, partial and complete resistance.

- Turn-based battles offer an optional browser-local tutorial, ten scripted rounds ending in a narrow win, concise effect cards and linked player rules. Live turns warn below five seconds; overload highlights the actual depleted resources. Closed games and modes disable matching while keeping learning and history available.
- Turn-based Battle Minigame (Test) lets each account save up to 10 private **Custom presets** across devices. Saving over a slot requires explicit confirmation; loading fills pre-match choices only and never queues or charges. Both modes use 30-second planning periods for new matches.
- Banned Discord sign-ins open the site's branded 403 page. The charity catalog avoids repeating provider/model details already included in the complete model name.
- Donors, administrators and stewards can set each donated key's failure threshold (default 10). Zero prevents error-triggered disablement and displays a persistent warning; saving keeps the count and immediately recalculates that state. Steward CallerKeys can read and edit the policy through the [automation API](docs/steward-automation.md).
- Gateway preserves both OpenAI output-budget fields. Administrators manage exact-target reasoning, storage and cache capabilities online, including explicit Anthropic end markers and tool-result text arrays; new requests use saved changes immediately. See [Gateway model controls](docs/gateway-model-controls.md).
- Gateway cost attribution is an administrator setting, off by default. Enabling it sends a server-generated user-and-origin pseudonym; Debug shows only whether it was sent. See the [Gateway compatibility matrix](docs/api-contract.md#24-native-ai-sdk-gateway-v3-compatibility), including the tested Runable embedding limitation.
- OpenAI-compatible `/v1/models`, `/v1/chat/completions`, and `/v1/embeddings` ingress. Chat supports OpenAI-compatible, Anthropic-compatible and native AI SDK Gateway v3 upstreams; embeddings support OpenAI-compatible and the strict Gateway text subset.
- Discord OAuth user sign-in and a separate administrator station.
- Per-user endpoints, mainstream channel templates, encrypted upstream credentials, automatic/manual model catalogs, platform model names, and a guided endpoint → key → model connection workflow.
- Ordered/random personal routing, ordered/uniform-random/expiry-weighted/cache-balanced charity routing, opt-in pre-commit retry, user concurrency limits, and owner-configured per-key concurrency/RPM shared by personal, charity, and live diagnostic calls. Owners and charity managers can configure bounded request-header and body adaptation within their authorized resources.
- Personal and charity models can preserve the caller's chat stream mode (default), force JSON upstream or force SSE upstream. The caller keeps its requested format; JSON callers still face client/proxy idle timeouts while waiting. See the [transport contract](docs/api-contract.md#22-post-v1chatcompletions).
- Administrator and level-6 request logs support literal, ASCII case-insensitive charity-model filtering over the name saved at call time, including exports.
- SSRF, DNS-rebinding, redirect, proxy, response-size, timeout, cancellation, concurrency, and streaming safeguards. Accepted chat streams send idle SSE comments every 20 seconds while waiting for output, including allowed retries.
- Encrypted-at-rest upstream secrets and safe ordinary lists, errors and exports. Restricted original-error diagnostics can contain input or credentials echoed by an upstream; the [data policy](docs/data-lifecycle-checklist.md) explains this exception.
- Request metadata, usage accounting, retention cleanup, account export/deletion, issues, alerts, and runtime limits.
- Account export schema 12 includes safe account and game records, Lake Notes progress, model-role settings, named private presets and retained automation results. Raw errors, source facts, risk evidence, prompts, images, credentials, administrator configuration and the identities of other users remain excluded.
- Separate general and game wallets with individually controlled check-ins. Administrators can optionally limit each person to one of the two check-ins per site day; the user page shows the rule and both check-in cards together. Games spend game credits first, then general credits; eligible refunds return their original assets. API calls and Thursday contributions use general credits. Daily welfare pays game credits, and 22 once-only newcomer tasks across all six games award 60,000 general credits in total.
- Shared user-limit, effective-level filtering and announcement management for administrators and L6 stewards. Full stewards can modify only other current L1–L5 users, cannot delete accounts or change cumulative donor credit, and can reset donated-key failure streaks in bounded batches across a complete selected result set. Donors can reset their own eligible keys.
- Fishing defaults to 100% gross RTP on fresh databases, preserving existing settings on upgrade. Each catch contributes separately rounded platform, welfare and Thursday cuts, defaulting to 1% each, with gross and net rewards displayed.
- Credits, check-in with a server-configured balance gate, personal credit history, donation-backed charity routing, per-key donation expiry and usage limits, and full-steward co-management. Authorized administrator and L6 logs expose routed key identifiers and logical-request charges; ordinary charity callers do not receive those details.
- Donated keys can combine lifetime and recurring total/input/output Token limits with call and credit limits. Each Token dimension is optional and all configured limits apply together; split limits use explicit input/output reservations. Input includes uncached, cache-write and cache-read Tokens once each. Existing totals are preserved without guessing historical splits. Reset or sliding windows support 1 hour, 5 hours, a day, a week or a month. Daily, weekly and monthly resets can use an exact date and time in the rule time zone, with a preview of the next three resets; monthly rules retain their original day after shorter months. These are charity budgets, not TPM limits; personal calls can consume additional upstream capacity. Full managers and scoped trainees maintain eligible keys; donors can inspect their own limits. Token-priced charity models retain an optional per-model credit reserve, with blank values inheriting the global setting.
- User and administrator resource lists have bounded server-side pagination with 10/20/50/100 page sizes, direct page navigation, filter and page restoration after returning or refreshing, and an independent browser-local page-size preference for each list.
- The charity model catalog provides plain-text descriptions, allowed-level sets, explicit availability reasons, and source/key browsing for authorized managers. The catalog can show configured models even when the current caller cannot use them; the public API remains limited to currently usable models.
- Administrators and full stewards can fetch model lists for a single available donated key, every available key in one donation, or all available donated keys. Trainees can discover keys within their mainstream model scope. Bulk operations cover other pages, show progress and support stopping further requests. Failed fetches preserve the existing catalog; manual entries and model bindings remain unchanged.
- Ordinary user pages use browser-local time, including server-resolved daylight-saving gaps and repeated times. Administrator and steward pages use the site's fixed time zone, with a shared notice when it differs from the browser. Recurring quota rules retain their selected business time zone separately from ordinary timestamp display.
- Daily welfare, the Thursday pooled activity, bilingual announcements, and public credential-theft reporting with administrator review. Creating a Thursday period automatically selects the next Thursday at 00:00 Beijing time for 24 hours, including the following week when created on a Thursday. The administrator page displays this Beijing-time window; editing an existing period preserves its schedule.
- Default-off experimental chat policies: OpenAI-only per-key `store:false` requests and per-model tool flattening for OpenAI-compatible, Anthropic-compatible and Gateway v3 connectors. Role policy is configured separately per model; tools retain their own protocol.
- A memory-only Debug Hub that starts in dry-run mode and requires explicit confirmation to send requests upstream. Live results are captured in the Debug page; the API caller receives a dedicated HTTP 422 debug response.
- LinkLink shares 2/3/5 hint or refresh opportunities per new board, adds 100 score points per unused opportunity on completion, and provides six per-user-best boards by size and 7/30-day window. Equal scores rank by earliest achievement. Ordinary matches avoid extra wallet/game-center reloads, and connection animations allow the next selection. Saved old games keep their original rules.
- A server-authoritative game center with Pond Fishing, LinkLink, three-player Rock Paper Scissors, Bidding Duel and Turn-based Battle Minigame (Test), including idempotent accounting, recovery, privacy-aware leaderboards, and bundled local artwork. Fishing opens on the rolling 30-day largest-length board, with the lifetime largest-length and rolling 30-day payout boards still available. Its transparent-background white-rice-themed blue fat fish Easter egg preserves the original legendary species and payout; length-board rows use a compact original species name while result details retain the original-species explanation.
- Server-generated upstream safety pseudonyms scoped to one user and one canonical upstream origin; see the [API contract](docs/api-contract.md#22-post-v1chatcompletions) for their rotation and privacy boundary.
- Redesigned bilingual React user/admin stations with responsive navigation, continuous resource workflows, safe Markdown guidance, and configurable site branding, embedded into a single Go binary.

The current source exposes the three OpenAI-compatible ingress routes listed above. OpenAI-compatible embeddings support text and Token ID inputs, single items and batches, float/base64 encoding, and optional output dimensions for personal and charity models. Models have no purpose classification: the request path selects the operation, and the upstream decides whether its model supports it. Rerank remains unsupported. An `anthropic-compatible` endpoint is translated behind that ingress; NonbiriAPI does not expose an Anthropic-native public endpoint. The `ai-sdk-gateway-v3` connector uses native Gateway routes; its strict subset includes text, tools, image input and text embeddings. Other OpenAI API families and connector types remain deferred. See the [API contract](docs/api-contract.md) for the strict Anthropic subset and token-limit rules.

Bidding Duel offers 13 simultaneous hidden-card rounds. Turn-based Battle Minigame (Test) combines five characters, eight harnesses and full skill/buff rules with a shared battery, server-authored event-paced settlement and dynamic resource feedback. The server commits the complete round before the client plays every step, without a fixed presentation duration cap. Uncapped API reserve and gold use numeric counters. Dedicated character, skill and harness illustrations are bundled locally. Both games support original-asset refunds, safe 30-day player history and administrator-only anonymous archives and resumable exports. See [the bilingual game guide](docs/duel-games.md).

The six games have dedicated covers; Fishing includes illustrated catches with an SVG fallback. Bidding Duel, Turn-based Battle Minigame (Test) and Blackjack include short sound effects. Turn-based Battle Minigame (Test) also has synchronized scene music. Sound and music start off and remember each game's choice in the browser. Account → Local preferences offers lightweight or lossless music, applied on the next music activation or game entry. Media sources and formats are documented in the [audio notice](web/src/shared/assets/game-audio/NOTICE.md).

The Fat Fish limited activity has an illustrated cover and optional background music: "Monkeys Spinning Monkeys" by Kevin MacLeod, under CC BY 4.0. It is delivered locally as the original MP3, with a separate music switch that starts off; the lightweight/lossless choice does not change this track. See its [media credits](web/public/assets/fatfish/NOTICE.md).

Blackjack shares one nine-seat table with a persistent waiting queue and a 5/20/5-second cadence aligned to each :00 and :30. Six-deck rules include splitting and doubling; each hand pays all net returns in general credits after frozen fees. The game starts disabled. See [Blackjack rules](docs/blackjack.md).

All six games provide private per-game seeds, opening commitments and terminal verification. See [randomness and phased disclosure](docs/game-randomness.md) for the protocol, independent verifier and its limits.

## Architecture

One binary serves two host-isolated stations:

- **User station:** user self-service APIs, `/v1/*`, and the user web application.
- **Admin station:** administrator APIs and the administrator web application.

Configure a user origin and a distinct administrator host. `NONBIRI_ADMIN_HOST` may be omitted when the derived `admin.<user-host>` name is correct; never expose both stations on the same hostname.

## Build from source

Build-time requirements:

- Go 1.26.6.
- Node.js 22.22.3 or newer and npm 12.0.1 for the web build.

Node.js is not needed to run the finished binary.

```sh
npm --prefix web ci
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
scripts/check-go.sh
CGO_ENABLED=0 go build -tags dist -trimpath -o nonbiriapi .
```

The untagged Go build embeds development placeholder pages. A usable binary must be built after `npm --prefix web run build` with `-tags dist`.

Before running it, copy `admin.env.example` to a **private path outside the checkout**, replace every `CHANGE_ME` value, and change the production-shaped `/etc` and `/var` paths for your environment. Generate the master key at the absolute path configured by `NONBIRI_MASTER_KEY_FILE`; do not create it in the repository. Then load that file and start the binary:

```sh
set -a
. /absolute/private/path/admin.env
set +a
./nonbiriapi
```

For the ordered key, permission, Discord, DNS, and reverse-proxy setup, follow [First-run configuration preparation](docs/first-run-setup.md).

## Configuration

`admin.env.example` documents the complete startup environment. The [configuration reference](docs/configuration.md) explains startup variables, administrator runtime settings, and private Discord trial gates. The required values include:

- `NONBIRI_MASTER_KEY_FILE` or `NONBIRI_MASTER_KEY` (exactly one; a 32-byte key).
- `NONBIRI_ADMIN_USERNAME` and `NONBIRI_ADMIN_PASSWORD`.
- `NONBIRI_DISCORD_CLIENT_ID` and `NONBIRI_DISCORD_CLIENT_SECRET`.
- `NONBIRI_SITE_BASE_URL`; optionally `NONBIRI_ADMIN_HOST` when the derived `admin.<user-host>` value is not suitable.

Keep `admin.env`, the master-key file, and the database outside the Git working tree. Never commit real credentials or a real database.

## VPS/systemd deployment

The intended first deployment model is a manually updated systemd service. See:

- [Deployment and systemd guide](docs/deployment.md)
- [Steward automation instructions for administrators](docs/steward-automation.md)
- [Example environment file](admin.env.example)
- [Example systemd unit](deploy/nonbiriapi.service.example)

The rc.4 release keeps Generation 2. Its formal upgrade source is the complete rc.3 repair database at `37e060ab0d0f29d632fe6b8036839b413388812a`; the exact preceding deployed tree `4e06025c6bf23fbb0f34db96673b45ed01c42e97` (`6af9d8349d9049197366f29984e2e413090b7814`) is a separately verified compatibility case. Other intermediate schemas are unsupported. Existing accounts, balances, donations, model bindings, games, credentials, settings and custom legal text are preserved; new activity assets start separately. A downgrade requires the complete matching stopped snapshot. Fresh databases keep maintenance enabled and public feature gates closed. Alpha/Generation 1 requires a fresh cutover.

Ordinary startup validates database identity, schema and credentials and recovers unfinished work before opening listeners. Full historical audits run separately with `./nonbiriapi maintenance verify`, using the normal private environment against a stopped database or trusted consistent copy. The command reads without repairing, migrating, starting workers or opening a listener; SQLite may create coordination files. See the [recovery contract](docs/api-contract.md#10-maintenance-recovery-and-retention).

The project is source-first and supports Linux/amd64 as its production target. Operators compile the exact release source commit on that target or use an equivalent controlled build pipeline. This source release provides no official precompiled binaries, container images, or installers; other production platforms are not supported.

## GitHub automation

The repository includes a read-only CI workflow. Pull requests use a conservative routine verification scope; unknown inputs fall back to full checks. Manual runs default to full ordinary Go and frontend coverage plus the complete concurrency-risk race catalog. Protected `master` changes go through those checks. Final candidates require full evidence; after merging, reuse passing evidence when the resulting tree and relevant inputs match, and verify changed inputs when they do not. CodeQL retains its own triggers. CI does not deploy the application. Release artifact automation is intentionally separate and will be added only after the supported targets and signing policy are decided.

## API

The release contract is documented in [docs/api-contract.md](docs/api-contract.md).

After creating a caller key in the user station:

```sh
curl https://api.example.com/v1/models \
  -H 'Authorization: Bearer nbk_REPLACE_WITH_YOUR_CALLER_KEY'
```

For chat completion requests, use a platform model name configured in the user station:

```sh
curl https://api.example.com/v1/chat/completions \
  -H 'Authorization: Bearer nbk_REPLACE_WITH_YOUR_CALLER_KEY' \
  -H 'Content-Type: application/json' \
  -d '{"model":"provider/model","messages":[{"role":"user","content":"Hello"}]}'
```

For embeddings, select an upstream model that supports the operation:

```sh
curl https://api.example.com/v1/embeddings \
  -H 'Authorization: Bearer nbk_REPLACE_WITH_YOUR_CALLER_KEY' \
  -H 'Content-Type: application/json' \
  -d '{"model":"provider/model","input":["Hello","World"],"encoding_format":"float"}'
```

Use a versioned upstream base such as `https://provider.example/v1`; the connector appends `/embeddings` without inserting `/v1`. A successful batch counts as one request. Token-priced charity embeddings charge all input tokens at the input rate; vector dimensions are not output tokens. See the [embedding contract](docs/api-contract.md#23-post-v1embeddings) for limits, unknown-usage settlement, and Debug behavior.

Browser clients can call all three public model routes across origins with an explicit Bearer CallerKey. Use the default fetch credentials mode or `credentials: 'omit'`; do not use `credentials: 'include'`. For example, with a CallerKey supplied by the user at runtime:

```js
const response = await fetch('https://api.example.com/v1/embeddings', {
  method: 'POST',
  credentials: 'omit',
  headers: { Authorization: `Bearer ${callerKey}`, 'Content-Type': 'application/json' },
  body: JSON.stringify({ model: 'provider/model', input: 'Hello' }),
});
const result = await response.json();
if (!response.ok) throw new Error(result.error.message);
```

The browser's OPTIONS preflight needs no key and incurs no model call or charge. Authentication remains required for the actual request. Session and administrator APIs retain their same-origin protection. See [CORS rules and limits](docs/api-contract.md#browser-cross-origin-access); if a preflight fails, the browser does not send the model request and no call log is created.

The complete CallerKey is shown only once after creation or replacement. Save it immediately; if it was not saved, replace it to receive a new value.

Treat caller keys and upstream credentials as secrets. Do not put them in URLs, issue reports, notes, shell history, screenshots, or logs.

Errors contain stable `error.code`, `source`, and `message` fields. Platform messages start with `[NonbiriAPI]`; upstream messages do not. Common outcomes are:

| HTTP | Stable `error.code` | `source` | Meaning |
| --- | --- | --- | --- |
| 400 | `invalid_request`, `content_too_short` | `platform` | Invalid input or a charity request below the configured minimum. |
| 401 | `unauthorized` | `platform` | Missing or invalid authentication. |
| 403 | `forbidden`, `elevated_required`, `feature_disabled`, `insufficient_credits`, `charity_suspended`, `checkin_cap_reached` | `platform` | Permission, feature, balance, or account restriction. |
| 404 / 405 | `not_found` / `method_not_allowed` | `platform` | Unavailable resource, wrong station, or unsupported method. |
| 409 | `conflict`, `already_checked_in`, `debug_live_cancelled` | `platform` | A state conflict, repeated check-in, or canceled live debug call. |
| 413 | `payload_too_large` | `platform` | Request size exceeds the limit. |
| 422 | `resource_limit_exceeded`, `debug_dry_run_intercepted`, `debug_live_result_captured` | `platform` | A resource limit or an intentional Debug interception. |
| 423 | `resource_locked` | `platform` | The resource is temporarily protected. |
| 429 | `rate_limited` | `platform` | A rate or concurrency limit prevents admission. |
| 500 | `internal` | `platform` | An internal error. |
| 503 | `maintenance`, `service_unavailable`, `unbound_model` | `platform` | Maintenance, unavailable service, or no usable model binding. |
| Upstream 4xx / 5xx | `upstream` | `upstream` | Personal and charity calls preserve the upstream HTTP error status. |
| 502 / 504 | `upstream` | `upstream` | An upstream transport/protocol failure or timeout. |

Personal and charity calls return recognizable upstream error messages and an optional `upstream_code` after removing source addresses and sensitive values. Unreadable, oversized or unsafe errors use a generic message. Once SSE headers are sent, the HTTP status cannot change; failures use a bounded error event or close the connection. Charity billing follows validated upstream generation and reported usage, including generation buffered for a JSON caller who never receives it. Existing unknown-usage settlement rules still apply. Client disconnect cancels upstream immediately; heartbeat comments alone do not establish consumption. See the [full error and billing contract](docs/api-contract.md).

Scripts can use a CallerKey to find existing owned endpoints/models, import upstream keys into an owned endpoint and append connections to an owned personal model. These operations do not create targets, enable disabled resources, donate keys or make paid verification calls. Partial success remains committed; identical keys keep their original settings and default manual mode does not verify availability. Within 24 hours, reconcile or continue a lost response with the original idempotency key and complete body; after expiry read the resources first. See [personal automation](docs/api-contract.md#34-personal-callerkey-automation) for methods, fictional JSON/Bash examples, budgets and per-item results. All charity CallerKey reads/writes require L6; L5 retains personal automation and existing mainstream browser permissions.

## Development checks

```sh
scripts/check-go.sh
scripts/race-check.sh
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the contribution workflow and required checks.

## Data and legal pages

The application includes English and Chinese privacy and terms pages. Operators must review and customize them for the actual operator identity, contact channel, jurisdiction, deployment, and data-processing practices before accepting real users.

Requests can be sent to account-selected OpenAI-compatible, Anthropic-compatible or AI SDK Gateway v3 providers, including donor-provided charity resources, and to the dedicated image-activity provider. Those independent providers may process or retain content under their own policies; `store:false` cannot guarantee zero retention. NonbiriAPI does not actively log request bodies or successful response bodies. It does retain original upstream error bodies, which can include content or credentials echoed by the upstream, for restricted administrator/L6 diagnostics. Ordinary retention is 30 days, at most 1 MiB per error and 1 GiB total by default; explicit legal holds can extend retention. Source IP and approved client-header clues are also restricted to those roles. Debug remains bounded and memory-only. Image prompts, parameters and results use process memory only, with a ten-minute result pickup window. General, game, draft-paper and brush balances stay separate; draft paper and brushes cannot be exchanged back. Lake Notes coins have their separate optional exact exchange rules. `donation_credit` remains a cumulative statistic, not a spendable wallet.

When a user deletes their account, the service retains a minimal read-only record of known former IDs, times, effective level, restriction state, deletion source and balances before zeroing. Administrators and current level-6 stewards can review it within their permissions; missing historical fields stay unknown. An administrator-only deletion alert contains the economic snapshot, and a minimal record of an active duel interrupted by self-deletion expires after 90 days. A Discord ID blacklist survives deletion until an administrator removes its entry. Administrators and level-6 stewards can add entries within their respective authority; the first reason remains and later reasons form an append-only history. Removal does not automatically unban an account. Limited same-identity qualification and unexpired violation windows can carry over to a new account without restoring the deleted account or its private data. Management security records are excluded from personal exports.

Fat Fish uses general credits for formal unlocks/tickets and never charges administrator playtests. The editor/player share a continuous field and workbench, keyboard controls and visual contour/period-graph tools. Current draft playtesting saves and fixes the clicked version, prepares it, then waits for a manual start; local completion submits for server verification. Recover or explicitly abandon an old playtest before switching. New content uses engine v3; saved old versions/replays keep their engines. Normal updates do not clear content or switch nodes. Explicit legacy cleanup requires a stopped, source-bound operator procedure. Export schema 12 keeps safe Fat Fish summaries/progress, role settings, named presets, Lake Notes and safe retained automation outcomes under existing limits.

Self-deletion during an effective ban appends the ban reason captured at the start of deletion to the automatic blacklist note. Empty reasons are omitted, and an expired ban is not treated as active. The original blacklist reason and initiator remain unchanged when an entry already exists; administrators and level-6 stewards can read the additional note with its line breaks preserved.

See [docs/data-lifecycle-checklist.md](docs/data-lifecycle-checklist.md) for the data export, deletion, retention, and privacy invariants.

Unreleased changes retain pre-deletion request logs and necessary source facts until their original completion-plus-30-day deadline or applicable hold. Self, administrator and inactivity deletion do not shorten it. Only authorized administrators/L6 can read original account/Discord identity; a new account with the same Discord identity cannot access old logs. Multi-address/shared-address reviews use trusted API sources, exclude website sign-ins, last at most 24 hours and do not automatically punish users. Forced key-review requirements use long-lived irreversible matching material; deletion/resubmission does not clear them, and approval clears only explicitly enabled members.

Lake Notes saves cross-device/period profiles and one cast on the server, with a once-only general-credit entry per period. Closing, pause and maintenance retain progress and stop play and all exchanges. Four independently configured directions start disabled and use exact whole lots. Completed summaries last 30 days; current/paused progress follows the account lifecycle, and private reward randomness is excluded from export. Personal model role policy, preset names and safe automation outcomes retained within 24 hours belong in owner exports and deletion. Fat Fish v3 is the default for new content; legacy content cleanup is an explicit offline operation, never ordinary startup or an account/ledger reset.

## Security

Please read [SECURITY.md](SECURITY.md). Do not report an undisclosed vulnerability in a public issue. Repository security settings are documented in [docs/github-settings.md](docs/github-settings.md). NonbiriAPI is licensed under the [GNU Affero General Public License v3.0](LICENSE).

## License

Copyright © 2026 `waiting-here`. The project code is distributed under the GNU Affero General Public License v3.0; see [LICENSE](LICENSE), [NOTICE](NOTICE), and [web/THIRD_PARTY_NOTICES.md](web/THIRD_PARTY_NOTICES.md).

The bundled game-center illustrations are original project artwork created with assistance from ChatGPT and are distributed under the same AGPL-3.0 terms. Visual research included [DeepSeek Whale-chan](https://github.com/Neko3000/deepseek-whalechan) and [Every Token You Spend Comes Back as a Waifu](https://github.com/guihui2538/Every-token-you-spend-comes-back-as-a-waifu.); no source image from either reference project is embedded in this repository.
