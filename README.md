# NonbiriAPI

[简体中文](README.zh-CN.md)

NonbiriAPI is a self-hosted AI endpoint manager and OpenAI-compatible gateway. Users manage endpoints, encrypted credentials, model discovery and routing, then use revocable CallerKeys for personal or shared charity calls. Optional activities and games share server-authoritative accounting and lifecycle rules.

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

| Area | Source and contract |
| --- | --- |
| Startup, recovery and HTTP composition | `main.go`, `internal/app/` |
| Stable schema and migrations | `internal/db/`, [database upgrades](docs/database-upgrades.md) |
| One-time database conversion | `cmd/nonbiri-upgrade-v1/` |
| Accounting, export, deletion and retention | `internal/ledger/`, `internal/lifecycle/`, [lifecycle](docs/data-lifecycle-checklist.md) |
| Connectors, forwarding and outbound policy | `internal/connector/`, `internal/forward/`, `internal/egress/` |
| Shared components and separate stations | `web/src/shared/`, `web/src/user/`, `web/src/admin/` |
| Verification and deployment examples | `scripts/`, `.github/workflows/`, `deploy/` |

The connector registry supports OpenAI-compatible, Anthropic-compatible, AI SDK Gateway v3, Google Gemini, OpenAI Responses and OpenAI Decisions upstreams. Public ingress includes `/v1/models`, `/v1/chat/completions`, `/v1/embeddings`, `/v1/images/generations` and `/v1/decisions`. Gemini and Responses translate Chat requests; Decisions uses its native endpoint. Operation support is connector-specific. The backend enforces ownership, credential protection, SSRF, accounting and cancellation. Wire formats and limits are in the [API contract](docs/api-contract.md).

Anonymous minute metrics separate pool acquisition from transaction work for
game checkpoints, request accounting and background maintenance. In-flight
elapsed time remains visible across reporting windows; see the runtime metrics
guide before comparing completed latency with current occupancy.

The optional Gacha Simulator includes layered SP artwork. Amber Vow keeps its
character beside desktop content, with matching static fallback and enhanced
artwork; composition and source details are in the [art guide](docs/gacha-art.md).

## Build and develop

Use **Go 1.26.9**, **Node.js ≥22.22.3**, **npm 12.0.1** and Bash; Windows uses Git Bash. The finished binary needs neither Node.js nor a C runtime.

```sh
npm --prefix web ci
npm --prefix web run build
CGO_ENABLED=0 go build -buildvcs=true -tags dist -trimpath -o nonbiriapi .
```

`-tags dist` embeds both built stations; without it, the binary serves development placeholders. [CONTRIBUTING](CONTRIBUTING.md) is the canonical entry for local checks, browser setup, shared fixtures, CI selection and protected-branch changes. Update implementation and affected tests together.

## Configure and deploy

[First-run setup](docs/first-run-setup.md) covers private paths, the encryption key, Discord OAuth and public origins. Copy [admin.env.example](admin.env.example) outside the checkout, replace placeholders and load it before starting. Preserve the database with its matching key.

Fresh databases start in maintenance with public features closed. The v1.0.2 binary accepts schema 21 or fresh storage. Existing schema 16, 17, 18, 19 and 20 databases use the separate offline converter; historical conversion code is excluded from the service. Later v1.x releases retain an upgrade path from every earlier stable v1.x schema. Older binaries need not read newer databases.

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
