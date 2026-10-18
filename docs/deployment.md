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
version=1.0.0
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
upstream credential and explicitly sensitive request-adaptation values sent in the same request, both as literal wire
bytes and across decoded JSON / SSE string fragments, and truncates the response if that
credential reappears. This response guard is a defense-in-depth layer, not a general
data-loss-prevention filter: it matches those exact known values, so it does not detect
an encoded, truncated, or otherwise transformed key, and it does not detect arbitrary other
sensitive data an upstream may return. Treat each caller key as a sensitive credential and
protect it at the proxy, storage, and logging boundary; the guard is one layer, not a
substitute for choosing trusted upstreams and keeping key material private.

The application accepts forwarding metadata only from configured trusted proxy addresses. An invalid or duplicate `X-Forwarded-For` falls back to the direct peer address, even if another IP header is valid. The application does not use `CF-Connecting-IP` or `True-Client-IP` as an alternative source.

Review upstream operators as a trust boundary. Their reported usage determines actual charges and donation rewards and may exceed the admission reserve; the platform cannot independently verify a provider's token accounting. Public HTTP endpoints remain supported, but send credentials and content without transport encryption. Prefer HTTPS and review any HTTP exception before approving a donated source.

Original-error diagnostics may retain a credential or other private content echoed by an upstream. Administrators and currently authorized level-6 stewards can read the complete saved content. Level-5 stewards can also read site-wide logs and diagnostics, with IPv4 addresses redacted from upstream error details. Source IPs and other authorized fields remain visible. Protect the database and its backups accordingly. Ordinary user displays still redact IPv4 addresses. Silent retries before an accepted response can also duplicate provider work when delivery is uncertain. Personal routes default to retries off; charity routes stop retrying as soon as HTTP 200 is received. See the [API contract](api-contract.md) for billing and error behavior.

Source auditing distinguishes a direct peer, a validated forwarded client and a fallback peer. Before using shared-IP findings, verify the complete direct/CDN → Nginx → application chain for both IPv4 and IPv6, including an untrusted client-supplied forwarding header and malformed or duplicate values. A proxy address incorrectly treated as a client can associate many unrelated users. Fallback-quality addresses are excluded from shared-IP findings, but configuration still determines whether a forwarded address is trustworthy. Client headers are self-reported clues and cannot establish the identity of a relay or application.

### Protecting the management entry

Use a separate identity boundary for the administrator host. Protect the DNS/CDN
account and its recovery email with MFA, and keep recovery methods outside the VPS.
Encrypted storage and hidden credential fields limit exposure; an administrator
can still change upstream destinations, so protecting administrator access matters.

For a Cloudflare deployment:

