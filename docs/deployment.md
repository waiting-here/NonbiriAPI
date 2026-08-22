# VPS deployment with systemd

This guide describes the supported single-instance operating model introduced for `v1.0.0-alpha.1` and used by the local, unpublished `v1.0.0-alpha.2` release candidate: one compiled binary, a dedicated system user, a systemd unit, a local SQLite database, and a reverse proxy that provides public TLS. The latest published tag remains alpha.1 until the candidate is explicitly released. See [configuration.md](configuration.md) for the full environment and runtime-settings reference.

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

For a manual launch outside the example systemd unit, the application itself now enforces owner-only access to the database directory and files: on startup it resolves the database directory to an absolute path (so the current directory for a relative database path and root for a root-level path are covered too) and verifies it is owned by the current account and grants no group or other access, and it tightens the database file and its `-wal`/`-shm` sidecars to `0600`. If it cannot verify owner-only access it refuses to start, so a non-standard launch no longer depends on the operator remembering `umask`. Create the database directory with mode `0700` owned by the service account; a pre-existing database file left world-readable (for example `0644`) is tightened to `0600`, while a group/other-accessible directory, a wrong owner, or a symlink or non-regular file at the database path or its `-wal`/`-shm` sidecars causes startup to fail closed (sidecars are checked before the database is opened). SQLite creates the `-wal` and `-shm` sidecars with the same mode as the database file, so once the database is `0600` every later sidecar creation is owner-only as well. Setting `umask 077` remains useful as a defense-in-depth layer but is no longer the sole protection.

## Build a release binary

Build on a controlled build host or in a clean checkout. The race gate requires a working C compiler for Go's race detector; the final production build remains `CGO_ENABLED=0`.

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
version=1.0.0-alpha.2
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
    proxy_read_timeout 310s;
    proxy_send_timeout 310s;
}
```

Use separate `server` blocks/certificates for user and administrator hosts so the administrator host can have stricter network or identity controls. Adjust the upstream port and timeouts to the actual deployment; test SSE through the complete Cloudflare → Nginx → application path.

## Manual update procedure

1. Verify the release source and build on a separate build host.
2. Run the Go, race, frontend, and release-like embed gates.
3. Copy the new binary to a new versioned release directory; do not overwrite the active binary in place.
4. Stop the service before backing up or replacing the database-adjacent files:

   ```sh
   sudo systemctl stop nonbiriapi.service
   ```

5. Back up the database and any SQLite sidecars as one protected set. When the service is stopped, copy the main file plus existing `-wal` and `-shm` files, or use a tested SQLite backup tool. Example:

   ```sh
   stamp=$(date -u +%Y%m%dT%H%M%SZ)
   sudo mkdir -p "/var/backups/nonbiriapi/$stamp"
   sudo cp -a /var/lib/nonbiriapi/nonbiriapi.db \
     "/var/backups/nonbiriapi/$stamp/"
   for sidecar in /var/lib/nonbiriapi/nonbiriapi.db-wal /var/lib/nonbiriapi/nonbiriapi.db-shm; do
     [ -e "$sidecar" ] && sudo cp -a "$sidecar" "/var/backups/nonbiriapi/$stamp/"
   done
   ```

   Protect backups as carefully as the master key: the database contains encrypted credentials and private account metadata.

6. Point `/opt/nonbiriapi/current` at the new release and start the service:

   ```sh
   version=1.0.0-alpha.2  # replace with the release being installed
   sudo ln -sfn "/opt/nonbiriapi/releases/$version" /opt/nonbiriapi/current.next
   sudo mv -Tf /opt/nonbiriapi/current.next /opt/nonbiriapi/current
   sudo systemctl start nonbiriapi.service
   sudo systemctl status nonbiriapi.service
   ```

7. Verify both configured hosts through the reverse proxy, then inspect the journal for startup errors. Confirm the user station, admin station, login boundary, and `/healthz` response. Do not treat a running process alone as a successful deployment.

If the binary fails to start, stop it, restore the previous symlink, and start the previous release. Do not delete the database or rotate the master key during rollback.

The alpha.2 credential-envelope upgrade is a one-way in-place migration. On
startup, the new binary upgrades endpoint credentials transactionally before it
starts any HTTP listener or background worker. Any invalid credential aborts
the complete credential batch and startup; it never leaves a partially upgraded
batch serving traffic. Before installing this upgrade, stop the service and
back up the database together with any WAL/SHM sidecars, the current binary, and
the unchanged master key. Do not run the old binary against a database that has
completed the credential migration. Downgrade only by restoring the complete
pre-upgrade backup and then starting the old binary.

## Backup and restore test

A backup is not complete until it has been restored in an isolated directory with the same master key and opened by a test instance. Test this before onboarding users and periodically thereafter. Never test restoration against the production database path.

## Upgrading from alpha.1 to alpha.2

Alpha.2 is a pre-1.0 development release with breaking schema changes. Always keep a tested backup before upgrading.

1. **Stop the service and back up.** Follow the [manual update procedure](#manual-update-procedure): stop the service, then copy the database and any existing `-wal`/`-shm` sidecars as one protected set. Keep the old binary, environment file, master key and database backup together.
2. **New tables are automatic.** Tables introduced by alpha.2 use idempotent bootstrap statements and are created on the first boot of the new binary. The bootstrap is safe to repeat.
3. **Existing-table columns are automatic.** On first boot, the alpha.2 binary idempotently adds its missing columns and backfills the legacy usage projection. Do not run hand-written `ALTER TABLE` statements: repeating them against a database that has already started under alpha.2 will fail with duplicate-column errors.
4. **Credential envelope migration.** On startup, the binary upgrades endpoint-key envelopes automatically in an idempotent, transactional migration. The old binary cannot read the new envelope format; downgrade only by restoring the complete pre-upgrade backup.
5. **Fresh database alternative.** For test or staging, delete the database and let the new binary create a fresh one. Keep and reuse the same master key; do not generate a replacement key for an existing deployment.
6. **Breaking changes are expected.** Alpha is a pre-1.0 development phase. A versioned migration framework is planned for after 1.0, so always back up before an upgrade.
7. **Rollback.** Stop the new service, restore the complete pre-upgrade backup (database plus sidecars), and start the old binary. Never run the old binary against a database that has completed the credential migration or schema changes.

## Alpha limitations

- The schema is bootstrapped idempotently. There is no general versioned migration framework; the alpha.2 credential-envelope upgrade uses one dedicated, idempotent startup migrator.
- SMTP settings are reserved and do not send alert email in the alpha releases.
- Real Discord OAuth and upstream success flows must be tested with disposable staging credentials before public operation.
- Choose a maintenance window for every update and keep a known-good binary, environment backup, and database backup together.
