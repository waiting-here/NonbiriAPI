# VPS deployment with systemd

This guide describes the first supported operating model for `v1.0.0-alpha.1`: one compiled binary, a dedicated system user, a systemd unit, a local SQLite database, and a reverse proxy that provides public TLS.

The commands are examples. Replace paths, hostnames, users, and package-manager commands for the target VPS. Do not copy real secrets into a Git checkout.

## Recommended layout

```text
/etc/nonbiriapi/admin.env       # 0600, service-user readable
/etc/nonbiriapi/master.key      # 0600, service-user readable
/opt/nonbiriapi/releases/<ver>/nonbiriapi
/opt/nonbiriapi/current          # symlink to the active release directory
/var/lib/nonbiriapi/nonbiriapi.db
/var/backups/nonbiriapi/
```

Use a dedicated system user such as `nonbiriapi`. The service only needs to read its environment/key files and write its database directory.

## Prepare the host

Create the service and directories using the distribution's normal administration tools. For example:

```sh
sudo useradd --system --home /var/lib/nonbiriapi --shell /usr/sbin/nologin nonbiriapi
sudo install -d -o nonbiriapi -g nonbiriapi -m 0750 /var/lib/nonbiriapi
sudo install -d -o root -g nonbiriapi -m 0750 /etc/nonbiriapi
sudo install -d -o root -g root -m 0755 /opt/nonbiriapi/releases
sudo install -d -o root -g root -m 0750 /var/backups/nonbiriapi
```

Install the real `admin.env` from [admin.env.example](../admin.env.example), then:

```sh
sudo chown root:nonbiriapi /etc/nonbiriapi/admin.env /etc/nonbiriapi/master.key
sudo chmod 0640 /etc/nonbiriapi/admin.env /etc/nonbiriapi/master.key
```

The environment file must set `NONBIRI_DB_PATH=/var/lib/nonbiriapi/nonbiriapi.db` and `NONBIRI_MASTER_KEY_FILE=/etc/nonbiriapi/master.key`. Keep `NONBIRI_LISTEN_ADDR` on loopback when a reverse proxy is in front.

Generate the master key once, before the first start. Do not regenerate it for an existing database: a new key makes encrypted upstream credentials unreadable.

```sh
openssl rand -hex 32 | sudo tee /etc/nonbiriapi/master.key >/dev/null
sudo chown root:nonbiriapi /etc/nonbiriapi/master.key
sudo chmod 0640 /etc/nonbiriapi/master.key
```

Replace the example key only before the first database is created. If a real database already exists, preserve its original key.

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
- restrict the admin host independently where possible;
- avoid logging `Authorization`, cookies, request bodies, or upstream credentials.

Do not trust arbitrary `X-Forwarded-*` headers. The application fails closed for malformed forwarding data and only accepts forwarding metadata from configured trusted proxy addresses.

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

If the binary fails to start, stop it, restore the previous symlink, and start the previous release. Do not delete the database or rotate the master key during rollback. If a future release introduces a schema migration, follow its explicit migration instructions and retain the pre-migration backup.

## Backup and restore test

A backup is not complete until it has been restored in an isolated directory with the same master key and opened by a test instance. Test this before onboarding users and periodically thereafter. Never test restoration against the production database path.

## Alpha limitations

- The schema is bootstrapped idempotently; alpha.1 has no versioned migration framework.
- SMTP settings are reserved and do not send alert email in this release.
- Real Discord OAuth and upstream success flows must be tested with disposable staging credentials before public operation.
- Choose a maintenance window for every update and keep a known-good binary, environment backup, and database backup together.