1. Create an Access application for the exact administrator hostname and allow only
   the intended identities. Require [independent MFA](https://developers.cloudflare.com/cloudflare-one/access-controls/access-settings/independent-mfa/)
   with Windows Hello or a security key. Enroll a recovery authenticator before
   enforcing it. Use a practical session duration, such as 24 hours, and test it
   in a separate browser session. Keep the application's own administrator login.
2. Leave the public user host and model APIs outside this application. For automated
   checks, scope any exception to the anonymous `/healthz` and `/readyz` paths,
   or use a dedicated [service-auth policy](https://developers.cloudflare.com/cloudflare-one/access-controls/service-credentials/service-tokens/).
   Do not bypass the whole admin API or treat an Access token as an application session.
3. Restrict origin HTTPS to Cloudflare ranges and use a private, zone-specific
   [Authenticated Origin Pull certificate](https://developers.cloudflare.com/ssl/origin-configuration/authenticated-origin-pull/set-up/zone-level/).
   A shared Cloudflare certificate authenticates Cloudflare traffic but does not
   identify the owning zone. Keep the normal server certificate and Full (strict)
   validation as well. Public HTTP should only redirect or serve an intentionally
   configured certificate challenge; it must not proxy application traffic.
4. Install the private CA at Nginx, enable client-certificate verification in
   optional mode, and confirm the expected certificate arrives on both hosts.
   Then require it. Validate with `nginx -t` before each reload, including the
   deployment helper's loopback health probes; use only a local probe path if
   those probes do not carry the origin-pull certificate. Direct external requests
   and requests without the private client certificate must be rejected.
5. Keep a working SSH recovery session during the change. Verify management login,
   revocation, public pages, CallerKey calls and SSE. If access is lost, restore the
   affected proxy or Access configuration through the recovery channel; a network
   policy failure does not call for restoring an old application database.

Keep SSH key authentication and review listeners against their actual workloads.
An origin address can become known through DNS history or other hosted services;
restrict access independently of whether the address is known. Review optional
systemd restrictions on the target host: local-interface discovery uses netlink
on Linux, so an address-family allowlist must include `AF_NETLINK`.

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

The process logger emits a `runtime performance` aggregate at INFO once per minute.
Fixed route groups contain completed, active, peak, server-error and cancellation
counts, plus duration p50/p95/p99 and maximum in milliseconds. Percentiles are
histogram bucket upper bounds; `first_response_p95_ms` measures the first final
header/body/flush, not the first model token. The aggregate covers every request
reaching the application, including station-boundary refusals. `http.completed`
is the window's total completed requests, `http.active` is active work at emission,
and `http.peak` is the true simultaneous peak across routes. Do not sum route
peaks. Proxy refusals that never reach the process are outside these counters.
Long streams span windows and complete when their handler returns.

`db_waits` and `db_wait_ms` are database-pool deltas for the whole process;
`db_in_use` and `db_open` are samples at emission. The same fields are separated
under `db_pools.writer` and `db_pools.reader` as `waits,wait_ms,in_use,open`.
Wait time sums all waiters and can exceed wall time; a fall in writer waiting
must be compared with reader waiting too. Pool sizes remain one and two.

`connection.<operation>` measures connection acquisition; `transaction.<operation>`
measures work from begin through commit/rollback and connection return. Fixed
operations include `session`, `personal_models`, `charity_models`,
`charity_capability`, `catalog`, `fishing_cleanup`, `donation_expiry`,
`lake_checkpoint`, `lake_mutation`, `lake_profile`, `lake_recovery`,
`lake_retention`, `claim_accept`, `claim_prepare`, `claim_release`,
`claim_attempt`, `claim_complete`, `fishing_settle`, `reward_delivery` and
`activities_projection`. Transaction timing includes `BeginTx` after pool
acquisition. Fishing settlement returns its connection before publishing events.
Database phases count non-cancellation errors in `failures` and interrupted work
in `canceled`. HTTP groups retain `server_errors` for server failures. Game-state
transaction timing retains its existing start after the writer lock and decision
clock. Background workers use the same observer as requests.

`operation.activities_publish`, `operation.lifecycle_recovery` and
`operation.lifecycle_retention` cover the complete operation, including waits
and multiple transactions; they do not measure one continuous connection hold.
Lake operations also expose fixed `phase.<phase>.<operation>` rows for
authorization, configuration, profile reads, rules, encoding, writes,
idempotency, active-action recording, response projection and commit.
These rows overlap their parent transaction; do not sum them with the parent.
Each row's `active_ms` integrates concurrent elapsed time inside this window,
including unfinished work. A transaction spanning two windows contributes to
both windows even while `completed` and `max_ms` are still zero. Percentiles
and maximum continue to describe operations completed in the current window.

These aggregates contain no identities, raw paths, SQL, query strings, headers
or bodies and add no database records. Apply ordinary process-log rotation.
Collect CPU, RSS/available memory and network throughput using host tools;
application concurrency alone does not establish capacity. Compare equivalent
load windows and account for request mix when evaluating a deployment.

Existing content-hashed JS/CSS files use a one-year immutable cache. HTML and
SPA fallbacks remain `no-store`; fixed-path artwork and other files do not gain
immutable caching. Preserve these headers at the reverse proxy.

The image activity accepts a task promptly and uses separate status/image reads; the browser does not hold one generation connection open for its 30-minute default execution deadline. All provider requests are made by the server under the activity's shared limits. Configure the endpoint and encrypted key privately in the administrator station, and review the [image activity guide](image-activity.md). The separate `/v1/images/generations` API serves CallerKey requests through platform models with image generation enabled; it uses the model-call timeouts above and does not use the activity queue or image cache.

Allow sufficient process memory beyond the activity's default 512 MiB budget, database and normal request overhead. Images and queued prompts are intentionally not durable: maintenance cancels queued image tasks with refunds, dispatched work continues where possible, and stopping the service loses cached images. A restart refunds queued tasks with lost payloads; known upstream tasks resume queries, while uncertain submissions pause new dispatch and require administrator recovery. Do not attempt to recover an uncertain result by manually repeating its generation POST.

Administrators configure the model request-body limit with `model_request_body_limit_mib`, an integer from 1 to 64 MiB, defaulting to 10 MiB. Match the reverse-proxy body limit to the intended application limit; for example, a 64 MiB application limit needs Nginx `client_max_body_size 64m` on both relevant hosts. Oversized application requests return 413 before upstream dispatch or credit reservation. Other APIs retain their own limits.

Raw error retention defaults to 30 days, 1 MiB per event and a 1 GiB raw-payload budget. Database, indexes, WAL, summaries and backups need additional disk capacity; the payload cap is not a disk-size cap. Held bodies count toward it. Full capacity omits new originals, leaving safe summaries and API service available. Restrict diagnostic pages and snapshots: original errors can contain prompts or credentials echoed by an upstream, despite the absence of active request-body logging. Review instance legal overrides for these source/error policies before opening the new behavior; they are preserved, not automatically rewritten by upgrade.

## Proxy maintenance page

Install [maintenance.html](../deploy/maintenance.html) as /usr/share/nonbiriapi/maintenance.html, readable by Nginx. Include [the map example](../deploy/nginx-maintenance-map.conf.example) once in the http block and [the server example](../deploy/nginx-maintenance-server.conf.example) in each existing user/admin server block. Keep the existing TLS, proxy headers, SSE, trusted-proxy and rate-limit settings. Ensure each application proxy location inherits proxy_intercept_errors off and the fallback error_page directive; a location with its own error_page needs the same fallback rule.

The shared page works without the application and offers Chinese/English text, light/dark colors and a keyboard-accessible refresh button. Nginx-generated 502/503/504 responses become 503 with Retry-After: 30 and Cache-Control: no-store. /admin/api, /api, /v1 and their subpaths, plus /healthz and /readyz, receive JSON. Application responses, including application 4xx/5xx, pass through unchanged because [proxy_intercept_errors](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_intercept_errors) is off.

This fallback also covers proxy timeouts or invalid upstream responses before response headers reach the client. It cannot replace an interrupted response after headers were sent, or handle failures before the request reaches Nginx. Test the actual layout with nginx -t before reload, then check both hosts with an unavailable upstream, an application-generated error and recovery.

## Database compatibility and version changes

The v1.0.0 application starts from an empty database or current schema **21**.
Schema **16**, **17**, **18**, **19** and **20** use the standalone
[v1 converter](../cmd/nonbiri-upgrade-v1/README.md). Other prereleases and unknown
structures are rejected. SQLite identity remains Generation 2; exports use
format **20**. See [database upgrades](database-upgrades.md) for manifests, stable
migration guarantees and content versions.

Run `./nonbiriapi maintenance schema-info --json` without a production environment
or database to inspect the target's compatibility descriptor. Deployment helpers
must consume this output and the matching converter's `--schema-info --json`
instead of parsing Go files. The service descriptor has no historical sources;
the converter declares its exact offline sources. Use
`./nonbiriapi maintenance verify` on a stopped current database or consistent copy
for a full offline audit; normal startup performs the current checks and recovers
unfinished work. Historical cleanup commands are retired from the running binary.

Preserve the database, master key, encrypted instance matching material,
configuration and matching binary together. A binary-only downgrade is unsupported.
Fresh creation requires the database and sidecars to be absent and starts with
maintenance on and public features closed. Opening features and publishing
instance legal text remain separate operator actions.

## Manual same-generation deployment procedure

1. Pin the target to an exact commit and inspect its descriptor. Reuse valid CI
   and platform evidence; a complete PR run does not need a simultaneous manual
   full run. Rehearse changed storage/conversion and startup behavior once using
   an isolated consistent copy of the verified current source.
2. Build the Linux/amd64 `CGO_ENABLED=0 -tags dist -trimpath -buildvcs=true`
   binary in a clean controlled checkout. Build the converter from that same
   commit when required. Record the commit, tree, toolchain and artifact hashes.
   Transfer verified artifacts; the VPS does not repeat full builds or tests.
   A branch deployment pins the selected commit and needs no tag or GitHub Release.
3. Close admission, drain accepted work and stop the service. Take one complete
   protected recovery snapshot containing the database and existing sidecars,
   exact source release, environment, master key, unit, manifest and checksums.
   An unchanged stopped-state retry reuses that snapshot.
4. For schema 16, 17, 18, 19 or 20, run the converter with `--apply` under exclusive database access.
   After any eligible legacy Gateway import, remove
   `NONBIRI_GATEWAY_MODEL_CAPABILITIES` and the six
   `NONBIRI_SMTP_HOST/PORT/USER/PASS/FROM/TLS` keys. Preserve other environment
   lines and permissions; report key names, never values. The target application
   itself does not perform this conversion.
5. Switch to the prepared release and start it once. Confirm both local station
   health endpoints and the running binary identity, then reopen admission.
   Verify public hosts, login boundaries and changed pages alongside about
   60 seconds of process/error observation. Extend only for a concrete fault.
6. Remove finished verification copies and duplicate artifacts. Keep the two most
   recent successful ordinary recovery sets and any independently required
   disaster-recovery, legal or unresolved-incident material. Check actual references
   before removing old releases or snapshots.

Before reopening, failed conversion/startup can restore the complete matching
snapshot. After reopening, do not automatically replace newly accepted data with
an old snapshot; diagnose the fault while preserving the current state.

## Backup and restore test

Use an isolated directory with the recovery set's matching binary and key. Reuse
valid restore evidence for an unchanged procedure. Never connect an external
SQLite client to the online production database or rehearse against its active
path. The conversion rehearsal is not another long-lived recovery set.

## Starting fresh from an unsupported version

Preserve the complete stopped source set and select a separate empty database
path. Do not copy tables or credentials into the fresh database manually. Review
required settings and closed admission gates before opening it; retain the source
only while it has a defined recovery or retention purpose.

## Deployment limits

Production targets Linux/amd64. Published releases contain source; deployment
artifacts are built from the selected commit. Real OAuth and upstream success
flows use disposable staging credentials for pre-deployment acceptance. Use a
maintenance window and retain matching binary, environment, key and database.
