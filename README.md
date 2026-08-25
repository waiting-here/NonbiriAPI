# NonbiriAPI

[简体中文](README.zh-CN.md)

NonbiriAPI is a self-hosted API endpoint manager and OpenAI-compatible ingress gateway. It lets each user manage their own upstream endpoints and credentials, discover upstream models, define user-owned platform model names, and call those models through a single `CallerKey`.

> **Latest published release:** `v1.0.0-alpha.2`. The current development target is the unreleased `v1.0.0-alpha.3`; this branch and its documentation are not a release or an upgrade authorization. Alpha builds are intended only for controlled self-hosted trials. Review the deployment, backup, privacy, and security documentation before exposing an instance to users.
>
> Source repository: [github.com/waiting-here/NonbiriAPI](https://github.com/waiting-here/NonbiriAPI)

## Highlights

- OpenAI-compatible `/v1/models` and `/v1/chat/completions` ingress, with OpenAI-compatible and Anthropic-compatible upstream connectors.
- Discord OAuth user sign-in and a separate administrator station.
- Per-user endpoints, encrypted upstream credentials, model discovery, platform model names, bindings, ordered/random routing, opt-in pre-commit retry, and independent per-user in-flight concurrency limits.
- SSRF, DNS-rebinding, redirect, proxy, response-size, timeout, cancellation, concurrency, and streaming safeguards.
- Encrypted-at-rest upstream secrets; plaintext credentials are not returned in lists, logs, alerts, or account exports.
- Request metadata, usage accounting, retention cleanup, account export/deletion, issues, alerts, and runtime limits.
- Credits, check-in, donation-backed charity routing, and a live-authorized level-5 steward view whose site-wide logs hide donated resources.
- Experimental OpenAI-only per-key `store:false` enforcement and per-model tool-call flattening, both disabled by default and explicitly risk-labelled.
- A memory-only Debug Hub that starts in dry-run mode and requires a fresh challenge plus confirmation before observing a real upstream call.
- A server-authoritative game framework whose first game is Pond Fishing, with idempotent accounting, recovery, privacy-aware leaderboards, and local SVG/CSS artwork.
- Server-generated upstream safety pseudonyms scoped to one user and one canonical upstream origin; see the [API contract](docs/api-contract.md#21-post-v1chatcompletions) for their rotation and privacy boundary.
- React user/admin stations embedded into a single Go binary.

The unreleased alpha.3 contract still exposes only the two OpenAI-compatible ingress routes listed above. An `anthropic-compatible` endpoint is translated behind that ingress; NonbiriAPI does not expose an Anthropic-native public endpoint. Other OpenAI API families and connector types remain deferred. See the [API contract](docs/api-contract.md) for the strict Anthropic subset and token-limit rules.

## Architecture

One binary serves two host-isolated stations:

- **User station:** user self-service APIs, `/v1/*`, and the user web application.
- **Admin station:** administrator APIs and the administrator web application.

Configure a user origin and a distinct administrator host. `NONBIRI_ADMIN_HOST` may be omitted when the derived `admin.<user-host>` name is correct; never expose both stations on the same hostname.

## Build from source

Build-time requirements:

- Go 1.26.x.
- Node.js 22.22.2 or newer and npm 12.0.1 for the web build.

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

Alpha.3 deliberately uses a fresh-only database generation. It does not migrate an alpha.1/alpha.2 database in place and refuses an old, empty, unknown, malformed, or unexpected database without writing to it. A normal binary-only downgrade is also unsafe. Stop the service, preserve a verified complete snapshot (database/sidecars, release, configuration, master key and unit), and follow the [deployment guide](docs/deployment.md). Starting alpha.3 from an existing deployment requires an explicit fresh cutover; the new database starts with maintenance on and registration and games off.

The current alpha release strategy is source-first: a prebuilt binary is not required. Operators may compile the source on the target platform or use their own build pipeline. A published binary, if added later, is a convenience artifact rather than the compatibility boundary.

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
