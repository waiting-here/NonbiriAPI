# VPS deployment with systemd

This guide describes the supported single-instance model: one Linux/amd64 binary, a dedicated service user, local SQLite and a TLS reverse proxy. The current source prerelease is 1.0.0-rc.3. Supported upgrade sources include the complete rc.2 maintenance database at db959c64674afc531046a63066de0464725d439c and the administration maintenance database at 84018acbd594765c563cc0ee4083d206e0bd6a77. Alpha deployments require a fresh cutover. Verify compatibility, backups, configuration, instance legal text and changed behavior before opening a deployment. See [configuration.md](configuration.md) for environment settings and [image-activity.md](image-activity.md) for activity operations.

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

Raw error retention defaults to 30 days, 1 MiB per event and a 1 GiB raw-payload budget. Database, indexes, WAL, summaries and backups need additional disk capacity; the payload cap is not a disk-size cap. Held bodies count toward it. Full capacity omits new originals, leaving safe summaries and API service available. Restrict diagnostic pages and snapshots: original errors can contain prompts or credentials echoed by an upstream, despite the absence of active request-body logging. Review instance legal overrides for these source/error policies before opening the new behavior; they are preserved, not automatically rewritten by upgrade.

<a id="beta1-database-compatibility-and-version-changes"></a>

## Database compatibility and version changes

Current Unreleased rc.5 targets the exact rc.4 source at commit `8a46c72d911a914eabcb7ef17c537e7ac12d6969` (tree `cfb3b4bf2be82aca5b14336c8295491c4719ab87`); final candidate acceptance must verify the actual source snapshot and target manifest. SQLite remains Generation 2, while current account exports use schema 12. A shared generation number is not compatibility evidence. The historical rc.4/rc.3 paths below retain their own source boundaries.

The current upgrade preserves accounts, encrypted credentials, balances, settled ledgers, other games, configuration and instance legal overrides. Renewed-review matching uses stable encrypted instance material; preserve it with the database and master key. A supported master-key rewrap must not rederive the review key or clear requirements. Missing/damaged matching material is an initialization error, not an instruction to generate replacement material. Lake Notes starts hidden/closed with exchanges disabled. New Fat Fish content defaults to v3; legacy reset requires the separate explicit offline action below.

The formal rc.4 upgrade source is the complete rc.3 repair database at commit `37e060ab0d0f29d632fe6b8036839b413388812a` (tree `4b44e6fb11ab6d72cea7fecf1ea45ea615594274`); its account export format is schema 11. A separate verified path covers the exact preceding deployed source at commit `4e06025c6bf23fbb0f34db96673b45ed01c42e97` (tree `6af9d8349d9049197366f29984e2e413090b7814`). Other intermediate schemas are unsupported; use a consistent backup from the exact supported source before deployment. The older source details below describe the published rc.3 upgrade path.

The database remains Generation 2: SQLite application_id=0x4E425249 and user_version=2. Fresh creation requires the main/WAL/SHM set to be absent. For the published rc.3 release, supported upgrade sources include the complete rc.2 maintenance database at db959c64674afc531046a63066de0464725d439c and the administration maintenance database at 84018acbd594765c563cc0ee4083d206e0bd6a77. Extensions, role migration, foreign keys and all four asset ledgers are checked atomically. Existing accounts, credentials, balances, settled charges, donations, bindings, saved game rules, configuration and instance legal overrides remain intact. Old manual level-5 stewards become level 6; model admission moves the former level-5 bit to level 6 and initializes new level 5 from former level 4. Historical audit roles and replay receipts are not rewritten. Existing total Token usage is preserved without inventing input/output splits. Unknown or partial structures are rejected before source writes. Older binaries reject the new manifest; rollback requires the complete matching stopped snapshot. Arbitrary schema repair and Generation 1 import remain unsupported.

Fat Fish level compatibility is versioned within Generation 2. New levels and the eight example levels use engine version 3 with scoring version 1; existing immutable level versions, active challenges, history, and period node bindings remain attached to their saved versions. An application update does not convert legacy drafts, publish new versions, or change existing node selections. An administrator can explicitly convert an older draft to version 3, save it, publish the immutable version, playtest that version, then manually select it for a node when ready.

Before a writable source open, existing files are copied through no-follow read-only handles to a private validation directory. Header, manifest, foreign keys, indexes, sidecars and contextual credential envelopes are validated there. Unsupported sources are rejected without repair or new source-side WAL/SHM files. The exact predecessor exception above does not extend to other intermediate schemas; sharing Generation 2 is insufficient evidence of compatibility.

The rc.2 maintenance source schema hash is dcce93b162d6f9540ec45118fbcc9661b633119fe35037213506a8f956117098. The administration maintenance source at 84018acbd594765c563cc0ee4083d206e0bd6a77 has complete manifest fb66fca4e735b786a546f1ff0bd02ecb9f48adc4522dbadce2c5f8158498558b. It is also accepted for the scan-task and discovery-dispatch extension. Record the target build’s canonical schema fingerprint and complete manifest with the artifact; do not infer compatibility from the generation number. The published v1.0.0-rc.2 tag still points to afc00a33a9764406db489b5bc3b3c7098aff421b; the maintenance source is later than that tag. Export schema 10 is independent of SQLite generation 2. Retain instance legal overrides for operator review: a binary update does not publish the bundled replacement text. No compatibility claim covers arbitrary unreleased intermediate databases.

