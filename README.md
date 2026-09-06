# NonbiriAPI

[简体中文](README.zh-CN.md)

NonbiriAPI is a self-hosted API endpoint manager and OpenAI-compatible ingress gateway. It lets each user manage their own upstream endpoints and credentials, discover upstream models, define user-owned platform model names, and call those models through a single `CallerKey`.

> **Source version:** `v1.0.0-beta.1`. Review the deployment, backup, privacy, and security documentation before exposing an instance to users.
>
> **Compatibility boundary:** beta.1 uses database Generation 2 and targets source builds for Linux/amd64. Alpha deployments require a fresh database; validated current and explicitly supported earlier beta.1 schemas preserve existing data during a normal update.
>
> Source repository: [github.com/waiting-here/NonbiriAPI](https://github.com/waiting-here/NonbiriAPI)

## Highlights

- OpenAI-compatible `/v1/models` and `/v1/chat/completions` ingress, with OpenAI-compatible and Anthropic-compatible upstream connectors.
- Discord OAuth user sign-in and a separate administrator station.
- Per-user endpoints, mainstream channel templates, encrypted upstream credentials, automatic/manual model catalogs, platform model names, and a guided endpoint → key → model connection workflow.
- Ordered/random personal routing, ordered/uniform-random/expiry-weighted charity routing, opt-in pre-commit retry, user concurrency limits, and owner-configured per-key concurrency/RPM shared by personal, charity, and live diagnostic calls.
- SSRF, DNS-rebinding, redirect, proxy, response-size, timeout, cancellation, concurrency, and streaming safeguards.
- Encrypted-at-rest upstream secrets; plaintext credentials are not returned in lists, logs, alerts, or account exports.
- Request metadata, usage accounting, retention cleanup, account export/deletion, issues, alerts, and runtime limits.
- Credits, check-in, personal credit history, donation-backed charity routing, per-key donation expiry and usage limits, and level-5 co-management. Authorized administrator and steward logs expose a fixed safe set of upstream resource details; ordinary charity callers do not receive those details.
- Daily welfare, the Thursday pooled activity, bilingual announcements, and public credential-theft reporting with administrator review.
- Experimental OpenAI-only per-key `store:false` enforcement and per-model tool-call flattening, both disabled by default and explicitly risk-labelled.
- A memory-only Debug Hub that starts in dry-run mode and requires explicit confirmation to send requests upstream. Live results are captured in the Debug page; the API caller receives a dedicated HTTP 422 debug response.
- A server-authoritative game center with Pond Fishing, LinkLink, and three-player Rock Paper Scissors, including idempotent accounting, recovery, privacy-aware leaderboards, and bundled local artwork.
- Server-generated upstream safety pseudonyms scoped to one user and one canonical upstream origin; see the [API contract](docs/api-contract.md#22-post-v1chatcompletions) for their rotation and privacy boundary.
- Redesigned bilingual React user/admin stations with responsive navigation, continuous resource workflows, safe Markdown guidance, and configurable site branding, embedded into a single Go binary.

Beta.1 exposes only the two OpenAI-compatible ingress routes listed above. An `anthropic-compatible` endpoint is translated behind that ingress; NonbiriAPI does not expose an Anthropic-native public endpoint. Other OpenAI API families and connector types remain deferred. See the [API contract](docs/api-contract.md) for the strict Anthropic subset and token-limit rules.

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
- [Example environment file](admin.env.example)
- [Example systemd unit](deploy/nonbiriapi.service.example)

Beta.1 uses database Generation 2 (`user_version=2`). It does not migrate an alpha database or Generation 1 in place and refuses unsupported or malformed existing databases without writing to them. Three exact earlier beta.1 schemas can be updated normally: startup atomically adds the missing charity-routing, per-key limit, and successful-response checkpoint tables and indexes. Existing accounts, resources, balances, configuration, routing choices, and key limits are preserved. A binary-only downgrade to an incompatible schema is unsafe. Stop the service, preserve a verified complete snapshot (database/sidecars, release, configuration, master key and unit), and follow the [deployment guide](docs/deployment.md). Starting beta.1 from an alpha deployment requires an explicit fresh cutover; the new database starts with maintenance on and registration, activities, charity, donation intake, and games off.

Beta.1 is source-first and supports Linux/amd64 as its production target. Operators compile the exact tagged source on that target or use an equivalent controlled build pipeline. This prerelease provides no official precompiled binaries, container images, or installers; other production platforms are not supported.

## GitHub automation

The repository includes a read-only CI workflow. GitHub Actions runs the Go and frontend checks on pushes to `master` and pull requests; it does not deploy the application. Release artifact automation is intentionally separate and will be added only after the supported targets and signing policy are decided.

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
| Upstream 4xx | `upstream` | `upstream` | Personal calls may preserve the upstream HTTP status. |
| 502 / 504 | `upstream` | `upstream` | An upstream failure or an owner-visible upstream timeout. |

After a charity call is dispatched, upstream failures use a generic `502 upstream` response without provider details. Once SSE headers are sent, the HTTP status cannot change; failures use a bounded error event or close the connection. A failed charity attempt with no validated successful output costs no credits or donation quota; interruption after successful output starts follows the documented usage and settlement rules. See the [full error and billing contract](docs/api-contract.md).

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

Requests can be sent to account-selected OpenAI-compatible or Anthropic-compatible providers, including donor-provided charity resources. Those independent providers may process or retain content under their own policies; the experimental `store:false` option is a best-effort request and cannot guarantee zero retention. NonbiriAPI itself keeps ordinary request/response content out of persistent logs; Debug capture is redacted, bounded, and memory-only. Spendable `credits` form one balance shared by check-in/donor/game rewards and supported consumption, while `donation_credit` remains a cumulative donor-reward statistic that ordinary spending never reduces.

See [docs/data-lifecycle-checklist.md](docs/data-lifecycle-checklist.md) for the data export, deletion, retention, and privacy invariants.

## Security

Please read [SECURITY.md](SECURITY.md). Do not report an undisclosed vulnerability in a public issue. Repository security settings are documented in [docs/github-settings.md](docs/github-settings.md). NonbiriAPI is licensed under the [GNU Affero General Public License v3.0](LICENSE).

## License

Copyright © 2026 `waiting-here`. The project code is distributed under the GNU Affero General Public License v3.0; see [LICENSE](LICENSE), [NOTICE](NOTICE), and [web/THIRD_PARTY_NOTICES.md](web/THIRD_PARTY_NOTICES.md).

The bundled game-center illustrations are original project artwork created with assistance from ChatGPT and are distributed under the same AGPL-3.0 terms. Visual research included [DeepSeek Whale-chan](https://github.com/Neko3000/deepseek-whalechan) and [Every Token You Spend Comes Back as a Waifu](https://github.com/guihui2538/Every-token-you-spend-comes-back-as-a-waifu.); no source image from either reference project is embedded in this repository.
