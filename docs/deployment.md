# VPS deployment with systemd

This guide describes the supported single-instance operating model for the unreleased `v1.0.0-beta.1` candidate: one Linux/amd64 binary built from the exact source commit, a dedicated system user, a systemd unit, a local SQLite database, and a reverse proxy that provides public TLS. The latest published prerelease remains `v1.0.0-alpha.3`. Verify the fresh-only boundary, backups, configuration, legal text, and smoke tests before opening any candidate deployment. See [configuration.md](configuration.md) for the full environment and runtime-settings reference.

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
version=1.0.0-beta.1
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

## Beta.1 fresh-only database and version changes

Beta.1 accepts only a completely absent database set or a validated Generation 2 database whose SQLite header contains `application_id=0x4E425249` and `user_version=2`. It does not run `ALTER`, schema repair, migration, or data import. Before a writable source open, an existing database is copied through no-follow read-only handles to a private validation directory; header, schema, foreign keys, indexes, sidecars, and contextual credential envelopes are checked there. An alpha or Generation 1 file, empty file, unknown generation, corrupt or unexpected schema, unsafe file shape, rollback journal, or anomalous sidecar is refused without modifying the source set or creating a new source-side WAL/SHM.

Therefore:

- installing a beta.1 binary over an alpha or Generation 1 database does not upgrade it;
- switching only the binary to an older version is never a supported downgrade;
- a stateful rollback must restore a complete compatible snapshot, not combine an old binary with the current database;
- a cutover from an earlier prerelease deliberately starts with an empty Generation 2 database and loses active application state unless the operator later re-enters it manually;
- a fresh beta.1 database starts with maintenance on and registration, activities, charity, donation intake, and games off. Keep those gates closed until instance legal text, required configuration, initialization, and smoke tests pass.

The companion deployment helper is maintained separately and is **not shipped by this repository**. Any helper used for this cutover must expose exactly four operator entry classes:

1. default interactive normal deployment to a trusted `origin` annotated tag or remote branch, fixed immediately to an immutable commit;
2. `--restore-snapshot`, which accepts only a complete snapshot whose checksum, release, schema, configuration, master key, and unit match;
3. `--destructive-fresh-deploy`, a permanent high-risk upgrade/downgrade escape hatch that first preserves a complete source snapshot, then removes only the revalidated active database/WAL/SHM paths and creates a fresh target-generation deployment;
4. `snapshot inventory`, `snapshot import`, and `snapshot delete` management.

The helper must present tags in version order and branches by name, identify branches as non-release targets, pin the selected commit, and allow `q` to exit before any destructive action. After the source snapshot has been verified, destructive fresh requires an interactive `/dev/tty` and one complete confirmation phrase bound to the operation, source and target refs/commits, revalidated absolute database path, and snapshot id; there is no `--yes`, `--no-backup`, or non-interactive bypass. A failure before the local commit point must restore the complete source state while retaining both the source snapshot and old release. Once local health has passed and the target is committed, a later public proxy/DNS/TLS check failure keeps the new service and source snapshot in place for diagnosis instead of automatically rolling back. If you do not have a separately reviewed compatible helper, use the manual procedure below only for a same-generation disposable/staging deployment or a verified compatible Generation 2 database; it is not a substitute for a destructive fresh cutover.

A destructive fresh cutover deletes the active database set and therefore removes users, sessions, OAuth state, CallerKeys; endpoints, upstream secrets, physical keys, models and bindings; charity resources, donations, reviews and routing state; spendable `credits`, cumulative `donation_credit`, levels, check-ins, usage totals and per-user limits; request/usage logs, audits, alerts, activity and lifecycle records; display, OAuth-gate, anti-abuse, charity, economy, timezone, maintenance, registration and legal settings in `site_config`; Debug session state; and game data from the new active instance. The protected source snapshot remains sensitive and may still contain all of that data. It is not an account export and must be protected together with the original release, environment/configuration, master key, unit, manifest, and checksums.

## Manual same-generation deployment procedure

1. Verify the release source and build on a separate Linux/amd64 build host. Confirm that the target binary and current database are both Generation 2; this procedure never converts an earlier database.
2. Run the Go, race, frontend, and release-like embed gates.
3. Copy the new binary to a new versioned release directory; do not overwrite the active binary in place.
4. Stop the service before backing up or replacing the database-adjacent files:

   ```sh
   sudo systemctl stop nonbiriapi.service
   ```

5. Create and verify a complete protected snapshot before changing the active release. It must keep the exact previous release, environment/configuration, master key, systemd unit, database and existing sidecars together with a manifest and checksums. The following command is only the database component of that complete snapshot; when the service is stopped, copy the main file plus existing `-wal` and `-shm` files, or use a tested SQLite backup tool:

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

6. Point `/opt/nonbiriapi/current` at the new compatible release and start the service:

   ```sh
   version=1.0.0-beta.1  # replace with the compatible release being installed
   sudo ln -sfn "/opt/nonbiriapi/releases/$version" /opt/nonbiriapi/current.next
   sudo mv -Tf /opt/nonbiriapi/current.next /opt/nonbiriapi/current
   sudo systemctl start nonbiriapi.service
   sudo systemctl status nonbiriapi.service
   ```

7. Verify both configured hosts through the reverse proxy, then inspect the journal for startup errors. Confirm the user station, admin station, login boundary, and `/healthz` response. Do not treat a running process alone as a successful deployment.

If the binary fails to start, stop it. Reverting only the release symlink is allowed only when the previous binary is explicitly compatible with the unchanged database generation. Otherwise restore the complete pre-change snapshot — release, database/sidecars, environment, master key, and unit — before starting the old service. Never delete a database or rotate the master key as an improvised rollback.

## Backup and restore test

A backup is not complete until it has been restored in an isolated directory with the same master key and opened by a test instance. Test this before onboarding users and periodically thereafter. Never test restoration against the production database path.

## Cutting over from an earlier prerelease

There is no in-place path. With a separately reviewed compatible helper and explicit authorization for the destructive operation, use its destructive-fresh flow: select and pin the trusted target; stop the service; create and verify a complete source snapshot; enter the complete target-bound confirmation phrase from `/dev/tty`; remove only the exact revalidated active database/WAL/SHM; start the target against an absent database path; verify the fresh safety gates; then reapply approved instance legal text and required configuration. Do not copy tables, rows, encrypted credentials, site configuration, or legal overrides into Generation 2 by hand.

If the cutover fails before the target passes local health and reaches its recorded commit point, restore the complete source snapshot and old release. After that commit point, a failure limited to public proxy, DNS, or TLS checks keeps the new service and source snapshot in place and reports the external fault; it does not automatically roll back the database. If the source snapshot is missing, corrupt, incompatible, or has been cleaned up, stateful rollback is unavailable: the safe choices are to keep the current compatible deployment, repair/restore from another verified complete snapshot, or perform a separately confirmed destructive fresh deployment. The helper must never offer a binary-only downgrade or fabricate a database for the old version.

## Candidate limitations

- Beta.1 is fresh-only Generation 2. A current database is validated, not bootstrapped or migrated; a completely absent main/WAL/SHM path set permits fresh creation, while every unsupported existing state is rejected without repair.
- The release target is Linux/amd64 and the release process is source-first.
- SMTP settings are reserved and do not send alert email in the current prereleases.
- Real Discord OAuth and upstream success flows must be tested with disposable staging credentials before public operation.
- Choose a maintenance window for every update and keep a known-good binary, environment backup, and database backup together.
