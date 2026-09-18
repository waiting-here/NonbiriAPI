# NonbiriAPI

[简体中文](README.zh-CN.md)

NonbiriAPI is a self-hosted API endpoint manager and OpenAI-compatible ingress gateway. It lets each user manage their own upstream endpoints and credentials, discover upstream models, define user-owned platform model names, and call those models through a single `CallerKey`.

> **Current source:** 1.0.0-rc.1 development candidate, not yet released. Latest published release: [1.0.0-beta.4](https://github.com/waiting-here/NonbiriAPI/releases/tag/v1.0.0-beta.4), a source prerelease for Linux/amd64. Review the deployment, privacy and security documentation before exposing an instance to users.
>
> **Compatibility boundary:** Generation 2 (`application_id=0x4E425249`, `user_version=2`), targeting Linux/amd64. Fifteen exact predecessor manifests, including complete beta.4 and the preceding release-candidate schema, support atomic upgrades to 117 tables, preserving existing data and configuration. The three new games start disabled. Alpha/Generation 1 still requires an explicit fresh cutover. See the [deployment guide](docs/deployment.md#database-compatibility-and-version-changes).
>
> Source repository: [github.com/waiting-here/NonbiriAPI](https://github.com/waiting-here/NonbiriAPI)

## Highlights

- Donors, administrators and stewards can set each donated key's failure threshold (default 10). Zero prevents error-triggered disablement and displays a persistent warning; saving keeps the count and immediately recalculates that state. Steward CallerKeys can read and edit the policy through the [automation API](docs/steward-automation.md).
- Gateway cost attribution is an administrator setting, off by default. Enabling it sends a server-generated user-and-origin pseudonym; Debug shows only whether it was sent. See the [Gateway compatibility matrix](docs/api-contract.md#24-native-ai-sdk-gateway-v3-compatibility), including the tested Runable embedding limitation.
- OpenAI-compatible `/v1/models`, `/v1/chat/completions`, and `/v1/embeddings` ingress. Chat supports OpenAI-compatible, Anthropic-compatible and native AI SDK Gateway v3 upstreams; embeddings support OpenAI-compatible and the strict Gateway text subset.
- Discord OAuth user sign-in and a separate administrator station.
- Per-user endpoints, mainstream channel templates, encrypted upstream credentials, automatic/manual model catalogs, platform model names, and a guided endpoint → key → model connection workflow.
- Ordered/random personal routing, ordered/uniform-random/expiry-weighted charity routing, opt-in pre-commit retry, user concurrency limits, and owner-configured per-key concurrency/RPM shared by personal, charity, and live diagnostic calls.
- SSRF, DNS-rebinding, redirect, proxy, response-size, timeout, cancellation, concurrency, and streaming safeguards.
- Encrypted-at-rest upstream secrets; plaintext credentials are not returned in lists, logs, alerts, or account exports.
- Request metadata, usage accounting, retention cleanup, account export/deletion, issues, alerts, and runtime limits.
- Account export schema 8 includes game-randomness proofs, both wallets, asset-tagged entries, independent check-ins, game payment sources and lifetime newcomer completions while excluding secrets, other users and internal capacity, scheduling and audit data.
- Separate general and game wallets with independent check-ins. Games spend game credits first, then general credits; eligible refunds return their original assets. API calls and Thursday contributions use general credits. Daily welfare pays game credits, and nine once-only newcomer tasks award 17,000 general credits in total.
- Shared user-limit, effective-level filtering and announcement management for administrators and L5 stewards. Stewards can modify only other current L1–L4 users, cannot delete accounts or change cumulative donor credit, and can reset donated-key failure streaks in bounded batches across a complete selected result set. Donors can reset their own eligible keys.
- Fishing defaults to 100% gross RTP on fresh databases, preserving existing settings on upgrade. Each catch contributes separately rounded platform, welfare and Thursday cuts, defaulting to 1% each, with gross and net rewards displayed.
- Credits, check-in with a server-configured balance gate, personal credit history, donation-backed charity routing, per-key donation expiry and usage limits, and level-5 co-management. Authorized administrator and steward logs expose a fixed safe set of upstream resource details, including the routed key identifier and logical-request charge; ordinary charity callers do not receive those details.
- Donated keys can combine recurring call, Token and credit limits with their total limits. Administrators and level-5 stewards configure reset or sliding windows of 1 hour, 5 hours, a day, a week or a month with a saved time zone; donors can inspect their own rules, usage, reservations and remaining capacity. These counters include only charity calls. Sharing the same key with personal calls may consume more upstream capacity than the charity counters show. For Token-priced charity models, authorized managers can set an optional per-model credit reserve before a call; leaving it blank inherits the global setting, and per-request pricing keeps its existing per-request reserve.
- User and administrator resource lists have bounded server-side pagination with 10/20/50/100 page sizes, direct page navigation, filter and page restoration after returning or refreshing, and an independent browser-local page-size preference for each list.
- The charity model catalog provides plain-text descriptions, allowed-level sets, explicit availability reasons, and source/key browsing for authorized managers. The catalog can show configured models even when the current caller cannot use them; the public API remains limited to currently usable models.
- General time-point forms parse and display saved instants in the browser's time zone, with server-resolved daylight-saving gaps and repeated times. Recurring quota rules retain their selected business time zone separately from ordinary timestamp display.
- Daily welfare, the Thursday pooled activity, bilingual announcements, and public credential-theft reporting with administrator review. Creating a Thursday period automatically selects the next Thursday at 00:00 Beijing time for 24 hours, including the following week when created on a Thursday. The administrator page displays this Beijing-time window; editing an existing period preserves its schedule.
- Experimental OpenAI-only chat policies for per-key `store:false` enforcement and per-model tool-call flattening, both disabled by default and explicitly risk-labelled.
- A memory-only Debug Hub that starts in dry-run mode and requires explicit confirmation to send requests upstream. Live results are captured in the Debug page; the API caller receives a dedicated HTTP 422 debug response.
- LinkLink shares 2/3/5 hint or refresh opportunities per new board, adds 100 score points per unused opportunity on completion, and provides six per-user-best boards by size and 7/30-day window. Equal scores rank by earliest achievement. Ordinary matches avoid extra wallet/game-center reloads, and connection animations allow the next selection. Saved old games keep their original rules.
- A server-authoritative game center with Pond Fishing, LinkLink, three-player Rock Paper Scissors, Bidding Duel and Likes Battle, including idempotent accounting, recovery, privacy-aware leaderboards, and bundled local artwork. Fishing opens on the rolling 30-day largest-length board, with the lifetime largest-length and rolling 30-day payout boards still available. Its transparent-background white-rice-themed blue fat fish Easter egg preserves the original legendary species and payout; length-board rows use a compact original species name while result details retain the original-species explanation.
- Server-generated upstream safety pseudonyms scoped to one user and one canonical upstream origin; see the [API contract](docs/api-contract.md#22-post-v1chatcompletions) for their rotation and privacy boundary.
- Redesigned bilingual React user/admin stations with responsive navigation, continuous resource workflows, safe Markdown guidance, and configurable site branding, embedded into a single Go binary.

The current source exposes the three OpenAI-compatible ingress routes listed above. OpenAI-compatible embeddings support text and Token ID inputs, single items and batches, float/base64 encoding, and optional output dimensions for personal and charity models. Models have no purpose classification: the request path selects the operation, and the upstream decides whether its model supports it. Rerank remains unsupported. An `anthropic-compatible` endpoint is translated behind that ingress; NonbiriAPI does not expose an Anthropic-native public endpoint. The `ai-sdk-gateway-v3` connector uses native Gateway routes; its strict subset includes text, tools, image input and text embeddings. Other OpenAI API families and connector types remain deferred. See the [API contract](docs/api-contract.md) for the strict Anthropic subset and token-limit rules.

Bidding Duel offers 13 simultaneous hidden-card rounds. Likes Battle combines five characters, eight harnesses and full skill/buff rules with a shared battery, synchronized five-second settlement and dynamic resource feedback. Uncapped API reserve and gold use numeric counters. Dedicated character, skill and harness illustrations are bundled locally. Both games support original-asset refunds, safe 30-day player history and administrator-only anonymous archives and resumable exports. See [the bilingual game guide](docs/duel-games.md).

The six games have dedicated covers; Fishing includes illustrated catches with an SVG fallback. Bidding Duel, Likes Battle and Blackjack include short sound effects. Likes Battle also has synchronized scene music. Sound and music start off and remember each game's choice in the browser. Account → Local preferences offers lightweight or lossless music, applied on the next music activation or game entry. Media sources and formats are documented in the [audio notice](web/src/shared/assets/game-audio/NOTICE.md).

Blackjack shares one eight-seat table with a persistent waiting queue and a 15/30/15-second minute cadence. Six-deck rules include splitting and doubling; each hand pays all net returns in general credits after frozen fees. The game starts disabled. See [Blackjack rules](docs/blackjack.md).

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

The database remains Generation 2: SQLite `application_id=0x4E425249` and `user_version=2`. Fresh creation requires the main/WAL/SHM set to be absent. Fifteen exact predecessor manifests, including complete beta.4 and the preceding release-candidate schema, receive an atomic upgrade to 117 tables. Missing predecessor extensions and the duel schema are applied before the complete manifest, foreign keys, both asset ledgers and reward capacity are validated. Unknown or partial structures are rejected before source writes; a second startup adds nothing. Existing identities, both wallets, settled charges, game rules, configuration and custom legal text remain intact. Game wallets are initialized to zero only when upgrading a source that did not have them. Existing Fishing, LinkLink and RPS games retain their saved version-1 or version-2 rules. The three new games use independent rules version 1 and start disabled. No historical payment source or newcomer completion is invented. Older binaries reject the new manifest; rollback requires the complete matching stopped snapshot. Alpha/Generation 1 and arbitrary schema repair remain unsupported. Fresh databases still start with maintenance enabled and registration, activities, charity, donation intake and games disabled.

The project is source-first and supports Linux/amd64 as its production target. Operators compile the exact release source commit on that target or use an equivalent controlled build pipeline. This source release provides no official precompiled binaries, container images, or installers; other production platforms are not supported.

## GitHub automation

The repository includes a read-only CI workflow. GitHub Actions runs the complete Go and frontend checks for pull requests, with manual runs available when needed. Protected `master` changes go through those checks; after merging, verify that its tree matches the tested PR tree and reuse the result. CodeQL retains its own triggers. CI does not deploy the application. Release artifact automation is intentionally separate and will be added only after the supported targets and signing policy are decided.

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

Personal and charity calls return recognizable upstream error messages and an optional `upstream_code` after removing source addresses and sensitive values. Unreadable, oversized or unsafe errors use a generic message. Once SSE headers are sent, the HTTP status cannot change; failures use a bounded error event or close the connection. A failed charity attempt with no validated successful output costs no credits or donation quota; interruption after successful output starts follows the documented usage and settlement rules. See the [full error and billing contract](docs/api-contract.md).

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

Requests can be sent to account-selected OpenAI-compatible, Anthropic-compatible or AI SDK Gateway v3 providers, including donor-provided charity resources. Those independent providers may process or retain content under their own policies; the experimental `store:false` option is a best-effort request and cannot guarantee zero retention. NonbiriAPI itself keeps ordinary request/response content out of persistent logs; Debug capture is redacted, bounded, and memory-only. General and game credits have separate signed balances and asset-tagged histories. `donation_credit` remains a cumulative donor-reward statistic that ordinary spending never reduces.

See [docs/data-lifecycle-checklist.md](docs/data-lifecycle-checklist.md) for the data export, deletion, retention, and privacy invariants.

## Security

Please read [SECURITY.md](SECURITY.md). Do not report an undisclosed vulnerability in a public issue. Repository security settings are documented in [docs/github-settings.md](docs/github-settings.md). NonbiriAPI is licensed under the [GNU Affero General Public License v3.0](LICENSE).

## License

Copyright © 2026 `waiting-here`. The project code is distributed under the GNU Affero General Public License v3.0; see [LICENSE](LICENSE), [NOTICE](NOTICE), and [web/THIRD_PARTY_NOTICES.md](web/THIRD_PARTY_NOTICES.md).

The bundled game-center illustrations are original project artwork created with assistance from ChatGPT and are distributed under the same AGPL-3.0 terms. Visual research included [DeepSeek Whale-chan](https://github.com/Neko3000/deepseek-whalechan) and [Every Token You Spend Comes Back as a Waifu](https://github.com/guihui2538/Every-token-you-spend-comes-back-as-a-waifu.); no source image from either reference project is embedded in this repository.
