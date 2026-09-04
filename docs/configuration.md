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
| `NONBIRI_SMTP_*` | no | parsed/reserved only; the current prereleases do not send alert email |

If registration is enabled, an OAuth scope override must retain both identity and guild-member access; `identify` alone cannot satisfy the guild/role registration gate.

The master key is not a normal setting. Back it up securely, keep its Unix file mode owner-only (`0400` or `0600`), and never replace it for an existing database. Losing it makes encrypted upstream credentials unrecoverable. The environment file also contains secrets (administrator password and OAuth client secret, and possibly SMTP credentials), so keep it readable only by root and the dedicated service account.

The database directory and file hold encrypted upstream credentials and private account metadata. On Unix, the application enforces owner-only access at startup: the database parent directory must be owned by the service account with mode `0700` (no group or other access), and an existing database file together with existing `-wal` and `-shm` sidecars must already be owner-owned regular files with mode `0600`. The parent directory is resolved to an absolute path, so the check also covers the current directory for a relative database path and root for a root-level path. A permissive, wrong-owner, symlink, non-regular, or otherwise anomalous existing file is rejected before the protected validation snapshot or writable open; the candidate does not chmod a rejected source as a side effect. Fresh files are created and verified as `0600`, and SQLite sidecars inherit that owner-only mode. On Windows, POSIX permission bits are not synthesized or checked; run the service under a dedicated account and configure an ACL that grants access only to that account and administrators, and verify the ACL in a supported test environment.

## Runtime administrator settings

The administrator station exposes the following authoritative keys. Unknown keys are rejected; `alert_prefs_*` is the only bounded namespace. Values below describe the unreleased `v1.0.0-beta.1` candidate. A fresh Generation 2 database explicitly seeds maintenance on and registration, activities, charity, donation intake, and all games off; these safety seeds take precedence over generic code fallbacks.

