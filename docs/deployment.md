# VPS deployment with systemd

This guide describes the supported single-instance operating model for the `1.0.0-rc.1` source prerelease: one Linux/amd64 binary built from the exact tagged source, a dedicated system user, a systemd unit, a local SQLite database, and a reverse proxy that provides public TLS. The validated release upgrade is complete beta.4 → rc.1 with existing data preserved. Alpha deployments require a fresh cutover. Verify compatibility, backups, configuration, legal text, and smoke tests before opening any deployment. See [configuration.md](configuration.md) for the full environment and runtime-settings reference.

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
version=1.0.0-rc.1
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

Do not trust arbitrary `X-Forwarded-*` headers. The application accepts forwarding metadata only from configured trusted proxy addresses; malformed or duplicate values are discarded wholesale and the direct proxy peer metadata is used instead.

### Nginx and Cloudflare notes

When Nginx is on the same host, keep the application on loopback and set `NONBIRI_TRUSTED_PROXY_CIDRS=127.0.0.1/32,::1/128`; the application trusts Nginx, not the whole Cloudflare address space. If Cloudflare proxies the public host, configure Nginx `set_real_ip_from` from Cloudflare's **current official IPv4/IPv6 lists**, use `real_ip_header CF-Connecting-IP`, enable `real_ip_recursive`, and preferably firewall the public TLS ports to those ranges. Do not copy a stale hard-coded range list from this document.

After Nginx has validated the direct peer, discard any client-supplied forwarding chain and send one canonical client address to the application. A representative location block is:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_set_header X-Forwarded-For $remote_addr;

    proxy_buffering off;
    proxy_read_timeout 1200s;
    proxy_send_timeout 1200s;
}
```

Use separate `server` blocks/certificates for user and administrator hosts so the administrator host can have stricter network or identity controls. Adjust the upstream port and timeouts to the actual deployment; test SSE through the complete Cloudflare → Nginx → application path.

Model calls allow up to 900 seconds for upstream response headers and 1200 seconds for the whole logical request, including retries and streaming. Keep proxy timeouts at least 1200 seconds. These are application defaults, not editable site settings. Model discovery retains its separate five-minute worker budget.

<a id="beta1-database-compatibility-and-version-changes"></a>

## Database compatibility and version changes

The database remains Generation 2: SQLite `application_id=0x4E425249` and `user_version=2`. Fresh creation requires the main/WAL/SHM set to be absent. The release upgrade gate covers complete populated beta.4 → rc.1, ending at 117 tables. Missing extensions are applied atomically before the complete manifest, foreign keys, both asset ledgers and reward capacity are validated. Unknown or partial structures are rejected before source writes; a second startup adds nothing. Existing identities, both wallets, settled charges, game rules, configuration, site branding and custom legal text remain intact. Existing Fishing, LinkLink and RPS games retain their saved version-1 or version-2 rules. The three new games use independent rules version 1 and start disabled on sources without their settings; configured candidate instances retain their current settings. No historical payment source or newcomer completion is invented. Older binaries reject the new manifest; rollback requires the complete matching stopped snapshot. Alpha/Generation 1 and arbitrary schema repair remain unsupported.

Before a writable source open, existing files are copied through no-follow read-only handles to a private validation directory. Header, manifest, foreign keys, indexes, sidecars and contextual credential envelopes are validated there. Unsupported sources are rejected without repair or new source-side WAL/SHM files. Exact older manifest recognizers are implementation safeguards, not a release guarantee for unreleased intermediate schemas. Updating an already deployed rc.1 candidate requires its exact source, schema and runtime changes to be checked separately; sharing Generation 2 is insufficient evidence.

Therefore:

- installing the current binary over an alpha or Generation 1 database does not upgrade it;
- switching only the binary to an older version is never a supported downgrade;
- a stateful rollback must restore a complete compatible snapshot, not combine an old binary with the current database;
- a cutover from an alpha release deliberately starts with an empty Generation 2 database and loses active application state unless the operator later re-enters it manually;
- a fresh Generation 2 database starts with maintenance on and registration, activities, charity, donation intake, and games off. Keep those gates closed until instance legal text, required configuration, initialization, and smoke tests pass.

This release adds no startup environment-variable names relative to beta.4. An existing environment file must still satisfy the current validation rules and is retained by the separately maintained helper, but every database-backed runtime setting is reset by a destructive fresh cutover and must be reviewed or re-entered through the administrator station.

The companion deployment helper is maintained separately and is **not shipped by this repository**. Any helper used for this cutover must expose exactly four operator entry classes:

1. default interactive normal deployment to a trusted `origin` annotated tag or remote branch, fixed immediately to an immutable commit;
2. `--restore-snapshot`, which accepts only a complete snapshot whose checksum, release, schema, configuration, master key, and unit match;
3. `--destructive-fresh-deploy`, a permanent high-risk upgrade/downgrade escape hatch that first preserves a complete source snapshot, then removes only the revalidated active database/WAL/SHM paths and creates a fresh target-generation deployment;
4. `snapshot inventory`, `snapshot import`, and `snapshot delete` management.

Any helper must fix a selected ref to an immutable commit and preserve the complete recovery set. Review the helper's actual behavior before using it: an option that merely skips tests does not verify CI evidence. Reuse successful checks only when source tree, relevant inputs, lockfiles, toolchain and target match. A valid final artifact should be transferred and checksum-verified rather than rebuilt by default on the production host. Destructive fresh and stateful restore operations require explicit operator authorization, a complete matching snapshot and precise path checks; normal compatible updates follow the procedure below.

A destructive fresh cutover deletes the active database set and therefore removes users, sessions, OAuth state, and CallerKeys; endpoints, upstream credentials, models, catalogs, and routing state; public reports, retained report fingerprints/tombstones, donation lineage, charity resources, donations, reviews, and routing state; both spendable credit wallets, cumulative `donation_credit`, ledger entries, claims, levels, check-ins, welfare and Thursday activity, usage totals, and per-user limits; announcements, request/usage logs, audits, alerts, lifecycle records, and worker checkpoints; display, OAuth-gate, anti-abuse, charity, economy, timezone, maintenance, registration, donation-guidance, and legal settings in `site_config`; and every game's queues, sessions, pending results, summaries, ranks, and statistics. Process-memory Debug sessions also end when the service stops. The protected source snapshot remains sensitive and may still contain all persisted data. It is not an account export and must be protected together with the original release, environment/configuration, master key, unit, manifest, and checksums.

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
