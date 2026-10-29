# Configuration reference

NonbiriAPI separates **startup security roots** from **runtime site settings**. Startup values are read only when the process starts; `site_config` values are administrator-controlled and applied without rebuilding the binary.

## Model, game and review settings

Model APIs configure `role_policy` separately for each model. Native/empty rules preserve the connector's default behavior. Personal automation uses the existing CallerKey and requires no additional startup token or charge configuration.

Administrator risk-audit configuration adds `user_ip_window_hours` (integer 1–720, default 24) and `user_ip_min_ips` (2–1000, default 3), alongside existing thresholds/revision. It flags one Discord identity using at least that many trusted API addresses in a rolling window across old/current accounts. Shared-IP counts deduplicate Discord identities. Full stewards can read the settings; only administrators change them. Scans never impose a penalty.

Lake Notes is a permanent free minigame with four independently configured exchanges. Fresh installations start closed; an open legacy activity stays available after upgrade, with all exchanges initially disabled. See [game settings and API](lake-notes.md).

[Steady Catch](steady-catch.md) starts disabled with zero ticket and first-clear reward. [AI Gwent](ai-gwent.md) and its Standard mode also start disabled; the initial ticket is 5,000 credits, with 1% each for the platform, welfare pool and Thursday pool. Review these values in **Games** before enabling either game. Gwent uses the same prize-pool settlement as Bidding Duel.

Preserve the database's stable encrypted renewed-review matching material with the master key. Resetting it would bypass cross-account secret-text review requirements and is not a supported repair or configuration edit.

## Experimental management assistant

Assistant settings use their own revisioned configuration and encrypted driver
storage, rather than startup environment variables or the general `site_config`
catalog. An administrator configures Chat Completions or Responses API drivers,
the ChatGPT connection, or the separate [Codex device-code connection](codex-agent.md),
plus local context/output-reserve budgets, shared concurrency and timeout.
Codex is limited to administrator chat and individually assigned stewards; it
cannot be a global/steward default, automatic-review source or public model route.
Built-in [presets](ai-agent-presets.md) supply editable messages, initial
shared memories and tool descriptions without overwriting saved customization.

