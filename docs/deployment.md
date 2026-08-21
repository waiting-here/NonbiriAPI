# VPS deployment with systemd

This guide describes the first supported operating model for `v1.0.0-alpha.1`: one compiled binary, a dedicated system user, a systemd unit, a local SQLite database, and a reverse proxy that provides public TLS. See [configuration.md](configuration.md) for the full environment and runtime-settings reference.

The commands are examples. Replace paths, hostnames, users, and package-manager commands for the target VPS. Do not copy real secrets into a Git checkout.

## Recommended layout

```text
/etc/nonbiriapi/admin.env       # 0640, root:nonbiriapi
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
sudo useradd --system --home /var/lib/nonbiriapi --shell /usr/sbin/nologin nonbiriapi
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

The environment file must set `NONBIRI_DB_PATH=/var/lib/nonbiriapi/nonbiriapi.db` and `NONBIRI_MASTER_KEY_FILE=/etc/nonbiriapi/master.key`. Keep `NONBIRI_LISTEN_ADDR` on loopback when a reverse proxy is in front.

Generate the master key once, before the first start. Do not regenerate it for an existing database: a new key makes encrypted upstream credentials unreadable.

```sh
sudo install -o nonbiriapi -g nonbiriapi -m 0600 /dev/null /etc/nonbiriapi/master.key
openssl rand -hex 32 | sudo -u nonbiriapi tee /etc/nonbiriapi/master.key >/dev/null
sudo chmod 0600 /etc/nonbiriapi/master.key
```

Run those key-generation commands only on a new installation: the first command truncates its target. Replace the example key only before the first database is created. If a real database already exists, preserve its original key. The loader deliberately rejects group-readable modes such as `0640`; the service account must own this `0600` file so it can read it without broadening access.

For a manual launch outside the example systemd unit, the application itself now enforces owner-only access to the database directory and files: on startup it resolves the database directory to an absolute path (so the current directory for a relative database path and root for a root-level path are covered too) and verifies it is owned by the current account and grants no group or other access, and it tightens the database file and its `-wal`/`-shm` sidecars to `0600`. If it cannot verify owner-only access it refuses to start, so a non-standard launch no longer depends on the operator remembering `umask`. Create the database directory with mode `0700` owned by the service account; a pre-existing database file left world-readable (for example `0644`) is tightened to `0600`, while a group/other-accessible directory, a wrong owner, or a symlink or non-regular file at the database path or its `-wal`/`-shm` sidecars causes startup to fail closed (sidecars are checked before the database is opened). SQLite creates the `-wal` and `-shm` sidecars with the same mode as the database file, so once the database is `0600` every later sidecar creation is owner-only as well. Setting `umask 077` remains useful as a defense-in-depth layer but is no longer the sole protection.

## Build a release binary

Build on a controlled build host or in a clean checkout:

```sh
npm --prefix web ci
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
scripts/check-go.sh
CGO_ENABLED=0 go build -tags dist -trimpath -o nonbiriapi .
```

The `dist` build tag is required for the real frontend. An untagged binary contains development placeholder pages. Verify the output before installation with `go version -m ./nonbiriapi` and a temporary configuration/database.

Install into a versioned directory and update the symlink atomically:

```sh
release=/opt/nonbiriapi/releases/1.0.0-alpha.1
sudo install -d -o root -g root -m 0755 "$release"
sudo install -o root -g root -m 0755 nonbiriapi "$release/nonbiriapi"
sudo ln -sfn "$release" /opt/nonbiriapi/current
```

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
  `/api/auth/elevate` route; keep the ten-minute OAuth-state capacity in
  mind and test the chosen limits without weakening callback availability.
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
   sudo ln -sfn /opt/nonbiriapi/releases/1.0.0-alpha.1 /opt/nonbiriapi/current
   sudo systemctl start nonbiriapi.service
   sudo systemctl status nonbiriapi.service
   ```

7. Verify both configured hosts through the reverse proxy, then inspect the journal for startup errors. Confirm the user station, admin station, login boundary, and `/healthz` response. Do not treat a running process alone as a successful deployment.

If the binary fails to start, stop it, restore the previous symlink, and start the previous release. Do not delete the database or rotate the master key during rollback.

The unreleased credential-envelope upgrade is a one-way in-place migration. On
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

## Alpha limitations

- The schema is bootstrapped idempotently. There is no general versioned migration framework; the unreleased credential-envelope upgrade uses one dedicated, idempotent startup migrator.
- SMTP settings are reserved and do not send alert email in this release.
- Real Discord OAuth and upstream success flows must be tested with disposable staging credentials before public operation.
- Choose a maintenance window for every update and keep a known-good binary, environment backup, and database backup together.
