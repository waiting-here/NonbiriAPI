# NonbiriAPI

NonbiriAPI is a self-hosted API endpoint manager and OpenAI-compatible gateway. It lets each user manage their own upstream endpoints and credentials, discover upstream models, define platform model aliases, and call those aliases through a single `CallerKey`.

> **Release status:** `v1.0.0-alpha.1`. This release is suitable for controlled self-hosted trials. Review the deployment, backup, privacy, and security documentation before exposing an instance to users.
>
> Source repository: [github.com/waiting-here/NonbiriAPI](https://github.com/waiting-here/NonbiriAPI)

## Highlights

- OpenAI-compatible `/v1/models` and `/v1/chat/completions` endpoints.
- Discord OAuth user sign-in and a separate administrator station.
- Per-user endpoints, encrypted upstream credentials, model discovery, aliases, bindings, ordered/random routing, and opt-in pre-commit retry.
- SSRF, DNS-rebinding, redirect, proxy, response-size, timeout, cancellation, concurrency, and streaming safeguards.
- Encrypted-at-rest upstream secrets; plaintext credentials are not returned in lists, logs, alerts, or account exports.
- Request metadata, usage accounting, retention cleanup, account export/deletion, issues, alerts, and runtime limits.
- React user/admin stations embedded into a single Go binary.

The alpha intentionally supports only the `openai-compatible` connector and the two OpenAI-compatible exit routes listed above. Other OpenAI API families and other connector types are deferred.

## Architecture

One binary serves two host-isolated stations:

- **User station:** user self-service APIs, `/v1/*`, and the user web application.
- **Admin station:** administrator APIs and the administrator web application.

Configure both origins explicitly. Do not expose the admin host on the same public hostname as the user station.

## Quick start from source

Build-time requirements:

- Go 1.26 or newer in the supported 1.26 series.
- Node.js 22.12 or newer and npm 12 for the web build.

Node.js is not needed to run the finished binary.

```sh
cp admin.env.example admin.env
chmod 600 admin.env

# Create a key outside the repository and make it readable only by the service user.
openssl rand -hex 32 > master.key
chmod 600 master.key
# Set NONBIRI_MASTER_KEY_FILE in admin.env to the absolute path of master.key.

npm --prefix web ci
npm --prefix web run build
CGO_ENABLED=0 go build -tags dist -o nonbiriapi .

set -a
. ./admin.env
set +a
./nonbiriapi
```

The untagged Go build embeds a development placeholder UI. A usable release binary must be built after `npm run build` with `-tags dist`.

## Configuration

`admin.env.example` documents the complete startup environment. The [configuration reference](docs/configuration.md) explains startup variables, administrator runtime settings, and private Discord trial gates. The required values include:

- `NONBIRI_MASTER_KEY_FILE` or `NONBIRI_MASTER_KEY` (exactly one; a 32-byte key).
- `NONBIRI_ADMIN_USERNAME` and `NONBIRI_ADMIN_PASSWORD`.
- `NONBIRI_DISCORD_CLIENT_ID` and `NONBIRI_DISCORD_CLIENT_SECRET`.
- `NONBIRI_SITE_BASE_URL` and the separate `NONBIRI_ADMIN_HOST`.

Keep `admin.env`, the master-key file, and the database outside the Git working tree. Never commit real credentials or a real database.

## VPS/systemd deployment

The intended first deployment model is a manually updated systemd service. See:

- [Deployment and systemd guide](docs/deployment.md)
- [Example environment file](admin.env.example)
- [Example systemd unit](deploy/nonbiriapi.service.example)

The update procedure is deliberately conservative: stop the service, back up the database and sidecars, build and verify a release binary, install it atomically, restart, and verify both configured hosts. The alpha has no versioned migration framework; keep a tested backup before every update.

Alpha.1 is source-first: a prebuilt binary is not required for the release. Operators may compile the source on the target platform or use their own build pipeline. A published binary, if added later, is a convenience artifact rather than the compatibility boundary.

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

See [docs/data-lifecycle-checklist.md](docs/data-lifecycle-checklist.md) for the data export, deletion, retention, and privacy invariants.

## Security

Please read [SECURITY.md](SECURITY.md). Do not report an undisclosed vulnerability in a public issue. Repository security settings are documented in [docs/github-settings.md](docs/github-settings.md). NonbiriAPI is licensed under the [GNU Affero General Public License v3.0](LICENSE).

## License

Copyright © 2026 `waiting-here`. The project code is distributed under the GNU Affero General Public License v3.0; see [LICENSE](LICENSE), [NOTICE](NOTICE), and [web/THIRD_PARTY_NOTICES.md](web/THIRD_PARTY_NOTICES.md).