Chat defaults to closed; automatic review independently defaults to `off` and
offers `preview` and `auto` modes. Model calls require a ready driver.
Optional web search needs its own configured endpoint and encrypted key. Level-5
access uses the separate assistant grants in the [permission catalog](steward-permissions.md#actions-and-defaults).
Each actual model call joins shared FIFO scheduling and global RPM/concurrency
limits without personal RPM or credit charges. Waiting for tools or human plan
approval does not occupy a model-call slot.

Complete assistant request/response logs have no automatic expiry; an
administrator can clean an exact selection. Conversations, memories and token
totals remain independently retained, and clearing a conversation only resets
its active context. Disabling chat preserves those assets and leaves the
automatic-review mode independent. See [configuration fields, defaults and retention](api-contract.md#78-experimental-ai-assistant)
before enabling calls or publishing instance privacy text.

## Gateway cost attribution

The runtime administrator setting `gateway_user_attribution_enabled` is a boolean, off by default (site-config JSON `false|true`). Enabling it includes a server-generated user-and-gateway-origin pseudonym in subsequent Gateway v3/v4 text, image generation, embedding and rerank requests for cost attribution. Disabling it omits the tag. It can correlate requests from the same user; it is not anonymous and does not claim upstream safety processing. The caller cannot override it. Existing OpenAI and Anthropic settings remain independent. It uses the ordinary configuration catalog, administrator authorization, revision and audit rules; no startup environment variable or rebuild is required.

## Startup environment

Copy [admin.env.example](../admin.env.example) to a private path outside the checkout and replace every placeholder. The example uses production-style `/etc` and `/var` paths; change them for local builds.

| Variable | Required | Purpose |
| --- | --- | --- |
| `NONBIRI_LISTEN_ADDR` | no | HTTP listener; blank/unset defaults to `127.0.0.1:8080` |
| `NONBIRI_DB_PATH` | no | SQLite path; blank/unset defaults to `nonbiriapi.db` |
| `NONBIRI_LOG_LEVEL` | no | `debug`, `info`, `warn`, or `error` |
| `NONBIRI_STARTUP_TIMEOUT_SECONDS` | no | Whole integer seconds; default `300`, inclusive range `30`–`1800`. One total startup budget from process start through configuration, validation, database recovery, application initialization, and listener bind. |
| `NONBIRI_SHUTDOWN_TIMEOUT_SECONDS` | no | Whole integer seconds; default `30`, inclusive range `5`–`120`. One total graceful-shutdown budget; expiration results in a nonzero process exit. |
| `NONBIRI_ADMIN_USERNAME` | yes | single administrator username |
| `NONBIRI_ADMIN_PASSWORD` | yes | single administrator password; changing it and restarting revokes old admin sessions |
| `NONBIRI_MASTER_KEY_FILE` or `NONBIRI_MASTER_KEY` | exactly one | 32-byte encryption root; the file form is preferred for production |
| `NONBIRI_DISCORD_CLIENT_ID` | yes | Discord OAuth application ID |
| `NONBIRI_DISCORD_CLIENT_SECRET` | yes | Discord OAuth application secret |
| `NONBIRI_DISCORD_OAUTH_SCOPES` | no | provider scope override; default `identify guilds.members.read` |
| `NONBIRI_SITE_BASE_URL` | yes | canonical public origin of the user station |
| `NONBIRI_ADMIN_HOST` | no | separate admin host; blank/unset derives `admin.<user-host>` (explicit value required for IPv6 user hosts) |
| `NONBIRI_TRUSTED_PROXY_CIDRS` | no | peers allowed to supply forwarding metadata; defaults to loopback ranges; `none` disables trust |
| `NONBIRI_CLOUDFLARE_WORKER_URL` | no | Complete HTTPS generic Worker entry URL. Configure together with its token; see [Cloudflare forwarding](cloudflare-egress.md). |
| `NONBIRI_CLOUDFLARE_WORKER_TOKEN` | no | Private shared Worker token, 32–4096 visible ASCII characters without whitespace. Must match the Worker's `NONBIRI_PROXY_TOKEN` secret. |
| `NONBIRI_CLOUDFLARE_ROUTES` | no | Legacy exact endpoint-to-AI-Gateway mappings, used only without a Worker. Unset, blank or `[]` loads no mappings. |

The timeout variables accept integer seconds only; decimals and out-of-range values are rejected. Blank or unset values use the defaults. Budgets cover the whole process phase rather than restarting for each step. Opening the database alone does not mean the application is ready; readiness follows full initialization and listener binding. See the [API contract](api-contract.md) for `/readyz`.

An OAuth scope override must retain both identity and guild-member access for registration or sign-in without an exemption; `identify` alone cannot satisfy guild/role checks.

The master key is not a normal setting. Back it up securely, keep its Unix file mode owner-only (`0400` or `0600`), and never replace it for an existing database. Losing it makes encrypted upstream credentials unrecoverable. The environment file also contains the administrator password and OAuth client secret, so keep it readable only by root and the dedicated service account.

`NONBIRI_GATEWAY_MODEL_CAPABILITIES` and `NONBIRI_SMTP_HOST/PORT/USER/PASS/FROM/TLS`
are removed. The rc.7 converter imports eligible legacy Gateway settings before
these keys are removed from an existing environment file; database settings take
precedence. See [database upgrades](database-upgrades.md).

The database directory and file hold encrypted upstream credentials and private account metadata. On Unix, the application enforces owner-only access at startup: the database parent directory must be owned by the service account with mode `0700` (no group or other access), and an existing database file together with existing `-wal` and `-shm` sidecars must already be owner-owned regular files with mode `0600`. The parent directory is resolved to an absolute path, so the check also covers the current directory for a relative database path and root for a root-level path. A permissive, wrong-owner, symlink, non-regular, or otherwise anomalous existing file is rejected before the protected validation snapshot or writable open; the application does not chmod a rejected source as a side effect. Fresh files are created and verified as `0600`, and SQLite sidecars inherit that owner-only mode. On Windows, POSIX permission bits are not synthesized or checked; run the service under a dedicated account and configure an ACL that grants access only to that account and administrators, and verify the ACL in a supported test environment.

## Runtime administrator settings

### Account MFA

`mfa_required_level_1` through `mfa_required_level_6` are independent boolean
site-config settings, all false by default. They require accounts at the current
effective level to enroll and verify an authenticator before browser account
access. Users at any level may enroll voluntarily; turning a policy off does not
disable an existing enrollment. Verification lasts seven days per login session.
CallerKey model calls and existing CallerKey automation are unchanged. See the
[authentication contract](api-contract.md#account-authenticator-verification)
for enrollment, recovery and administrator reset.

### Loans, quick stakes and ranking privacy

`PATCH /admin/api/activities/config` extends the existing revision-controlled
configuration with `loan_enabled` (false), `loan_tiers` (three increasing integer
credit strings, default `["10000","100000","1000000"]`), `loan_a` (`"0.9"`)
and `loan_b` (`"1.3"`). The activity master switch must also be on. Stewards
cannot edit these settings. Coefficients have at most three decimals:
`0.001 <= a <= 0.999` and `b >= 1.001`. For every tier X, the nominal amount,
disbursement aX, fee (1−a)X, repayment bX and interest (b−1)X must fit the
9,000,000,000,000-credit per-operation bound. Changes validate together and
invalidate unaccepted quotes; saved receipts and balances are unchanged.

`PATCH /admin/api/games/config` accepts `blackjack.quick_stakes`, an array of
0–8 distinct canonical credit strings. Each must lie within min/max and satisfy
`(stake-min_stake) % stake_step == 0`; the response sorts them ascending.
Defaults are `["1000","5000","10000","50000"]`. An empty array disables the
buttons. Changes to limits and buttons validate atomically under the same
revision. Repeated startup preserves explicit empty arrays and existing queue terms.

The user profile's `charity_profile_public` is separate from `game_profile_public`
and defaults to false. A current ban forces anonymous display on both sets of
boards. Ranking windows and retention are fixed behavior, not new site settings.
Current effective abuse windows persist across restarts; changing their settings
keeps the existing bounded trimming and one-penalty-per-window behavior.

Embeddings require the `embeddings` operation in the platform model's `model_types`; existing models retain it, while newly created models default to chat. Configure an `openai-compatible` versioned base; the connector appends `/embeddings`. Personal and charity model connections use their existing workflow, with no separate startup variable or price configuration. Token-priced embeddings use only the uncached-input price and reward rate; a batch is one call for per-request pricing and call quotas. Missing usage follows the model's accepted reserve or global inheritance and earns no donor reward. Minimum-content penalties and the `force_store_false`/`flatten_tool_calls` policies apply only to chat. Force-store-false remains OpenAI-compatible only; tool flattening also supports Anthropic-compatible and Gateway v3/v4 chat. Other shared limits remain active.

The administrator station exposes the following authoritative keys. Unknown keys are rejected; `alert_prefs_*` is the only bounded namespace. A fresh database starts with maintenance on and registration, activities, charity, donation intake, and all games off. Existing administrator choices are preserved during upgrades.

| Key | Type / range | Default and effect |
| --- | --- | --- |
| `game_bidding_enabled`, `game_likes_enabled` | boolean | `false`; gates new queues, with the shared `games_enabled` master switch |
| `game_bidding_{tier1,tier2,tier3}_enabled`, `game_likes_{quick,standard}_enabled` | boolean | `false`; each mode gates new admission |
| `game_{game}_{mode}_ticket_milli` | positive integer millicredits | Bidding defaults: 5000/10000/50000 credits; Likes quick/standard: 5000/25000 credits. Frozen entry price; use the dedicated game configuration form; wire prices are canonical credit strings |
| `game_{game}_{mode}_rake_{platform,welfare,thursday}_bp` | integer `[0,9999]` | each `0`; the three rates must sum below 10000 and apply only to the losing entry |
| `site_name` | non-empty text, ≤256 bytes | display name; frontend falls back when unset |
| `site_logo_url` | text, ≤2048 bytes, blank allowed | display image URL; blank uses the default mark |
| `artwork_r18_disabled` | boolean | `true`; disables R-18 built-in artwork, preserving collections and saved preferences. Administrator-only writes; the same boolean is included in both public configuration projections |
| `announcement_epoch` | read-only opaque ID | generated for each fresh database and used to isolate announcement caches |
| `legal_privacy_override_zh`, `legal_privacy_override_en` | multiline text, ≤65536 bytes each | blank uses the embedded privacy template |
| `legal_terms_override_zh`, `legal_terms_override_en` | multiline text, ≤65536 bytes each | blank uses the embedded terms template |
| `legal_authoritative_locale` | `zh`, `en`, or blank | declares the controlling legal-language version |
| `charity_donation_notice_zh`, `charity_donation_notice_en` | multiline text, ≤8192 bytes each | blank uses the embedded donation guidance shown beside the user submission form |
| `default_endpoint_limit` | integer `[0,10000]` | 50; used only when a user has no explicit endpoint limit; `0` prevents new endpoint creation for those users |
| `default_endpoint_key_limit` | integer `[1,10000]` | 20 per endpoint |
| `default_model_limit` | integer `[1,10000]` | 100 per user |
| `default_binding_limit` | integer `[1,10000]` | 50 per model |
| `default_rpm_per_user` | integer `[1,4096]` | 60 heavy charity calls per user; the user's `rpm_limit` overrides this default |
| `default_light_rpm_per_user` | integer `[1,4096]` | 30 light charity calls per user; `light_rpm_limit` overrides this default |
| `global_rpm_per_user` | integer `[1,4096]` | 60 model calls per user across both classes and personal/charity calls; `global_rpm_limit` overrides this default |
| `global_rpm` | integer `[1,4096]` | 600 across the process |
| `default_per_endpoint_concurrency` | integer `[1,100000]` | 8 per normalized base URL |
| `egress_global_concurrency` | integer `[1,100000]` | 32 across all outbound requests |
| `anthropic_default_max_tokens` | nullable integer `[1,2147483647]` | raw `null`; effective fallback 65536 when an Anthropic attempt receives neither token-limit field; it is not a maximum for explicit values |
| `discord_guild_id` | text, ≤128 bytes, blank allowed | required together with `discord_role_id` for registration and sign-in without an exemption |
| `discord_role_id` | text, ≤128 bytes, blank allowed | required together with `discord_guild_id` for registration and sign-in without an exemption |
| `discord_registered_user_gate_exempt` | boolean | false; exempts registered users from guild and role checks at their next sign-in, unless overridden per user |
| `oauth_start_rate_limit` | integer `[0,1000]` | 10 starts per client IP; `0` disables the in-process layer |
| `oauth_start_rate_window_seconds` | integer `[1,3600]` | 60-second window |
| `oauth_start_rate_penalty_seconds` | integer `[0,3600]` | 60-second penalty; `0` disables the penalty duration |
| `maintenance_mode` | boolean | fresh Generation 2 seed `true`; server-side authoritative admission gate — while on, ordinary user-station `/api/*` and `/v1/*` admission is refused with `503 maintenance` except the documented bootstrap/logout and accepted-continuation routes; already-issued credentials are affected immediately; the admin station remains available. Route matrix in [api-contract.md](api-contract.md#10-maintenance-recovery-and-retention) |
| `charity_enabled` | boolean | `false`; charity system master switch — while off, no new charity routing happens and the price table is hidden; in-flight reservations still settle |
| `donation_accept_enabled` | boolean | `false`; gates new donation submissions only; review/routing of existing donations is unaffected |
| `charity_token_reserve_milli` | nullable canonical positive decimal milli-credit string | **null (default) = not configured** — distinct from an explicit value; while unset, per-token charity models without a model-level override cannot be routed (fail closed), although an enabled model record may still be saved; PATCH rejects `null` and non-positive values |
| `rpm_ban_threshold` | integer `[0,4096]` | `5`; requests denied by the per-user charity RPM limit before an automatic ban; personal calls, global limits, shared key limits and upstream `429` do not count; `0` disables |
| `rpm_ban_window_seconds` | integer `[1,316224000]` | `86400`; persisted rolling RPM violation window; unexpired counts survive restart and same-Discord re-registration |
| `rpm_ban_duration_seconds` | integer `[1,316224000]` | `86400`; automatic ban duration |
| `redemption_ban_threshold` | integer `[0,4096]` | `5`; counted redemption-code errors before an automatic ban; `0` disables |
| `redemption_ban_window_seconds` | integer `[1,316224000]` | `86400`; persisted rolling redemption violation window |
| `redemption_ban_duration_seconds` | integer `[1,316224000]` | `86400`; redemption automatic ban duration |
| `charity_min_chars` | integer `[0,1048576]` | `20`; Unicode characters in chat message text, tool descriptions/parameter schemas and prior function-call arguments before dispatch; `0` disables |
| `charity_violation_deduct_milli` | canonical non-negative decimal string | `"0"`; per-violation penalty, allowed to make credits negative |
| `charity_violation_ban_seconds` | integer `[0,316224000]` | `0`; single-violation automatic ban duration |
| `charity_violation_window_seconds` | integer `[1,316224000]` | `86400`; persisted rolling short-content window; unexpired counts survive restart and same-Discord re-registration |
| `charity_violation_ban_threshold` | integer `[0,4096]` | `0`; violations needed for a window ban |
| `charity_violation_window_ban_seconds` | integer `[0,316224000]` | `0`; window-ban duration |
| `charity_suspend_window_seconds` | integer `[1,316224000]` | `86400`; persisted rolling charity-suspension window; unexpired counts survive restart and same-Discord re-registration |
| `charity_suspend_threshold` | integer `[0,4096]` | `0`; violations needed for a charity-only suspension |
| `charity_suspend_duration_seconds` | integer `[0,316224000]` | `0`; charity-only suspension duration |
| `registration_open` | boolean | fresh Generation 2 seed `false`; when false, new registration is refused while existing accounts may sign in |
| `site_timezone_offset_minutes` | nullable integer; multiple of 30 in `[-720,+840]` | **null (default) = not configured** — distinct from an explicit `0` (UTC). While unset, site-day-key features (check-in, activity) are force-disabled for normal users behind the ordinary feature-disabled error without revealing why. Once any check-in or activity row exists, the value is frozen: every further write is refused with `conflict`, even rewriting the identical value or after those rows are later cleaned up. The offset is read authoritatively per business transaction; there is no runtime singleton |
| `level_threshold_2_milli` | canonical non-negative decimal milli-credit string | `"0"`; donation-credit threshold for automatic promotion to level 2; `"0"` disables that level's promotion; enabled thresholds must be strictly increasing in level order and are cross-validated in the same transaction as the write |
| `level_threshold_3_milli` | canonical non-negative decimal milli-credit string | `"0"`; same rules as `level_threshold_2_milli`, for level 3 |
| `level_threshold_4_milli` | canonical non-negative decimal milli-credit string | `"0"`; same rules as `level_threshold_2_milli`, for level 4; levels 5 (trainee) and 6 (full steward) remain manual-only |
| `checkin_mode` | enum: `enabled` / `level_gated` / `disabled` | `disabled`; `level_gated` admits only effective level ≥3; an unset timezone or any other unavailable cause returns the identical feature-disabled error |
| `checkin_mutually_exclusive` | boolean | `false`; when enabled, each site date permits only one successful General or Game check-in, including a zero award. Each type keeps its own availability and balance threshold. Earlier claims that day count, including after same-Discord re-registration; failed claims do not. Disabling restores independent daily eligibility without reversing issued awards |
| `checkin_award_min_milli` | canonical non-negative decimal milli-credit string | `"40000000"`; inclusive lower bound of the uniformly drawn daily award; cross-validated `min ≤ max` against `checkin_award_max_milli` in one transaction |
| `checkin_award_max_milli` | canonical non-negative decimal milli-credit string | `"60000000"`; inclusive upper bound of the daily award |
| `credits_cap_milli` | canonical non-negative decimal milli-credit string | `"250000000"`; check-in admission threshold: every effective level whose balance has reached the cap is refused (`checkin_cap_reached`) without consuming the day; `"0"` disables the threshold; it is never a truncation of awards |
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
| `game_fishing_blue_fat_fish_chance_bps` | integer `[0,10000]` | 1000; chance within legendary catches; mutually exclusive with milk crab |
| `game_fishing_milk_crab_chance_bps` | integer `[0,10000]` | 1000 on fresh storage; combined presentation chances must not exceed 10000. Upgrade defaults to `min(1000, 10000 - blue)` when absent. Both probabilities are frozen when a batch is accepted; rewards remain unchanged |
| `game_fishing_rtp` | integer `[0,100]` | 100 on fresh databases; worm/lure gross target RTP; existing configured values are preserved |
| `game_fishing_rtp_premium` | integer `[0,100]` | 100 on fresh databases; premium gross target RTP; existing configured values are preserved |
| `game_fishing_rake_{platform,welfare,thursday}_bp` | integer `[0,9999]` | 100 each; sum below 10000; freeze at admission and floor each outcome's cuts separately |
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

The four `legal_*_override_*` fields accept multiline UTF-8 text up to 65,536 bytes; the two donation notices allow 8,192 bytes. Line endings are normalized to LF before validation, storage and responses. Paragraphs, tabs and other accepted characters are preserved. Existing stored overrides are not rewritten on startup. After changing an instance's legal text, reload the settings and verify the anonymous privacy/terms pages, the donation form and `legal_authoritative_locale`. A fresh database does not import prior overrides automatically.

The independent game check-in uses `game_checkin_mode`, `game_checkin_award_min_milli`, `game_checkin_award_max_milli` and `game_credits_cap_milli`. Its fresh defaults are disabled, 40,000–60,000 game credits and a 250,000 game-credit admission cap. These generic site-config keys follow the same typed money and mode rules as the general check-in. Each wallet has its own daily record and cap; welfare eligibility instead counts the game wallet plus unused game credits held by accepted games.

### Per-user limit overrides

The administrator user APIs expose both nullable raw values and their current fallback projections:

```json
{
  "endpoint_limit": null,
  "effective_endpoint_limit": "50",
  "rpm_limit": null,
  "effective_rpm_limit": "60",
  "concurrency_limit": null,
  "effective_concurrency_limit": "5",
  "light_rpm_limit": null,
  "effective_light_rpm_limit": "30",
  "light_concurrency_limit": null,
  "effective_light_concurrency_limit": "15",
  "global_rpm_limit": null,
  "effective_global_rpm_limit": "60"
}
```

Profile mode on `PATCH /admin/api/users/{id}` accepts canonical decimal strings or null: `endpoint_limit` (0–10000), `rpm_limit`, `light_rpm_limit` and `global_rpm_limit` (1–4096), and `concurrency_limit` and `light_concurrency_limit` (1–100000). Effective fields are strings. Omission preserves a value; null restores the fallback. Heavy charity RPM uses the existing RPM default, light charity RPM defaults to 30, and combined user RPM uses the site global-per-user default. Heavy and light concurrency default to 5 and 15. These pools each combine personal and charity calls. Embeddings, rerank, SystemOne and decisions are light; all other inference operations are heavy. Classification uses the ingress operation, independent of model names or capabilities.

Administrators may edit these limits. On `/api/steward/users/{id}`, a steward with `users.limits` may edit the five inference limits within configured numeric bounds only for another non-administrator account whose effective level is strictly lower. A profile request containing other fields retains its existing permissions; endpoint quota changes require an administrator or `users.profile` within its endpoint allowance. Batch edits use the same per-user checks. Permission is rechecked before replay and mutation. Explicit limits can exceed defaults; zero never means unlimited. Site-wide RPM and egress limits remain independent.

Donated-key `max_concurrency` and `max_rpm` edit the same physical `endpoint_key_limits` used by personal calls. Both are JSON integers 0–2147483647; zero adds no per-key limit and existing site/user gates still apply. Management PATCH requires the donation `expected_revision` and, when either field is present, the physical `expected_key_limits_revision`. Null is invalid; omission preserves the value. A concurrent owner edit conflicts. Administrators and stewards with `donations.manage_keys` may edit keys within their current whole-donation scope. Standard scope checks every live key's mainstream source, effective `mainstream` endpoint tag or mainstream-model binding; an optional `charity_model_id` checks the target and never widens that scope. Enhanced tagged donations and full scope also support direct key maintenance. The independent `charity.non_mainstream` grant only widens eligible model targets. This shared effect must be shown when submitting a donation. See [API revision and replay rules](api-contract.md#31-endpoints-and-keys).

### Registration gate

A brand-new account is accepted only when all of these are true:

1. `registration_open` is `true`;
2. both `discord_guild_id` and `discord_role_id` are non-empty;
3. Discord confirms membership in that guild and possession of that role.

If either ID is blank, registration and sign-in without an exemption cannot complete. Existing accounts can sign in while registration is closed, but by default must still satisfy the guild and role checks. Administrators may enable `discord_registered_user_gate_exempt` globally or set `discord_gate_policy` in a user's profile to `inherit` (the default), `require`, or `exempt`. The per-user setting takes priority. Only administrators can change this setting through `PATCH /admin/api/users/{id}` in profile mode; stewards and users cannot change it. The current value is included in the user's account export and disappears with account deletion.

Changes apply at the next OAuth sign-in. Existing sessions and API keys keep their current lifetime and authority checks. Exemption never bypasses Discord identity verification, bans, blacklists, account deletion or new-registration requirements. Normal users may update their interface language and `game_profile_public` leaderboard preference through `PATCH /api/me`; endpoint quotas require administrator authority or `users.profile`. Steward inference-limit changes require `users.limits`, a strictly lower-level target and configured numeric bounds. See [steward permissions](steward-permissions.md).

### Mainstream channel catalog

Mainstream channels are managed through `/admin/api/mainstream-channels`, not `site_config`. Each active entry fixes a display name, category (`subscription` or `api_platform`), Connector type, and canonical base URL. At most 100 active-and-enabled entries are allowed. User endpoint creation reads `/api/endpoint-create-options`; choosing a channel copies an immutable provenance snapshot into the endpoint in the final transaction. Disabling or retiring a channel affects future choices and never rewrites an existing endpoint. A retired channel cannot be restored.

### Runtime application

RPM and concurrency settings update the shared in-process controllers immediately. The operation selects the light or heavy concurrency pool before reading the request body. Bounded model classification then selects personal or charity accounting. Charity calls check their class-specific charity cap first, then both the combined per-user global and site-wide caps; personal calls use the latter two. Changing limits preserves active permits and existing RPM events, including restored identity windows. A concurrency refusal creates no RPM hit or automatic penalty. Resource-count caps are read transactionally on each create. Display, legal, donation guidance, registration, OAuth admission, maintenance, Connector defaults, activities, reports, and game values are read from the database without rebuilding; responses are `no-store`. The site timezone offset carries no runtime singleton: consumers resolve it per transaction and fail closed when it is unset.

`GET /api/config` exposes only the display/legal values, the two donation notices, `maintenance_mode`, `registration_open`, `artwork_r18_disabled`, and `announcement_epoch`. The admin-host bootstrap endpoint exposes the same safe projection after administrator authentication. Neither exposes operational rate limits, Discord gate IDs, alert preferences, or secrets.

## What requires a rebuild or code change

The following are not arbitrary runtime customization:

- connector registry and protocol behavior;
- retention policy, security bounds, and data-lifecycle behavior;
- route/API contract changes;
- frontend layout/components beyond the supported runtime branding and legal text.

Do not weaken authentication, ownership, egress, secret, stream, station, or no-store boundaries to customize a deployment.

## Private deployment data

A public repository does not need the real URL, DNS records, Discord invite, administrator host, user list, or deployment credentials. Keep them in operator-controlled deployment configuration. Public documentation should explain the mechanism without identifying a private instance.


### Model request size and account protection

The administrator settings catalog exposes `model_request_body_limit_mib` under request limits. It is an integer from 1 to 64, defaults to **10 MiB**, and applies to both personal/charity chat and embedding calls. A missing row in a retained database uses the default; saving a change applies to new requests. Keep the reverse proxy allowance at least as large as the intended application allowance (for example, Nginx `client_max_body_size 15m;` for a 10 MiB application limit). Independent administrative, export and diagnostics limits remain as documented.

The administrator alert center supports type filtering and resolving selected unresolved alerts on the current page. User deletion records retain the Discord ID and an exact economic snapshot; a negative general or game balance requires manual review. The Discord blacklist page permanently blocks registration and bans any existing account in one transaction. Removing a blacklist entry permits future registration but requires a separate unban for an existing account.


### Image service configuration and model discovery

Image download origins apply only when an image provider returns a URL. For a result at `https://cdn.example.com/images/1.png`, allow `https://cdn.example.com`, with no path, credentials or wildcard. Base64 results need no download origin. This allowlist does not control model discovery, and downloading an image never forwards the provider API key.

The adapter describes three stages: `discovery` selects the model-list endpoint and JSON fields, `submit` maps a generation request, and `response` identifies returned images. For `{"data":[{"id":"image-model"}]}`, use `items_pointer=/data` and `id_pointer=/id`. A request path beginning with `/` starts at the host root; other paths append to the configured base path. Save changes before refreshing the catalog.

Failed discovery displays a dedicated explanation and, while its diagnostic record remains within ordinary retention, the upstream HTTP status. Authentication, missing endpoints, rate limits, redirects, timeouts, oversized catalogs and invalid model-list responses have distinct guidance. The response-body contents remain separate from the refresh status. A completed failure can start a new refresh; an uncertain acceptance retains its original retry key.

## Card collection activity

The Gacha Simulator entry is hidden by default and can be shown in its administrator settings. The display switch does not disable the activity URL. Eligible signed-in users can use the activity when the site is operational and its timezone is configured. Its dedicated administrator page uses `GET/POST /admin/api/gacha` for rules, prices, quotas, pools and saved presets. The generic activity master switch does not close this permanent collection activity. Only published limited pools use schedules; installation does not publish a limited pool automatically. See [the activity guide](gacha.md).

## Managed session headers

Personal and charity model settings use the same managed-header fields and save them with the model. Request adaptation saves separately, with request-header and request-body groups. Owners edit their personal models; administrator authority or the applicable `charity.create`/`charity.model_settings` grant edits charity settings; steward charity request adaptation remains read-only.

Each header selects `uuid_v4`, `hex_32`, `opencode_session`, `opencode_message` or `opencode_part`. Idle lifetime accepts 0–86,400 seconds; omission or a blank browser field uses 300. Zero generates a new value for each logical request, reused by its internal retries, without an absolute uniqueness guarantee. Positive lifetimes reuse a value for the same user/model/header until idle expiry. See [field defaults, precedence, export and lifecycle](charity-session-headers.md).

## Global pressure and appearance

Administrator and level-5/6 steward management logs display sampled global concurrency and RPM, their approximate 24-hour peaks and persistent all-time observed maxima. There is no new site setting or CallerKey permission. Sampling is every five seconds; visible pages refresh every 15 seconds. Refresh failures mark retained values stale, while revoked session access hides the panel. See [pressure routes and observation limits](api-contract.md#global-pressure).

Appearance controls offer light, dark or system theme and three intensity levels (`vivid`, `balanced`, `focus`). A manual theme/intensity choice takes priority over the corresponding system preference. Without a saved intensity, reduced transparency selects focus. Preferences apply immediately and are stored in the browser when available; they are separate from server-authorized cosmetic unlocks.

## Charity context prices

Charity model pricing can enable independent input thresholds for full long-context prices and a one-time extra-long penalty. Both default off and follow existing pricing permissions. See [configuration and billing behavior](charity-context-pricing.md).

## Header diagnostics and price templates

`request_header_budget_mib` is an administrator runtime setting, default 1024
MiB, range 1–65536. It is independent of the raw-error budget and includes saved
header payloads under legal hold. Lowering it below usage stops new captures
until capacity becomes available, without evicting retained captures. See
[header capture and privacy](api-contract.md#11-restricted-diagnostics-and-manual-risk-auditing).

Price templates are shared resources managed by administrators and readable by
steward browser sessions with `charity.price_templates.read`. Applying one copies the complete price/reward, Token
reserve, context and discount values into a model form, including clearing
disabled settings. Model save remains explicit. There is no template CallerKey
scope or automatic propagation to existing models. Saving the model requires
the applicable create/pricing grant. See
[the template API](api-contract.md#741-price-templates).
