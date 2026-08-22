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
| `NONBIRI_SMTP_*` | no | parsed/reserved only; the alpha releases do not send alert email |

If registration is enabled, an OAuth scope override must retain both identity and guild-member access; `identify` alone cannot satisfy the guild/role registration gate.

The master key is not a normal setting. Back it up securely, keep its Unix file mode owner-only (`0400` or `0600`), and never replace it for an existing database. Losing it makes encrypted upstream credentials unrecoverable. The environment file also contains secrets (administrator password and OAuth client secret, and possibly SMTP credentials), so keep it readable only by root and the dedicated service account.

The database directory and file hold encrypted upstream credentials and private account metadata. On Unix, the application enforces owner-only access at startup: the database parent directory must be owned by the service account with mode `0700` (no group or other access), and the database file together with its `-wal` and `-shm` sidecars are tightened to `0600` and verified. The parent directory is resolved to an absolute path, so the check also covers the current directory for a relative database path and root for a root-level path. If owner-only access cannot be guaranteed — a group/other-accessible directory, a wrong owner, a symlink or non-regular file at the database path or its `-wal`/`-shm` sidecars (rejected before the database is opened), or a failed permission check — the process refuses to start. A pre-existing world-readable database file is tightened to `0600`; startup no longer depends on the operator setting `umask`. SQLite creates the `-wal`/`-shm` sidecars with the same mode as the database file, so once the database is `0600` later sidecar creation is owner-only too. On Windows, POSIX permission bits are not synthesized or checked; run the service under a dedicated account and configure an ACL that grants access only to that account and administrators, and verify the ACL in a supported test environment.

## Runtime administrator settings

The administrator station exposes the following authoritative keys. Unknown keys are rejected; `alert_prefs_*` is the only bounded namespace. Values shown below describe the local, unpublished alpha.2 release candidate; alpha.2-only keys are unavailable to an unupgraded alpha.1 deployment.

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
| `maintenance_mode` | boolean | `false`; **changed from alpha.1 in the alpha.2 candidate**: now a server-side authoritative admission gate — while on, every user-station `/api/*` and `/v1/*` request is refused with a stable `503 service_unavailable` envelope (`source: platform`, `no-store`) except a strict allowlist (`/healthz`, the public config endpoints, and `/api/auth/logout`); already-issued user sessions and caller keys are affected immediately; the admin station is never gated; atomically live-applied and loaded from the database at startup. Route matrix in `docs/api-contract.md` §6 |
| `charity_enabled` | boolean | `false`; charity system master switch — while off, no new charity routing happens and the price table is hidden; in-flight reservations still settle |
| `donation_accept_enabled` | boolean | `false`; gates new donation submissions only; review/routing of existing donations is unaffected |
| `charity_token_reserve_milli` | nullable canonical positive decimal milli-credit string | **null (default) = not configured** — distinct from an explicit value; while unset, per-token charity models cannot be enabled or routed (fail closed); PATCH rejects `null` and non-positive values |
| `rpm_ban_threshold` | integer `[0,4096]` | `5`; count of effective per-user RPM denials before an automatic 24-hour ban; `0` disables |
| `rpm_ban_window_seconds` | integer `[1,316224000]` | `86400`; in-memory RPM violation window |
| `rpm_ban_duration_seconds` | integer `[1,316224000]` | `86400`; automatic ban duration |
| `charity_min_chars` | integer `[0,1048576]` | `20`; counted Unicode message runes before a charity request is dispatched; `0` disables |
| `charity_violation_deduct_milli` | canonical non-negative decimal string | `"0"`; per-violation penalty, allowed to make credits negative |
| `charity_violation_ban_seconds` | integer `[0,316224000]` | `0`; single-violation automatic ban duration |
| `charity_violation_window_seconds` | integer `[1,316224000]` | `86400`; in-memory short-content window |
| `charity_violation_ban_threshold` | integer `[0,4096]` | `0`; violations needed for a window ban |
| `charity_violation_window_ban_seconds` | integer `[0,316224000]` | `0`; window-ban duration |
| `charity_suspend_window_seconds` | integer `[1,316224000]` | `86400`; in-memory charity-suspension window |
| `charity_suspend_threshold` | integer `[0,4096]` | `0`; violations needed for a charity-only suspension |
| `charity_suspend_duration_seconds` | integer `[0,316224000]` | `0`; charity-only suspension duration |
| `registration_open` | boolean | `true`; when false, new registration is refused while existing accounts may sign in |
| `site_timezone_offset_minutes` | nullable integer; multiple of 30 in `[-720,+840]` | **null (default) = not configured** — distinct from an explicit `0` (UTC). While unset, site-day-key features (check-in, activity) are force-disabled for normal users behind the ordinary feature-disabled error without revealing why. Once any check-in or activity row exists, the value is frozen: every further write is refused with `conflict`, even rewriting the identical value or after those rows are later cleaned up. The offset is read authoritatively per business transaction; there is no runtime singleton |
| `level_threshold_2_milli` | canonical non-negative decimal milli-credit string | `"0"`; donation-credit threshold for automatic promotion to level 2; `"0"` disables that level's promotion; enabled thresholds must be strictly increasing in level order and are cross-validated in the same transaction as the write |
| `level_threshold_3_milli` | canonical non-negative decimal milli-credit string | `"0"`; same rules as `level_threshold_2_milli`, for level 3; level 3 also bypasses the check-in credits cap |
| `level_threshold_4_milli` | canonical non-negative decimal milli-credit string | `"0"`; same rules as `level_threshold_2_milli`, for level 4; level 5 steward access remains manual-only |
| `checkin_mode` | enum: `enabled` / `level_gated` / `disabled` | `disabled`; `level_gated` admits only effective level ≥3; an unset timezone or any other unavailable cause returns the identical feature-disabled error |
| `checkin_award_min_milli` | canonical non-negative decimal milli-credit string | `"40000000"`; inclusive lower bound of the uniformly drawn daily award; cross-validated `min ≤ max` against `checkin_award_max_milli` in one transaction |
| `checkin_award_max_milli` | canonical non-negative decimal milli-credit string | `"60000000"`; inclusive upper bound of the daily award |
| `credits_cap_milli` | canonical non-negative decimal milli-credit string | `"250000000"`; check-in admission threshold: a user below level 3 whose balance has reached the cap is refused (`checkin_cap_reached`) without consuming the day; `"0"` disables the threshold; level ≥3 always bypasses; it is never a truncation of awards |
| `alert_prefs_*` | single-line text, ≤512 bytes | administrator alert-center preferences |

### Registration gate

A brand-new account is accepted only when all of these are true:

1. `registration_open` is `true`;
2. both `discord_guild_id` and `discord_role_id` are non-empty;
3. Discord confirms membership in that guild and possession of that role.

If either ID is blank, new registration is paused; blank does **not** mean “skip that check.” Existing registered users can still sign in. Normal users may update only their interface language through `PATCH /api/me`; per-user RPM and endpoint limits are administrator-controlled.

### Runtime application

RPM and concurrency settings update the shared in-process controllers immediately. Resource-count caps are read transactionally on each create. Display, legal, registration, OAuth-admission, and maintenance values are read from the database without rebuilding; responses are `no-store`. The site timezone offset carries no runtime singleton: consumers resolve it per transaction and fail closed when it is unset.

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
