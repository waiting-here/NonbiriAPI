# First-run configuration preparation

This is the ordered walkthrough for preparing every configuration artifact a
production VPS needs **before the first boot**. It focuses on the environment
file, the master key, the Discord application, DNS, and the trusted-proxy list;
for the build, systemd unit, and reverse-proxy mechanics see
[deployment.md](deployment.md), and for the full variable reference see
[configuration.md](configuration.md).

The process reads its startup environment **only at boot**. The configuration loader accumulates its validated-field errors so a malformed security root never comes up silently; OS-level failures such as an unusable listen address or database path are reported when those resources are opened. Prepare the items below in order; the last section summarizes the loader checks.

## 0. Decide these first

Before touching the host, decide and record (privately, outside the repository):

- **Two hostnames.** The user station and the admin station are separate
  origins. Pick a public user origin such as `https://api.example.com` and a
  different admin host such as `admin.example.com`. They must differ.
- **A reverse proxy** that terminates public TLS and forwards to a loopback
  listener. The application is designed to sit behind one; its listen address
  defaults to `127.0.0.1:8080`.
- **A Discord application** for user sign-in (admin sign-in is a local
  username/password, not Discord). Create it in the Discord developer portal and
  note the client ID, client secret, and the redirect URI
  `https://<user-host>/api/auth/discord/callback`.
- **A long, unique administrator password.** It is the only admin credential;
  rotating it (env change + restart) revokes existing admin sessions, so pick one
  you can keep and store it in a password manager.

## 1. Create the service user and directories