Therefore:

- installing the current binary over an alpha or Generation 1 database does not upgrade it;
- switching only the binary to an older version is never a supported downgrade;
- a stateful rollback must restore a complete compatible snapshot, not combine an old binary with the current database;
- a cutover from an alpha release deliberately starts with an empty Generation 2 database and loses active application state unless the operator later re-enters it manually;
- a fresh Generation 2 database starts with maintenance on and registration, activities, charity, donation intake, and games off. Keep those gates closed until instance legal text, required configuration, initialization, and smoke tests pass.

This source update uses the existing startup environment variables. New diagnostic, audit, inactivity and activity settings are database-backed and must be reviewed through their authorized controls. A destructive fresh cutover resets them; a supported upgrade preserves instance configuration and starts the new inactivity policy disabled and the picture-book activity hidden with no opening period.

The companion deployment helper is maintained separately and is **not shipped by this repository**. Any helper used for this cutover must expose exactly four operator entry classes:

1. default interactive normal deployment to a trusted `origin` annotated tag or remote branch, fixed immediately to an immutable commit;
2. `--restore-snapshot`, which accepts only a complete snapshot whose checksum, release, schema, configuration, master key, and unit match;
3. `--destructive-fresh-deploy`, a permanent high-risk upgrade/downgrade escape hatch that first preserves a complete source snapshot, then removes only the revalidated active database/WAL/SHM paths and creates a fresh target-generation deployment;
4. `snapshot inventory`, `snapshot import`, and `snapshot delete` management.

Any helper must fix a selected ref to an immutable commit and preserve the complete recovery set. Review the helper's actual behavior before using it: an option that merely skips tests does not verify CI evidence. Reuse successful checks only when source tree, relevant inputs, lockfiles, toolchain and target match. A valid final artifact should be transferred and checksum-verified rather than rebuilt by default on the production host. Destructive fresh and stateful restore operations require explicit operator authorization, a complete matching snapshot and precise path checks; normal compatible updates follow the procedure below.

A destructive fresh cutover deletes the active database set and therefore removes users, sessions, OAuth state, and CallerKeys; endpoints, upstream credentials, models, catalogs, and routing state; public reports, retained report fingerprints/tombstones, donation lineage, charity resources, donations, reviews, and routing state; all four credit/activity wallets, cumulative `donation_credit`, ledger entries, claims, levels, check-ins, welfare and Thursday activity, usage totals, and per-user limits; announcements, request/usage logs, audits, alerts, lifecycle records, and worker checkpoints; display, OAuth-gate, anti-abuse, charity, economy, timezone, maintenance, registration, donation-guidance, and legal settings in `site_config`; and every game's queues, sessions, pending results, summaries, ranks, and statistics. Process-memory Debug sessions, queued image payloads and cached images also end when the service stops. The snapshot includes retained original-error bodies and source facts; it does not turn RAM-only prompts or images into database content. The protected source snapshot remains sensitive and may still contain all persisted data. It is not an account export and must be protected together with the original release, environment/configuration, master key, unit, manifest, and checksums.

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

## Cutting over from alpha or Generation 1

There is no in-place path from alpha or Generation 1. With a separately reviewed compatible helper and explicit authorization for the destructive operation, use its destructive-fresh flow: select and pin the trusted target; stop the service; create and verify a complete source snapshot; enter the complete target-bound confirmation phrase from `/dev/tty`; remove only the exact revalidated active database/WAL/SHM; start the target against an absent database path; verify the fresh safety gates; then reapply approved instance legal text and required configuration. Do not copy tables, rows, encrypted credentials, site configuration, or legal overrides into Generation 2 by hand.

If the cutover fails before the target passes local health and reaches its recorded commit point, restore the complete source snapshot and old release. After that commit point, a failure limited to public proxy, DNS, or TLS checks keeps the new service and source snapshot in place and reports the external fault; it does not automatically roll back the database. If the source snapshot is missing, corrupt, incompatible, or has been cleaned up, stateful rollback is unavailable: the safe choices are to keep the current compatible deployment, repair/restore from another verified complete snapshot, or perform a separately confirmed destructive fresh deployment. The helper must never offer a binary-only downgrade or fabricate a database for the old version.

## Deployment limitations

- A deployment coming from alpha requires a fresh Generation 2 database. Current Generation 2 databases are validated; exact supported predecessors receive the additive tables, indexes, and defaults described above. A completely absent main/WAL/SHM path set permits fresh creation, while every unsupported existing state is rejected without repair.
- The release target is Linux/amd64 and the release process is source-first.
- SMTP settings are reserved and do not send alert email in the current prereleases.
- Real Discord OAuth and upstream success flows must be tested with disposable staging credentials before public operation.
- Choose a maintenance window for every update and keep a known-good binary, environment backup, and database backup together.
