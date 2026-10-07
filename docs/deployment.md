# VPS deployment with systemd

This guide covers one Linux/amd64 application instance with a dedicated service user, local SQLite and a TLS reverse proxy. Check the [supported upgrade sources](#database-compatibility-and-version-changes) before updating. See [configuration.md](configuration.md) for settings and [image-activity.md](image-activity.md) for activity operations.

The commands are examples. Replace paths, hostnames, users, and package-manager commands for the target VPS. Do not copy real secrets into a Git checkout.

## Recommended layout

```text
/etc/nonbiriapi/admin.env       # 0640, root:nonbiriapi (secret-bearing; dedicated group only)
/etc/nonbiriapi/master.key      # 0600, nonbiriapi:nonbiriapi
/opt/nonbiriapi/releases/<ver>/nonbiriapi
/opt/nonbiriapi/current          # symlink to the active release directory
/var/lib/nonbiriapi/nonbiriapi.db
/var/backups/nonbiriapi/
```

Use a dedicated system user such as `nonbiriapi`. The service only needs to read its environment/key files and write its database directory.

## Prepare the host

For the ordered walkthrough of preparing the environment file, master key, Discord application, DNS, and trusted-proxy list before the first boot, see [first-run-setup.md](first-run-setup.md); the full variable reference is in [configuration.md](configuration.md). The steps below are the host-level mechanics.

Create the service and directories using the distribution's normal administration tools. For example:

```sh
sudo useradd --system --user-group --home /var/lib/nonbiriapi --shell /usr/sbin/nologin nonbiriapi
sudo install -d -o nonbiriapi -g nonbiriapi -m 0700 /var/lib/nonbiriapi
sudo install -d -o root -g nonbiriapi -m 0750 /etc/nonbiriapi
sudo install -d -o root -g root -m 0755 /opt/nonbiriapi/releases
sudo install -d -o root -g root -m 0750 /var/backups/nonbiriapi
```

Install the real `admin.env` from [admin.env.example](../admin.env.example), then:

```sh
sudo chown root:nonbiriapi /etc/nonbiriapi/admin.env
sudo chmod 0640 /etc/nonbiriapi/admin.env
```

The environment file must set `NONBIRI_DB_PATH=/var/lib/nonbiriapi/nonbiriapi.db` and `NONBIRI_MASTER_KEY_FILE=/etc/nonbiriapi/master.key`. It contains the administrator password and Discord client secret, so `nonbiriapi` must be a dedicated group with no unrelated members. Keep `NONBIRI_LISTEN_ADDR` on loopback when a reverse proxy is in front.

Generate the master key once, before the first start. Do not regenerate it for an existing database: a new key makes encrypted upstream credentials unreadable.

```sh
sudo install -o nonbiriapi -g nonbiriapi -m 0600 /dev/null /etc/nonbiriapi/master.key
openssl rand -hex 32 | sudo -u nonbiriapi tee /etc/nonbiriapi/master.key >/dev/null
sudo chmod 0600 /etc/nonbiriapi/master.key
```

Run those key-generation commands only on a new installation: the first command truncates its target. Replace the example key only before the first database is created. If a real database already exists, preserve its original key. The loader deliberately rejects group-readable modes such as `0640`; the service account must own the file. Unix mode `0400` or `0600` is accepted (`0600` is used by the generation commands).

For a manual launch outside the example systemd unit, the application enforces owner-only access to the database directory and files. It resolves the database directory to an absolute path and verifies that the current account owns it with no group/other access. Existing database/WAL/SHM paths must already be owner-owned regular non-symlink files with mode `0600`; a permissive, wrong-owner, substituted, or non-regular source is rejected without chmod, creation, removal, or SQLite recovery. Fresh files are created as `0600`, and later SQLite sidecars inherit that owner-only mode. Setting `umask 077` remains useful defense in depth but is not the sole protection.

## Build a release binary

Build on a controlled Linux/amd64 host or in a clean checkout that produces that exact target. The race gate requires a working C compiler for Go's race detector; the final production build remains `CGO_ENABLED=0`.

```sh
npm --prefix web ci
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
scripts/check-go.sh
scripts/race-check.sh
CGO_ENABLED=0 go build -tags dist -trimpath -o nonbiriapi .
```

The `dist` build tag is required for the real frontend. An untagged binary contains development placeholder pages. Verify the output before installation with `go version -m ./nonbiriapi` and a temporary configuration/database.

Install into a versioned directory and publish the symlink with a same-filesystem rename. Set `version` to the release being installed:

```sh
version=1.0.0-rc.3
release=/opt/nonbiriapi/releases/$version
sudo install -d -o root -g root -m 0755 "$release"
sudo install -o root -g root -m 0755 nonbiriapi "$release/nonbiriapi"
sudo ln -sfn "$release" /opt/nonbiriapi/current.next
sudo mv -Tf /opt/nonbiriapi/current.next /opt/nonbiriapi/current
```

On the intended Ubuntu target, `mv -T` replaces the symlink itself rather than following it; creating the temporary link and renaming it avoids an unlink/create gap.

Keep at least one previous release directory until the new release has passed its health and functional checks.

## Example systemd unit

Copy [deploy/nonbiriapi.service.example](../deploy/nonbiriapi.service.example) to `/etc/systemd/system/nonbiriapi.service`, review every path and hardening option, then run:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now nonbiriapi.service
sudo systemctl status nonbiriapi.service
sudo journalctl -u nonbiriapi.service -n 100 --no-pager
```

The service should start only after the environment file, master key, release binary, and database directory are readable/writable by the service account.

`NONBIRI_STARTUP_TIMEOUT_SECONDS` defaults to `300` and accepts whole integer seconds from `30` through `1800`; it is one total budget for configuration, validation, database recovery, application initialization, and listener binding. `NONBIRI_SHUTDOWN_TIMEOUT_SECONDS` defaults to `30` and accepts whole integer seconds from `5` through `120`; it is one total graceful-shutdown budget, and expiration causes a nonzero process exit. See [configuration.md](configuration.md#startup-environment) for the full environment reference. Opening the database alone does not mean the application is ready.

The anonymous `GET /readyz` returns HTTP 200 with `{"status":"ready"}` only when fully initialized and the listener is bound and serving. During shutdown or after a critical worker fails, it returns HTTP 503 with `{"status":"not_ready"}`. Before initialization and listener binding, requests may be unreachable. The existing `/healthz` response remains unchanged.

## Reverse proxy requirements

Configure the public user host and the separate admin host in DNS and TLS. The proxy should:

- forward the original Host and the correct HTTPS scheme;
- be the only source included in `NONBIRI_TRUSTED_PROXY_CIDRS`;
- preserve long-lived SSE responses without imposing a shorter buffering or idle timeout than the application contract;
- apply both per-client and global rate limits to the unauthenticated
  `/api/auth/discord/start` route and to the session-gated
  `/api/auth/elevate` route; OAuth states expire after ten minutes and the shared in-process pending-state store holds at most 4096 live entries. Test chosen limits without weakening callback availability.
  The application additionally enforces an in-process per-client-IP admission
  throttle on both routes as a second layer (configurable at runtime via the
  `oauth_start_rate_limit` / `oauth_start_rate_window_seconds` /
  `oauth_start_rate_penalty_seconds` site_config keys; setting
  `oauth_start_rate_limit=0` disables it and falls back to the proxy limit
  alone). The proxy limit remains the outer boundary either way;
- restrict the admin host independently where possible;
- avoid logging `Authorization`, cookies, request bodies, or upstream credentials.

Before a forwarded response is returned to a caller, the application scans it for the exact
upstream credential that was handed to the upstream in the same request, both as literal wire
bytes and across decoded JSON / SSE string fragments, and truncates the response if that
credential reappears. This response guard is a defense-in-depth layer, not a general
data-loss-prevention filter: it matches only the exact known credential, so it does not detect
an encoded, truncated, or otherwise transformed key, and it does not detect arbitrary other
sensitive data an upstream may return. Treat each caller key as a sensitive credential and
protect it at the proxy, storage, and logging boundary; the guard is one layer, not a
substitute for choosing trusted upstreams and keeping key material private.

The application accepts forwarding metadata only from configured trusted proxy addresses. An invalid or duplicate `X-Forwarded-For` falls back to the direct peer address, even if another IP header is valid. The application does not use `CF-Connecting-IP` or `True-Client-IP` as an alternative source.

Review upstream operators as a trust boundary. Their reported usage determines actual charges and donation rewards and may exceed the admission reserve; the platform cannot independently verify a provider's token accounting. Public HTTP endpoints remain supported, but send credentials and content without transport encryption. Prefer HTTPS and review any HTTP exception before approving a donated source.

Original-error diagnostics may retain a credential or other private content echoed by an upstream. Only administrators and currently authorized level-6 stewards can read them; protect the database and its backups accordingly. Silent retries before an accepted response can also duplicate provider work when delivery is uncertain. Personal routes default to retries off; charity routes stop retrying as soon as HTTP 200 is received. See the [API contract](api-contract.md) for billing and error behavior.

Source auditing distinguishes a direct peer, a validated forwarded client and a fallback peer. Before using shared-IP findings, verify the complete direct/CDN → Nginx → application chain for both IPv4 and IPv6, including an untrusted client-supplied forwarding header and malformed or duplicate values. A proxy address incorrectly treated as a client can associate many unrelated users. Fallback-quality addresses are excluded from shared-IP findings, but configuration still determines whether a forwarded address is trustworthy. Client headers are self-reported clues and cannot establish the identity of a relay or application.

### Nginx and Cloudflare notes

When Nginx is on the same host, keep the application on loopback and set `NONBIRI_TRUSTED_PROXY_CIDRS=127.0.0.1/32,::1/128`. These are the application's trusted Nginx peers; configure Cloudflare's trusted ranges at Nginx instead.

If Cloudflare proxies the public hosts, configure the Nginx realip module for **both the user and administrator `server` blocks**. Set `set_real_ip_from` using Cloudflare's current official [IPv4 list](https://www.cloudflare.com/ips-v4/) and [IPv6 list](https://www.cloudflare.com/ips-v6/), and use `real_ip_header CF-Connecting-IP`. Both servers can inherit the same settings from `http` or include the same validated configuration. An administrator-only setting leaves the user host recording Cloudflare peers. Preferably firewall public origin TLS ports to those ranges; keep the range configuration current.

`real_ip_recursive` defaults to `off`. Cloudflare's standard `CF-Connecting-IP` contains one address, so recursive search is unnecessary for this setup. Recursion matters when the chosen header contains a chain: `on` selects its last non-trusted address, while `off` selects its last address after the original peer matches `set_real_ip_from`. Choose it according to the actual trusted proxy chain. See the [Nginx realip directives](https://nginx.org/en/docs/http/ngx_http_realip_module.html) and [Cloudflare header semantics](https://developers.cloudflare.com/fundamentals/reference/http-headers/).

After Nginx has validated the direct peer, discard any client-supplied forwarding chain and send one canonical client address to the application. A representative location block is:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Real-IP $remote_addr;

    proxy_buffering off;
    proxy_read_timeout 1200s;
    proxy_send_timeout 1200s;
}
```

Use separate `server` blocks/certificates for user and administrator hosts so the administrator host can have stricter network or identity controls. Adjust the upstream port and timeouts to the actual deployment; test SSE through the complete Cloudflare → Nginx → application path.

Overwrite forwarding headers with the validated `$remote_addr`; do not append a client-supplied chain using `$proxy_add_x_forwarded_for` at this boundary. Validate the Nginx configuration before reload, then check both hosts with IPv4/IPv6 clients and direct requests carrying forged Cloudflare/XFF headers. A direct peer outside the configured Cloudflare ranges must not control the forwarded client address.

The audit address is the network address reported by this trusted path. It does not establish a final device's identity or bypass a visitor's own proxy. Cloudflare Worker subrequests and Pseudo IPv4 can also change the address reported in visitor headers; account for those [documented header behaviors](https://developers.cloudflare.com/fundamentals/reference/http-headers/).

A proxy correction affects new captures. Source records retain the selected address and its quality, without raw `CF-Connecting-IP`, `X-Forwarded-For` or `CF-Ray`. If earlier trusted per-request evidence was not retained, historical visitor addresses cannot be reconstructed from those records. Do not replace them using an account's newer address or approximate time/User-Agent matches.

Model calls allow up to 900 seconds for upstream response headers and 1200 seconds for the whole logical request, including retries and streaming. Keep proxy timeouts at least 1200 seconds. These are application defaults, not editable site settings. Model discovery retains its separate five-minute worker budget.

The image activity accepts a task promptly and uses separate status/image reads; the browser does not hold one generation connection open for its 30-minute default execution deadline. All provider requests are made by the server under the activity's shared limits. Configure the endpoint and encrypted key privately in the administrator station, and review the [image activity guide](image-activity.md). No ordinary `/v1/images/generations` route is enabled.

Allow sufficient process memory beyond the activity's default 512 MiB budget, database and normal request overhead. Images and queued prompts are intentionally not durable: maintenance cancels queued image tasks with refunds, dispatched work continues where possible, and stopping the service loses cached images. A restart refunds queued tasks with lost payloads; known upstream tasks resume queries, while uncertain submissions pause new dispatch and require administrator recovery. Do not attempt to recover an uncertain result by manually repeating its generation POST.

Administrators configure the model request-body limit with `model_request_body_limit_mib`, an integer from 1 to 64 MiB, defaulting to 10 MiB. Match the reverse-proxy body limit to the intended application limit; for example, a 64 MiB application limit needs Nginx `client_max_body_size 64m` on both relevant hosts. Oversized application requests return 413 before upstream dispatch or credit reservation. Other APIs retain their own limits.

Raw error retention defaults to 30 days, 1 MiB per event and a 1 GiB raw-payload budget. Database, indexes, WAL, summaries and backups need additional disk capacity; the payload cap is not a disk-size cap. Held bodies count toward it. Full capacity omits new originals, leaving safe summaries and API service available. Restrict diagnostic pages and snapshots: original errors can contain prompts or credentials echoed by an upstream, despite the absence of active request-body logging. Review instance legal overrides for these source/error policies before opening the new behavior; they are preserved, not automatically rewritten by upgrade.

## Proxy maintenance page

Install [maintenance.html](../deploy/maintenance.html) as /usr/share/nonbiriapi/maintenance.html, readable by Nginx. Include [the map example](../deploy/nginx-maintenance-map.conf.example) once in the http block and [the server example](../deploy/nginx-maintenance-server.conf.example) in each existing user/admin server block. Keep the existing TLS, proxy headers, SSE, trusted-proxy and rate-limit settings. Ensure each application proxy location inherits proxy_intercept_errors off and the fallback error_page directive; a location with its own error_page needs the same fallback rule.

The shared page works without the application and offers Chinese/English text, light/dark colors and a keyboard-accessible refresh button. Nginx-generated 502/503/504 responses become 503 with Retry-After: 30 and Cache-Control: no-store. /admin/api, /api, /v1 and their subpaths, plus /healthz and /readyz, receive JSON. Application responses, including application 4xx/5xx, pass through unchanged because [proxy_intercept_errors](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_intercept_errors) is off.

This fallback also covers proxy timeouts or invalid upstream responses before response headers reach the client. It cannot replace an interrupted response after headers were sent, or handle failures before the request reaches Nginx. Test the actual layout with nginx -t before reload, then check both hosts with an unavailable upstream, an application-generated error and recovery.

<a id="beta1-database-compatibility-and-version-changes"></a>

## Database compatibility and version changes

rc.6 accepts a fresh database, the final rc.5 schema at commit `8949a3d6e5b3d7536549f42a4c597393fccab62a`, and registered rc.6 schemas. The database remains Generation 2 (`application_id=0x4E425249`, `user_version=2`); account exports use schema 12. Compatibility is checked against the complete structural manifest. Earlier releases, partial schemas and unknown intermediate states are rejected without repair.

The upgrade adds ledger-compaction storage, history-query indexes, an explicit migration version and Lake Notes save-format versions. It preserves accounts, credentials, balances, donations, saved game rules, configuration and instance legal overrides. Six-hour maintenance compacts expired credit details into balance baselines and audit totals before removing them. Necessary settlement, legal-hold and idempotency evidence follows its business lifecycle.

From v1.0.0 onward, every stable v1.x release must support a direct upgrade from any earlier stable v1.x database. Prereleases are outside that long-term guarantee. The [database upgrade guide](database-upgrades.md) describes the baseline and migration rules.

Startup validates the registered source, applies supported changes atomically, checks target configuration and credentials before commit, and recovers unfinished work before opening listeners. It does not replay the complete historical ledger on every upgrade. Run `./nonbiriapi maintenance verify` against a stopped current-version database or an upgraded consistent copy for full structural, historical accounting and domain audits. Verification uses the normal private environment and does not repair, migrate, start workers or open listeners; SQLite may create coordination files.

Keep the database, master key, encrypted instance matching material, configuration and matching binary together. A binary-only downgrade is unsupported; rollback restores the complete stopped snapshot. After reopening, do not overwrite newly accepted user data automatically with an old snapshot.

Fresh creation requires the main database and its sidecars to be absent. It starts with maintenance on and public features closed. Opening those features and publishing instance legal text are operator actions.

Saved Fat Fish levels and replays retain their own engine versions. An update does not rewrite drafts, publish levels or replace selected period nodes. Legacy content cleanup remains the explicit procedure below.

Deployment helpers are maintained separately. Review their actual behavior and bind the selected source to an immutable commit. Reuse passing CI and a trusted final artifact when their inputs match; transfer and verify that artifact instead of rebuilding on the production host by default.

## Explicit Fat Fish legacy cleanup

This optional destructive operation is not part of normal startup and must be approved for the exact instance and consistent source. Stop the service, preserve a complete matching recovery set and use the target binary with that instance's normal protected environment. Do not point an external SQLite client at the online database.

Provide `source.json` with lowercase-hex `instance_identity` (64 characters), `source_commit` (40), `source_tree` (40) and `source_schema_hash` (64) taken from the verified source, not invented placeholders. Planning selects old v1/v2 content and associated periods/nodes/revisions, progress/ranks, challenges and playtests; review the resulting precise ID manifest before applying it.

```sh
./nonbiriapi maintenance fatfish-cleanup-plan --source source.json > manifest.json
./nonbiriapi maintenance fatfish-cleanup --source source.json   --manifest manifest.json --operation-key "$CLEANUP_OPERATION_KEY" > receipt.json
```

These commands open the configured database offline, without listeners or normal workers. The plan, source and stable operation key must match. Pending game work is terminated through its real transitions and eligible unsettled tickets are refunded before deletion. Refunds, exact deletion and the source-bound receipt commit together; a repeated identical operation returns the receipt and never clears later v3 content. Mismatched or changed source state is rejected. Accounts, site wallets, settled financial/reward receipts, other games, source assets, configuration and legal text are preserved. Validate foreign keys, integrity, capacity and balance conservation on the resulting stopped copy before reopening. After reopening, do not overwrite new data with the old snapshot.

## Manual same-generation deployment procedure

1. Fix the source commit, supported source schema and impact of the change. Generation 2 alone does not prove compatibility. Reuse valid CI and release evidence; run only missing platform, startup, storage or recovery checks. Validate schema changes using isolated synthetic predecessor databases before the production window.
2. Build the final Linux/amd64 `CGO_ENABLED=0 -tags dist -trimpath` binary once in a controlled environment. Record commit, tree, toolchain and SHA256, copy it into a new immutable release directory, and verify the hash on the host.
3. Close public admission, drain accepted work and stop the service. Create one complete protected snapshot containing the database and existing sidecars, exact old release, environment, master key, unit, manifest and checksums. An unchanged stopped-state retry does not need another snapshot.
4. Preserve runtime configuration and custom legal text. Point the current-release link at the prepared release and start the target once. Validate local health and the active binary identity before reopening.
5. Reopen admission. Verify both public hosts, login boundaries and changed pages while observing the process and errors for about 60 seconds. Extend observation or add an isolated audit only for a concrete fault or remaining evidence gap.
6. Remove temporary verification copies and processes. Ordinary deployment recovery sets retain the most recent two successful switches, excluding independent disaster recovery, legal retention and any unresolved incident set. Do not retroactively delete unrelated older archives.

If startup fails before reopening, stop the target and preserve its diagnostic state. Restore the complete matching pre-change set when rollback is needed; an old binary alone is not a rollback. After reopening, new user data must not be overwritten automatically by the old snapshot. Diagnose public proxy/TLS failures while preserving accepted target data.

## Backup and restore test

Verify the recovery procedure with a complete restore in an isolated directory, using its matching binary and master key. Reuse matching Linux upgrade/restore evidence for an unchanged procedure; a production snapshot does not need a second full rehearsal by default. Never open an online production database with an external SQLite client or rehearse against its active path.

## Starting fresh from an unsupported version

There is no direct upgrade from releases before rc.5. Preserve a complete stopped recovery set and configure a separate empty database path. Do not copy tables, credentials or configuration rows into the new database by hand. Re-enter required settings and review the fresh safety gates before opening the instance. Keep the old database until its recovery or retention purpose ends.

## Deployment limitations

- Production targets Linux/amd64. All releases contain source code only; build the selected revision with the documented toolchain.
- SMTP settings are reserved and do not send alert email in the current prereleases.
- Real Discord OAuth and upstream success flows must be tested with disposable staging credentials before public operation.
- Choose a maintenance window for every update and keep a known-good binary, environment backup, and database backup together.