| Key | Type / range | Default and effect |
| --- | --- | --- |
| `site_name` | non-empty text, ≤256 bytes | display name; frontend falls back when unset |
| `site_logo_url` | text, ≤2048 bytes, blank allowed | display image URL; blank uses the default mark |
| `announcement_epoch` | read-only opaque ID | generated for each fresh database and used to isolate announcement caches |
| `legal_privacy_override_zh`, `legal_privacy_override_en` | multiline text, ≤65536 bytes each | blank uses the embedded privacy template |
| `legal_terms_override_zh`, `legal_terms_override_en` | multiline text, ≤65536 bytes each | blank uses the embedded terms template |
| `legal_authoritative_locale` | `zh`, `en`, or blank | declares the controlling legal-language version |
| `charity_donation_notice_zh`, `charity_donation_notice_en` | multiline text, ≤8192 bytes each | blank uses the embedded donation guidance shown beside the user submission form |
| `default_endpoint_limit` | integer `[0,10000]` | 50; used only when a user has no explicit endpoint limit; `0` prevents new endpoint creation for those users |
| `default_endpoint_key_limit` | integer `[1,10000]` | 20 per endpoint |
| `default_model_limit` | integer `[1,10000]` | 100 per user |
| `default_binding_limit` | integer `[1,10000]` | 50 per model |
| `default_rpm_per_user` | integer `[1,4096]` | 60; administrator-controlled default |
| `global_rpm` | integer `[1,4096]` | 600 across the process |
| `default_per_endpoint_concurrency` | integer `[1,100000]` | 8 per normalized base URL |
| `egress_global_concurrency` | integer `[1,100000]` | 32 across all outbound requests |
| `anthropic_default_max_tokens` | nullable integer `[1,2147483647]` | raw `null`; effective fallback 65536 when an Anthropic attempt receives neither token-limit field; it is not a maximum for explicit values |
| `discord_guild_id` | text, ≤128 bytes, blank allowed | required together with `discord_role_id` for new registration |
| `discord_role_id` | text, ≤128 bytes, blank allowed | required together with `discord_guild_id` for new registration |
| `oauth_start_rate_limit` | integer `[0,1000]` | 10 starts per client IP; `0` disables the in-process layer |
| `oauth_start_rate_window_seconds` | integer `[1,3600]` | 60-second window |
| `oauth_start_rate_penalty_seconds` | integer `[0,3600]` | 60-second penalty; `0` disables the penalty duration |
| `maintenance_mode` | boolean | fresh Generation 2 seed `true`; server-side authoritative admission gate — while on, ordinary user-station `/api/*` and `/v1/*` admission is refused with `503 maintenance` except the documented bootstrap/logout and accepted-continuation routes; already-issued credentials are affected immediately; the admin station remains available. Route matrix in [api-contract.md](api-contract.md#10-maintenance-recovery-and-retention) |
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
| `registration_open` | boolean | fresh Generation 2 seed `false`; when false, new registration is refused while existing accounts may sign in |
| `site_timezone_offset_minutes` | nullable integer; multiple of 30 in `[-720,+840]` | **null (default) = not configured** — distinct from an explicit `0` (UTC). While unset, site-day-key features (check-in, activity) are force-disabled for normal users behind the ordinary feature-disabled error without revealing why. Once any check-in or activity row exists, the value is frozen: every further write is refused with `conflict`, even rewriting the identical value or after those rows are later cleaned up. The offset is read authoritatively per business transaction; there is no runtime singleton |
| `level_threshold_2_milli` | canonical non-negative decimal milli-credit string | `"0"`; donation-credit threshold for automatic promotion to level 2; `"0"` disables that level's promotion; enabled thresholds must be strictly increasing in level order and are cross-validated in the same transaction as the write |
| `level_threshold_3_milli` | canonical non-negative decimal milli-credit string | `"0"`; same rules as `level_threshold_2_milli`, for level 3; level 3 also bypasses the check-in credits cap |
| `level_threshold_4_milli` | canonical non-negative decimal milli-credit string | `"0"`; same rules as `level_threshold_2_milli`, for level 4; level 5 steward access remains manual-only |
| `checkin_mode` | enum: `enabled` / `level_gated` / `disabled` | `disabled`; `level_gated` admits only effective level ≥3; an unset timezone or any other unavailable cause returns the identical feature-disabled error |
| `checkin_award_min_milli` | canonical non-negative decimal milli-credit string | `"40000000"`; inclusive lower bound of the uniformly drawn daily award; cross-validated `min ≤ max` against `checkin_award_max_milli` in one transaction |
| `checkin_award_max_milli` | canonical non-negative decimal milli-credit string | `"60000000"`; inclusive upper bound of the daily award |
| `credits_cap_milli` | canonical non-negative decimal milli-credit string | `"250000000"`; check-in admission threshold: a user below level 3 whose balance has reached the cap is refused (`checkin_cap_reached`) without consuming the day; `"0"` disables the threshold; level ≥3 always bypasses; it is never a truncation of awards |
| `level_display_name_1` … `level_display_name_5` | text, ≤64 runes, blank allowed | built-in `Lv. 1` … `Lv. 5`; blank restores the built-in display name |
| `activities_enabled` | boolean | `false`; master admission switch for new activity operations |
| `activity_welfare_enabled` | boolean | `false`; enables daily welfare only when the site timezone and positive threshold/cap compile successfully |
| `activity_welfare_threshold_milli`, `activity_welfare_cap_milli` | canonical non-negative decimal strings | `"0"`; both must be positive before welfare can be enabled |
| `activity_thursday_enabled` | boolean | `false`; enables new Thursday contributions only when a complete next-period configuration exists |
| `games_enabled` | boolean | fresh seed `false`; master gate for starting new game rounds; existing paid rounds can still settle |
| `game_fishing_enabled` | boolean | fresh seed `false`; Fishing start gate below the game master gate |
| `game_fishing_bait_worm_price_milli` | canonical positive decimal string | `"2500000"`; exact Fishing entry price |
| `game_fishing_bait_lure_price_milli` | canonical positive decimal string | `"5000000"`; exact Fishing entry price |
| `game_fishing_bait_premium_price_milli` | canonical positive decimal string | `"7500000"`; exact Fishing entry price |
| `game_fishing_rtp` | integer `[0,100]` | 90; worm/lure target RTP, accepted only when the complete Fishing economy validates |
| `game_fishing_rtp_premium` | integer `[0,100]` | 88; premium target RTP, subject to the same whole-economy validation |
| `game_fishing_treasure_bottle_mult` | integer `[1,1000]` | 2 |
| `game_fishing_treasure_clover_mult` | integer `[1,1000]` | 3 |
| `game_fishing_treasure_shell_mult` | integer `[1,1000]` | 5 |
| `game_linklink_enabled` | boolean | `false`; LinkLink start gate below the game master switch |
| `game_linklink_{6x8,8x8,10x10}_enabled` | boolean | `false`; independent board-size start gates |
| `game_linklink_{6x8,8x8,10x10}_price_milli` | canonical non-negative decimal strings | `"0"`; an enabled board size requires a positive price; time limits are fixed at 150/180/240 seconds |
| `game_rps_enabled` | boolean | `false`; three-player Rock Paper Scissors queue gate below the game master switch |
| `game_rps_{quick,standard,deathmatch}_enabled` | boolean | `false`; independent mode queue gates |
| `game_rps_{quick,standard,deathmatch}_b_milli` | canonical non-negative decimal strings | `"0"`; an enabled mode requires a positive base amount |
| `game_rps_{mode}_{platform,welfare,thursday}_bp` | integer `[0,9999]` | 100 basis points for each cut; the three values are compiled with the complete mode economy |
| `game_rps_{mode}_queue_seconds` | integer `[30,120]` | 120 |
| `game_rps_{mode}_gesture_seconds` | integer `[5,20]` | 20 |
| `game_rps_{mode}_{dealer,follower}_seconds` | integer `[5,15]` | 15 |
| `report_pending_ttl_seconds` | integer `[1,259200]` | 86400; pending report lifetime before expiry |
| `alert_prefs_*` | single-line text, ≤512 bytes | administrator alert-center preferences |

Game and activity keys cannot be changed through the generic single-key PATCH route. `GET/PATCH /admin/api/games/config` and `GET/PATCH /admin/api/activities/config` read or atomically update complete typed snapshots; the server merges a partial request, validates the entire resulting configuration, and writes all touched rows or none. `maintenance_mode` uses its dedicated maintenance routes, and `announcement_epoch` is read-only.

### Configuration catalog

`GET /admin/api/site-config/catalog` is the admin-only machine-readable authority for every known key. Entries are sorted by key and include bilingual title/description/unit, value type, stored/default semantics, effective fallback, hard minimum/maximum, allowed values, null/zero/empty semantics, independent gates, and the correct write endpoint. `GET /admin/api/site-config` returns the typed editor projection: required keys use their documented effective default when no valid row exists, while nullable keys preserve their absence as JSON `null`. In particular, an unset `anthropic_default_max_tokens` remains `null`; its effective 65536 fallback is described by the catalog and applied only when compiling an Anthropic attempt.

`PATCH /admin/api/site-config/{key}` accepts `{ "value": ... }`. JSON `null` is writable only for `anthropic_default_max_tokens`, where it deletes the explicit override and restores the built-in 65536 fallback. Specialized maintenance, announcement, activity, and game keys return `conflict` from this generic route and must use their dedicated endpoint.

The four `legal_*_override_*` fields preserve accepted UTF-8 bytes, including LF/CRLF, tabs, and multibyte characters, up to 65,536 bytes. The two donation notices use the same byte-preserving multiline behavior with an 8,192-byte bound. Before opening an instance, save all owner-approved texts, reload the settings page, read them back, and re-save once to prove the full round trip is lossless; then verify the anonymous privacy/terms pages, the user donation form, and `legal_authoritative_locale`. A fresh database does not import prior overrides automatically.

### Per-user limit overrides

The administrator user APIs expose both nullable raw values and their current fallback projections:

```json
{
  "endpoint_limit": null,
  "effective_endpoint_limit": 50,
  "rpm_limit": null,
  "effective_rpm_limit": 60,
  "concurrency_limit": null,
  "effective_concurrency_limit": 5
}
```

`PATCH /admin/api/users/{id}` accepts `endpoint_limit` (`0..10000`), `rpm_limit` (`1..4096`), and `concurrency_limit` (`1..100000`) in profile mode. An absent field is unchanged and JSON `null` restores its fallback. An explicit override may be lower, equal to, or higher than the corresponding default; defaults are not clamps. The global RPM and egress gates remain independent. A concurrency value of `0` is invalid and never means unlimited.

Donation-key charity limits use a different explicit contract: `max_concurrency` is `[0,100000]`, `rpm_limit` is `[0,4096]`, and `0` means unlimited without falling back to a site, user, or endpoint default. Donation creation/replacement normalizes omitted or JSON-null fields to zero. In a reviewer/admin/level-5 partial update, omission or null means unchanged and an explicit zero removes the limit.

### Registration gate

A brand-new account is accepted only when all of these are true:

1. `registration_open` is `true`;
2. both `discord_guild_id` and `discord_role_id` are non-empty;
3. Discord confirms membership in that guild and possession of that role.

If either ID is blank, new registration is paused; blank does **not** mean “skip that check.” Existing registered users can still sign in. Normal users may update their interface language and `game_profile_public` leaderboard preference through `PATCH /api/me`; endpoint, RPM, and concurrency limits remain administrator-controlled.

### Mainstream channel catalog

Mainstream channels are managed through `/admin/api/mainstream-channels`, not `site_config`. Each active entry fixes a display name, category (`subscription` or `api_platform`), Connector type, and canonical base URL. At most 100 active-and-enabled entries are allowed. User endpoint creation reads `/api/endpoint-create-options`; choosing a channel copies an immutable provenance snapshot into the endpoint in the final transaction. Disabling or retiring a channel affects future choices and never rewrites an existing endpoint. A retired channel cannot be restored.

### Runtime application

RPM and concurrency settings update the shared in-process controllers immediately. Per-user concurrency admission occurs before RPM reservation; a concurrency refusal creates no RPM hit or automatic penalty. Resource-count caps are read transactionally on each create. Display, legal, donation guidance, registration, OAuth admission, maintenance, Connector defaults, activities, reports, and game values are read from the database without rebuilding; responses are `no-store`. The site timezone offset carries no runtime singleton: consumers resolve it per transaction and fail closed when it is unset.

`GET /api/config` exposes only the display/legal values, the two donation notices, `maintenance_mode`, `registration_open`, and `announcement_epoch`. The admin-host bootstrap endpoint exposes the same safe projection after administrator authentication. Neither exposes operational rate limits, Discord gate IDs, alert preferences, or secrets.

## What requires a rebuild or code change

The following are not arbitrary runtime customization:

- connector registry and protocol behavior;
- retention policy, security bounds, and data-lifecycle behavior;
- route/API contract changes;
- frontend layout/components beyond the supported runtime branding and legal text.

Do not weaken authentication, ownership, egress, secret, stream, station, or no-store boundaries to customize a deployment.

## Private deployment data

A public repository does not need the real URL, DNS records, Discord invite, administrator host, user list, or deployment credentials. Keep them in operator-controlled deployment configuration. Public documentation should explain the mechanism without identifying a private instance.