Use the host's normal administration tools (full commands in
[deployment.md](deployment.md#prepare-the-host)):

```sh
sudo useradd --system --user-group --home /var/lib/nonbiriapi --shell /usr/sbin/nologin nonbiriapi
sudo install -d -o nonbiriapi -g nonbiriapi -m 0750 /var/lib/nonbiriapi
sudo install -d -o root   -g nonbiriapi -m 0750 /etc/nonbiriapi
```

The service account only needs to read `/etc/nonbiriapi/` and write
`/var/lib/nonbiriapi/`.

## 2. Generate the master key (do this exactly once)

The master key is the AES-256 encryption root for every upstream credential the
database stores. It is **not** a normal setting:

- Generate it once, before the first database is created.
- **Never regenerate it for an existing database** — a new key makes every
  encrypted upstream credential unrecoverable.
- Back it up somewhere offline and protected; losing it is equivalent to losing
  every user's upstream keys.

The key must decode to exactly **32 bytes**. The loader accepts 64-character
hex, or standard / URL-safe base64 (padded or unpadded). A file source may also
contain exactly 32 raw bytes. The simplest correct form is 64 hex characters:

```sh
sudo install -o nonbiriapi -g nonbiriapi -m 0600 /dev/null /etc/nonbiriapi/master.key
openssl rand -hex 32 | sudo -u nonbiriapi tee /etc/nonbiriapi/master.key >/dev/null
sudo chmod 0600 /etc/nonbiriapi/master.key
```

The key file must be owned by the service account and owner-only: Unix mode `0400` or `0600` is accepted (`0600` is used during generation). The loader deliberately rejects group-readable modes such as `0640`, so the service account must own the file to read it without broadening access. Run the truncating
`install` line only on a brand-new installation.

## 3. Prepare the environment file

Copy [`admin.env.example`](../admin.env.example) to `/etc/nonbiriapi/admin.env`,
then replace every `CHANGE_ME` placeholder and review each optional value. Then restrict it:

```sh
sudo chown root:nonbiriapi /etc/nonbiriapi/admin.env
sudo chmod 0640 /etc/nonbiriapi/admin.env
```

The environment file contains secrets, including the administrator password and Discord client secret (and possibly SMTP credentials). The example `0640 root:nonbiriapi` layout is acceptable only when `nonbiriapi` is a dedicated group with no unrelated members: root owns the file and the service account needs group-read access. Use an equivalently restrictive owner-only layout if your service manager supports it. Walk through each variable:

### Listener and storage

| Variable | Required | Value |
| --- | --- | --- |
| `NONBIRI_LISTEN_ADDR` | no | `127.0.0.1:8080` behind a reverse proxy. Keep it on loopback. |
| `NONBIRI_DB_PATH` | no | `/var/lib/nonbiriapi/nonbiriapi.db`. The service account must write the directory. |
| `NONBIRI_LOG_LEVEL` | no | `info` for production; `debug` only when diagnosing. |

The application creates a missing database directory owner-only. If you launch
it manually outside systemd, run `umask 077` first so SQLite sidecar files stay
owner-only.

### Administrator credential

| Variable | Required | Value |
| --- | --- | --- |
| `NONBIRI_ADMIN_USERNAME` | yes | 1–128 bytes. Default `admin` is fine; it is not a secret. |
| `NONBIRI_ADMIN_PASSWORD` | yes | 1–4096 bytes. Use a long random password; do not reuse it elsewhere. |

Rotating the password (change the env value and restart) revokes every existing
admin session at its next request. A plain restart with the **same** password
keeps existing admin sessions, so routine deploys do not log the admin out.

### Master key source

Set **exactly one** of:

| Variable | Required | Value |
| --- | --- | --- |
| `NONBIRI_MASTER_KEY_FILE` | one of the two | `/etc/nonbiriapi/master.key` (preferred; the key never appears in the process environment). |
| `NONBIRI_MASTER_KEY` | one of the two | The 64-hex / base64 string inline. Avoid in production; the file form keeps the key out of `ps`/journal snapshots. |

Setting both is a startup error.

### Discord OAuth

| Variable | Required | Value |
| --- | --- | --- |
| `NONBIRI_DISCORD_CLIENT_ID` | yes | 1–512 bytes, from the Discord developer portal. |
| `NONBIRI_DISCORD_CLIENT_SECRET` | yes | 1–4096 bytes, from the portal. |
| `NONBIRI_DISCORD_OAUTH_SCOPES` | no | Leave unset to use `identify guilds.members.read`. If overridden while registration is enabled, retain both scopes; `identify` alone cannot check guild membership. |

In the Discord application settings, set the OAuth redirect URI to
`https://<user-host>/api/auth/discord/callback` (the user station, not the admin
host). Registration is gated by a configured guild and role, which you set
later in the admin site configuration (see [configuration.md](configuration.md)).

### Public origins

| Variable | Required | Value |
| --- | --- | --- |
| `NONBIRI_SITE_BASE_URL` | yes | The user station origin, e.g. `https://api.example.com`. Scheme must be `http` or `https`; no userinfo, path, query, or fragment; a trailing slash is stripped. |
| `NONBIRI_ADMIN_HOST` | no | The admin station host, e.g. `admin.example.com`. If unset it is derived as `admin.` + the user host. It must differ from the user host. An IPv6 user host requires an explicit admin host. A `:port` is allowed. |

These are origins/hostnames, never full paths. The two stations are
deliberately separate so the admin surface is independently restrictable.

### Trusted proxy addresses

| Variable | Required | Value |
| --- | --- | --- |
| `NONBIRI_TRUSTED_PROXY_CIDRS` | no | Comma-separated CIDRs allowed to supply `X-Forwarded-*` metadata. Defaults to `127.0.0.0/8,::1/128`. Bare IPs are accepted; `none` disables forwarding entirely. |

**Set this to only your reverse proxy's address.** The application ignores
forwarding headers from any other source, and malformed/duplicate headers are
discarded wholesale in favor of the direct peer. Listing a broader range lets a
caller forge its apparent IP and defeat the per-IP OAuth admission throttle. If
the proxy runs on the same host, the loopback default is correct; if it is on a
separate private address, put that address here.

### SMTP (reserved, leave off in alpha)

The `NONBIRI_SMTP_*` variables are parsed and validated only when
`NONBIRI_SMTP_HOST` is set; alpha does not send alert email. Leave them all
commented out for a first run. If you set them later: `NONBIRI_SMTP_PORT` is an
integer in `[1,65535]` (default 587), `NONBIRI_SMTP_TLS` is `starttls` or
`implicit` (or unset), and `NONBIRI_SMTP_FROM` defaults to `NONBIRI_SMTP_USER`.

## 4. Permissions checklist

Before the first boot, confirm:

- `/etc/nonbiriapi/master.key` — `0400` or `0600`, owned by `nonbiriapi:nonbiriapi`.
- `/etc/nonbiriapi/admin.env` — `0640`, owned by `root:nonbiriapi`, with `nonbiriapi` a dedicated group containing no unrelated accounts.
- `/var/lib/nonbiriapi` — `0750`, owned by `nonbiriapi:nonbiriapi` (writable by the service).
- No secret appears in the repository, in a backup that is not key-grade, or in a
  shell history file. Treat the database and backups as carefully as the master
  key — they contain encrypted upstream credentials and private account data.

## 5. What the configuration loader validates

The loader collects the validation problems below and reports them together. Resource-opening failures (for example, an invalid/unavailable listen address or unwritable database directory) are separate startup errors. The configuration load fails if any of these is true:

- No master key, or both `NONBIRI_MASTER_KEY` and `NONBIRI_MASTER_KEY_FILE` set,
  or the value does not decode to exactly 32 bytes; on Unix the file must also be a stable owner-only regular file (no final symlink, mode `0400`/`0600`).
- `NONBIRI_ADMIN_USERNAME`, `NONBIRI_ADMIN_PASSWORD`,
  `NONBIRI_DISCORD_CLIENT_ID`, or `NONBIRI_DISCORD_CLIENT_SECRET` is empty, over its bound, invalid UTF-8, or contains a control character; a scope override is also bounded/control-free.
- `NONBIRI_SITE_BASE_URL` missing, or not an `http`/`https` origin (userinfo,
  path, query, or fragment present).
- The admin host equals the user host, or is not a valid hostname.
- `NONBIRI_TRUSTED_PROXY_CIDRS` malformed (use `none` to disable cleanly).
- Any `NONBIRI_SMTP_*` field malformed when `NONBIRI_SMTP_HOST` is set.

The error message names each offending variable; it never prints secret
material.

## 6. First-boot smoke test

After the systemd unit is installed and started (see
[deployment.md](deployment.md#example-systemd-unit)):

```sh
sudo systemctl status nonbiriapi.service
sudo journalctl -u nonbiriapi.service -n 100 --no-pager
```

Then through the reverse proxy, confirm:

1. `GET https://<user-host>/healthz` returns a healthy response.
2. The user station loads and the Discord sign-in flow reaches the callback.
3. The admin station (`https://<admin-host>`) shows the admin login, and the
   configured username/password signs in.
4. After signing in to the admin station, set the Discord guild and role used by
   the registration gate (see [configuration.md](configuration.md)) before
   inviting the first user.

Do not treat a running process alone as a successful deployment. A clean
journal with both stations reachable and the login boundary enforced is the
minimum acceptance bar.

## 7. Do not

- Do not regenerate the master key for an existing database.
- Do not commit `admin.env`, `master.key`, or any real secret to the repository; the environment file itself is secret-bearing.
- Do not widen `NONBIRI_TRUSTED_PROXY_CIDRS` beyond the reverse proxy.
- Do not bind the listener to a public interface when a reverse proxy is in
  front.
- Do not weaken the authentication, ownership, egress, secret, stream, or
  no-store boundaries to customize a deployment; customize via source and
  rebuild instead.
