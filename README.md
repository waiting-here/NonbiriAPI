# NonbiriAPI

[简体中文](README.zh-CN.md)

NonbiriAPI is a self-hosted AI API endpoint manager and OpenAI-compatible gateway. Each user manages their own endpoints, encrypted credentials, discovered models and routing, then calls personal or shared charity models with a revocable CallerKey.

The current development version is **1.0.0-rc.6**. See the [changelog](CHANGELOG.md) and [latest release](https://github.com/waiting-here/NonbiriAPI/releases/latest). All releases provide source code only. Linux/amd64 is the supported production target.

## Capabilities

- `/v1/models`, `/v1/chat/completions` and `/v1/embeddings` ingress, with OpenAI-compatible, Anthropic-compatible and AI SDK Gateway v3 connectors. Supported operations and protocol limits are defined in the [API contract](docs/api-contract.md).
- Personal model names, discovery, routing, bounded request adaptation and a memory-only Debug Hub. Charity resources add donated keys, budgets, credit accounting and scoped steward management.
- Discord sign-in, a separate administrator station, bilingual responsive pages and configurable branding. Users can export or delete their accounts.
- Optional check-ins, shared activities and nine games with server-authoritative settlement, recovery and privacy-aware rankings.
- A shared outbound security boundary, encrypted upstream secrets, bounded diagnostics and retention controls. Request logs and credit details have a 30-day ordinary retention period; compacted balance and audit summaries preserve accounting continuity.

## Build and run

Build requirements: **Go 1.26.6**, **Node.js ≥22.22.3**, **npm 12.0.1**, and Bash for repository scripts. Windows development uses Git Bash. The finished binary embeds both React stations and uses pure-Go SQLite; it needs no Node.js runtime.

```sh
npm --prefix web ci
npm --prefix web run build
CGO_ENABLED=0 go build -tags dist -trimpath -o nonbiriapi .
```

`-tags dist` embeds the built web applications. Without it, the binary serves development placeholder pages.

Follow [first-run setup](docs/first-run-setup.md) to prepare private paths, permissions, Discord OAuth, DNS and the reverse proxy. Copy [admin.env.example](admin.env.example) to a private path outside the checkout and replace its placeholders. Configure:

- Exactly one of `NONBIRI_MASTER_KEY_FILE` or `NONBIRI_MASTER_KEY`, holding a 32-byte encryption key.
- Administrator username/password and Discord client ID/secret.
- `NONBIRI_SITE_BASE_URL` and a distinct administrator hostname. `NONBIRI_ADMIN_HOST` defaults to `admin.<user-host>`.
- Database and key paths outside the checkout, with backups that preserve the matching key.

```sh
set -a
. /absolute/private/path/admin.env
set +a
./nonbiriapi
```

Fresh databases start in maintenance with public features closed. Review the instance's legal pages and settings before accepting users. See [configuration](docs/configuration.md) for startup variables and online controls, and [deployment](docs/deployment.md) for systemd, proxy, backup and restore procedures.

rc.6 supports fresh databases, the final rc.5 schema and registered rc.6 schemas. Older or unknown databases are rejected. The [compatibility section](docs/deployment.md#database-compatibility-and-version-changes) identifies the supported source and rollback requirements.

## Call the API

Create an endpoint, add an upstream key, discover or enter its models, and connect a model to a personal platform name. Create a CallerKey and save its complete value when it is shown.

```sh
curl https://api.example.com/v1/chat/completions \
  -H 'Authorization: Bearer nbk_REPLACE_WITH_YOUR_CALLER_KEY' \
  -H 'Content-Type: application/json' \
  -d '{"model":"provider/model","messages":[{"role":"user","content":"Hello"}]}'
```

Use `/v1/models` to list available names and `/v1/embeddings` for supported embedding models. Upstream base URLs include their API version, for example `https://provider.example/v1`. Browser clients use a Bearer CallerKey with `credentials: 'omit'`. Keep keys out of URLs and shared logs.

The [API contract](docs/api-contract.md) covers streaming, errors, billing, CORS, connector differences and personal automation. [Steward automation](docs/steward-automation.md) describes the administrator-provided integration guide.

## Development

The application runs as one process with one SQLite database. The user and administrator stations share the binary but use separate hosts and authorization boundaries.

| Area | Entry point |
| --- | --- |
| Startup and HTTP wiring | `main.go`, `internal/app/` |
| Database schema, initialization and verification | `internal/db/` |
| Accounting and retention | `internal/ledger/`, `internal/lifecycle/` |
| Upstream protocols and outbound policy | `internal/connector/`, `internal/egress/` |
| User and administrator interfaces | `web/` |
| Checks and deployment examples | `scripts/`, `deploy/` |

Run checks relevant to a change while developing; run the full gates for a final candidate:

```sh
scripts/check-go.sh
scripts/check-upgrade.sh
scripts/race-check.sh
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
```

The race gate needs a working C compiler. CI also covers real browsers, licenses, vulnerabilities and target builds. It does not deploy the application. See [CONTRIBUTING.md](CONTRIBUTING.md) for test scope and the protected-branch workflow.

## Further reading

| Topic | Guide |
| --- | --- |
| Gateway models and cache controls | [Gateway controls](docs/gateway-model-controls.md) |
| Games and verifiable randomness | [Duel games](docs/duel-games.md), [AI players](docs/ai-players.md), [Blackjack](docs/blackjack.md), [randomness](docs/game-randomness.md) |
| New game modules | [Steady Catch](docs/steady-catch.md), [AI Gwent](docs/ai-gwent.md), [Lake Notes](docs/lake-notes.md) |
| Picture-book activity | [Activity guide](docs/image-activity.md) |
| Exports, deletion and retained records | [Data lifecycle](docs/data-lifecycle-checklist.md) |
| Offline integrity and accounting audit | [Maintenance verification](docs/api-contract.md#10-maintenance-recovery-and-retention) |

Operators must adapt the bundled privacy and terms pages to their actual deployment. Independent upstream providers apply their own data policies; `store:false` does not guarantee zero retention. Restricted original-error diagnostics can contain content echoed by an upstream. The lifecycle guide describes access, retention and legal-hold exceptions.

Report vulnerabilities through [SECURITY.md](SECURITY.md). Project code is licensed under [AGPL-3.0](LICENSE); see [NOTICE](NOTICE), [frontend notices](web/THIRD_PARTY_NOTICES.md) and [audio credits](web/src/shared/assets/game-audio/NOTICE.md) for attribution.
