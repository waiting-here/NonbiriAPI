# Configuration reference

NonbiriAPI separates **startup security roots** from **runtime site settings**.

## Startup environment

Copy [admin.env.example](../admin.env.example) and replace its placeholders. The process reads these values only at startup:

| Variable | Required | Purpose |
| --- | --- | --- |
| `NONBIRI_LISTEN_ADDR` | no | HTTP listener; defaults to `127.0.0.1:8080` |
| `NONBIRI_DB_PATH` | no | SQLite path; defaults to `nonbiriapi.db` |
| `NONBIRI_LOG_LEVEL` | no | `debug`, `info`, `warn`, or `error` |
| `NONBIRI_ADMIN_USERNAME` | yes | administrator username |
| `NONBIRI_ADMIN_PASSWORD` | yes | administrator password |
| `NONBIRI_MASTER_KEY_FILE` or `NONBIRI_MASTER_KEY` | exactly one | 32-byte encryption root |
| `NONBIRI_DISCORD_CLIENT_ID` | yes | Discord OAuth application ID |
| `NONBIRI_DISCORD_CLIENT_SECRET` | yes | Discord OAuth application secret |
| `NONBIRI_DISCORD_OAUTH_SCOPES` | no | provider scope override; the default includes identity and guild-member lookup |
| `NONBIRI_SITE_BASE_URL` | yes | user station public origin |
| `NONBIRI_ADMIN_HOST` | no | separate admin host; otherwise derived from the user host |
| `NONBIRI_TRUSTED_PROXY_CIDRS` | no | addresses allowed to supply forwarding metadata |
| `NONBIRI_SMTP_*` | no | parsed/reserved only; alpha.1 does not send alert email |

The master key is not a normal setting. Back it up securely, restrict its permissions, and never replace it for an existing database. Losing it makes encrypted upstream credentials unrecoverable.

## Runtime administrator settings

After signing in to the admin station, the site configuration page can change the following bounded settings without rebuilding the binary:

- `site_name` and `default_locale`;
- `default_endpoint_limit`;
- `default_rpm_per_user` and `global_rpm`;
- `default_per_endpoint_concurrency` and `egress_global_concurrency`;
- `discord_guild_id` and `discord_role_id`.

The Discord registration gate is intentionally restrictive: a new account must satisfy both the configured guild and role. For a private-server trial, set both IDs to the trial server and role in the administrator site configuration. Keep the server and role identifiers private unless disclosure is necessary.

User accounts can change their language and requested per-user RPM limit within the administrator ceiling. The server remains authoritative and clamps or rejects unsafe values.

## What requires a rebuild

The following are currently source/build configuration rather than runtime settings:

- privacy and terms text;
- frontend branding beyond the bounded site name/locale settings;
- connector registry and protocol behavior;
- retention policy and security limits;
- route/API contract changes.

Customize these by changing the source, regenerating the frontend, and rebuilding the `-tags dist` binary. Do not weaken authentication, ownership, egress, secret, stream, or no-store boundaries merely to customize a deployment.

## Private trial deployment

A GitHub repository does not need to contain the URL, DNS records, Discord server invite, administrator host, user list, or any deployment credentials. Keep those in the operator's private deployment configuration. Public documentation should describe the configuration mechanism without identifying the private trial instance.
