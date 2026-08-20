# Configuration reference

NonbiriAPI separates **startup security roots** from **runtime site settings**. Startup values are read only when the process starts; `site_config` values are administrator-controlled and applied without rebuilding the binary.

## Startup environment

Copy [admin.env.example](../admin.env.example) to a private path outside the checkout and replace every placeholder. The example uses production-style `/etc` and `/var` paths; change them for local builds.

| Variable | Required | Purpose |
| --- | --- | --- |
| `NONBIRI_LISTEN_ADDR` | no | HTTP listener; blank/unset defaults to `127.0.0.1:8080` |
| `NONBIRI_DB_PATH` | no | SQLite path; blank/unset defaults to `nonbiriapi.db` |
| `NONBIRI_LOG_LEVEL` | no | `debug`, `info`, `warn`, or `error` |
| `NONBIRI_ADMIN_USERNAME` | yes | single administrator username |
| `NONBIRI_ADMIN_PASSWORD` | yes | single administrator password; changing it and restarting revokes old admin sessions |
| `NONBIRI_MASTER_KEY_FILE` or `NONBIRI_MASTER_KEY` | exactly one | 32-byte encryption root; the file form is preferred for production |
| `NONBIRI_DISCORD_CLIENT_ID` | yes | Discord OAuth application ID |
| `NONBIRI_DISCORD_CLIENT_SECRET` | yes | Discord OAuth application secret |
| `NONBIRI_DISCORD_OAUTH_SCOPES` | no | provider scope override; default `identify guilds.members.read` |
| `NONBIRI_SITE_BASE_URL` | yes | canonical public origin of the user station |
| `NONBIRI_ADMIN_HOST` | no | separate admin host; blank/unset derives `admin.<user-host>` (explicit value required for IPv6 user hosts) |
| `NONBIRI_TRUSTED_PROXY_CIDRS` | no | peers allowed to supply forwarding metadata; defaults to loopback ranges; `none` disables trust |
| `NONBIRI_SMTP_*` | no | parsed/reserved only; alpha.1 does not send alert email |

If registration is enabled, an OAuth scope override must retain both identity and guild-member access; `identify` alone cannot satisfy the guild/role registration gate.

The master key is not a normal setting. Back it up securely, keep its Unix file mode owner-only (`0400` or `0600`), and never replace it for an existing database. Losing it makes encrypted upstream credentials unrecoverable. The environment file also contains secrets (administrator password and OAuth client secret, and possibly SMTP credentials), so keep it readable only by root and the dedicated service account.

## Runtime administrator settings

The administrator station exposes the following authoritative keys. Unknown keys are rejected; `alert_prefs_*` is the only bounded namespace. Values shown below are current alpha.1 behavior.

| Key | Type / range | Default and effect |
| --- | --- | --- |
| `site_name` | non-empty text, ≤256 bytes | display name; frontend falls back when unset |
| `site_logo_url` | text, ≤2048 bytes, blank allowed | display image URL; blank uses the default mark |
| `default_locale` | `zh` or `en` | frontend locale fallback |
| `legal_privacy_override_zh`, `legal_privacy_override_en` | multiline text, ≤65536 bytes each | blank uses the embedded privacy template |
| `legal_terms_override_zh`, `legal_terms_override_en` | multiline text, ≤65536 bytes each | blank uses the embedded terms template |
| `legal_authoritative_locale` | `zh`, `en`, or blank | declares the controlling legal-language version |
| `default_endpoint_limit` | integer `[0,10000]` | 50; `0` prevents new endpoint creation |
| `default_endpoint_key_limit` | integer `[1,10000]` | 20 per endpoint |
| `default_model_limit` | integer `[1,10000]` | 100 per user |
| `default_binding_limit` | integer `[1,10000]` | 50 per model |
| `default_rpm_per_user` | integer `[1,4096]` | 60; administrator-controlled default |
| `global_rpm` | integer `[1,4096]` | 600 across the process |
| `default_per_endpoint_concurrency` | integer `[1,100000]` | 8 per normalized base URL |
| `egress_global_concurrency` | integer `[1,100000]` | 32 across all outbound requests |
| `discord_guild_id` | text, ≤128 bytes, blank allowed | required together with `discord_role_id` for new registration |
| `discord_role_id` | text, ≤128 bytes, blank allowed | required together with `discord_guild_id` for new registration |
| `oauth_start_rate_limit` | integer `[0,1000]` | 10 starts per client IP; `0` disables the in-process layer |
| `oauth_start_rate_window_seconds` | integer `[1,3600]` | 60-second window |
| `oauth_start_rate_penalty_seconds` | integer `[0,3600]` | 60-second penalty; `0` disables the penalty duration |
| `maintenance_mode` | boolean | `false`; replaces the **user web application** with a maintenance notice, but does not server-disable `/v1/*` or authenticated APIs |
| `registration_open` | boolean | `true`; when false, new registration is refused while existing accounts may sign in |
| `alert_prefs_*` | single-line text, ≤512 bytes | administrator alert-center preferences |

### Registration gate

A brand-new account is accepted only when all of these are true:

1. `registration_open` is `true`;
2. both `discord_guild_id` and `discord_role_id` are non-empty;
3. Discord confirms membership in that guild and possession of that role.

If either ID is blank, new registration is paused; blank does **not** mean “skip that check.” Existing registered users can still sign in. Normal users may update only their interface language through `PATCH /api/me`; per-user RPM and endpoint limits are administrator-controlled.

### Runtime application

RPM and concurrency settings update the shared in-process controllers immediately. Resource-count caps are read transactionally on each create. Display, legal, registration, OAuth-admission, and maintenance values are read from the database without rebuilding; responses are `no-store`.

`GET /api/config` (and the admin-host bootstrap equivalent) exposes only the display/legal values plus `maintenance_mode` and `registration_open`. It never exposes operational rate limits, Discord gate IDs, alert preferences, or secrets.

## What requires a rebuild or code change

The following are not arbitrary runtime customization:

- connector registry and protocol behavior;
- retention policy, security bounds, and data-lifecycle behavior;
- route/API contract changes;
- frontend layout/components beyond the supported runtime branding and legal text.

Do not weaken authentication, ownership, egress, secret, stream, station, or no-store boundaries to customize a deployment.

## Private deployment data

A public repository does not need the real URL, DNS records, Discord invite, administrator host, user list, or deployment credentials. Keep them in operator-controlled deployment configuration. Public documentation should explain the mechanism without identifying a private instance.
