# Data Lifecycle Checklist (export / delete / retention / privacy)

> Status: **active** — current table describes the alpha.2 development schema. Future rows must be added in the same change that introduces them; planning text alone is not current coverage.
>
> Every new user-associated table or column MUST be checked against this list in the same
> task that introduces it. "User-associated" means any row whose lifecycle is bound to a
> normal user's account (directly by `user_id`, or transitively by a foreign key that
> ultimately reaches `users(id)`), plus any no-FK column that references a user id (the
> late-callback suppression pattern).

## The four checks

For each user-associated table/column, answer all four. A "no" is a contract gap that must
be closed before the task is accepted.

1. **Export** — Is the data included in the self-service export package (JSON), or
   explicitly excluded by policy with a recorded reason? Upstream secrets and their
   ciphertext, OAuth tokens, caller-key plaintext, and request/response content are NEVER
   exported (DEC-005/DEC-006). Metadata, usage totals, and bounded log summaries ARE.
2. **Delete** — Is the data removed (or atomically suppressed for late writes) when the
   account is deleted? Direct/transitive `ON DELETE CASCADE` covers most children; a no-FK
   user-reference column (e.g. `admin_alerts.subject_user_id`) needs an explicit cleanup in
   `Store.DeleteUserAccount` AND an atomic `INSERT ... SELECT ... WHERE EXISTS users`
   suppression for late writers.
3. **Retention** — Is there a bounded retention/cleanup policy, or is the data naturally
   bounded by the account lifetime? Request logs are cleaned past 30 days; expired sessions
   are purged at startup and every six hours by idle/absolute TTL; resolved admin alerts
   are removed past 30 days (`CleanupResolvedAlerts` in the same startup + 6h sweep);
   pending alerts and user issues are bounded by their own caps and resolve state.
4. **Privacy** — Does the privacy/terms text (zh/en) describe the data, and is untrusted or
   sensitive content bounded/sanitized at every sink? Upstream identifiers/diagnostics live
   only behind `internal/diagnostic.Bound`; secrets never enter logs, errors, CSV, HTML, or
   export.

## Current coverage (as of the account-deletion rail)

| Table / column | User link | Export | Delete | Retention | Privacy |
|---|---|---|---|---|---|
| `users` (identity, lang, limits) | self | yes (whitelisted fields) | row removed by `DeleteUserAccount` | account lifetime | no Discord token/credential stored; profile text bounded |
| `users` (ban/suspension columns: `banned_until`, `auto_banned`, `charity_suspended_until`) | self | current state only (`banned_until`/`charity_suspended_until` nullable unix seconds in `/api/session` and `/api/me`; no violation-window data exported) | removed with the row | account lifetime; due deadlines are lifted lazily by an atomic conditional update on read (logged, never alerted) | deadlines are server-set timestamps, never user content; the unset-vs-configured reason for feature gates is never exposed to normal users |
| `users` (usage accumulators) | self | yes (totals: four-bucket token counters `total_uncached_input_tokens`/`total_cache_write_input_tokens`/`total_cache_read_input_tokens`/`total_output_tokens` plus legacy `total_prompt_tokens`/`total_completion_tokens` mirrors) | removed with the row | account lifetime (totals are the authoritative counter; `request_logs` is never summed) | metadata only; pre-four-bucket history carried no cache split (legacy totals backfilled into uncached/output, cache buckets zero) and is never valid for retroactive billing |
| `users` (economy columns: `credits`, `donation_credit`) | self | yes (canonical decimal strings `credits` / `donation_credit` in the export user section) | removed with the row | account lifetime | server-set integer milli-credit balances; `credits` may be negative by frozen settlement semantics; every change is audited in `credit_ledger`; no violation-window or pricing-configuration data exported |
| `users` (level columns: `level`, `auto_level`) | self | yes (small integer state values `manual_level` nullable / `auto_level` in the export user section; `effective_level`/`manual_level` also projected in `/api/session` and `/api/me`) | removed with the row | account lifetime; `auto_level` is a persisted only-ever-raised high-water mark (lazy CAS promotion on authoritative resolution; no downgrade path in alpha.2), `level` is the nullable administrator-set manual override | server-set capability state; capability checks (banner ≥2, check-in cap bypass ≥3, steward ≥5) are decided server-side per use from the authoritative resolver — the projected level is display/state data, never authorization input; the administrator row is excluded from the level system entirely; no violation-window or threshold-configuration data exported |
| `credit_ledger` | `user_id` FK CASCADE; `actor_user_id` FK SET NULL | yes (the owner's full bounded ledger: id, kind, deltas and resulting balances as canonical decimal strings, nullable opaque actor id, bounded reason, created_at) | cascade on user delete (no late-write path exists: every write goes through the same-transaction balance update on a live user row) | account lifetime; append-only, never rewritten or cleaned early | kind/delta/balance/reason only; `anti_abuse_penalty` uses this existing ledger kind; the actor is an opaque nullable id (never a username); no secret, ciphertext, or request content exists on ledger rows |
| `checkins` | `user_id` FK CASCADE | yes (`checkins` section: the owner's check-in history — the site-local calendar date `YYYY-MM-DD` rendered under the frozen site offset and the granted award as a canonical decimal string; export package `schema_version` 2) | cascade on user delete (the day's site-activity rollup recompute covers the check-in contribution via the same-transaction activity row) | **account lifetime** — deliberately NOT part of any time-based retention sweep | dates + award only by construction: no streak, location, or device column exists anywhere in the table; the client can never supply the award or the day |
| `sessions` | `user_id` FK CASCADE | no (transient) | cascade on user delete; idle+absolute TTL purge | idle/absolute TTL (`PurgeExpiredSessions` at startup + every 6h) | opaque hash only; no plaintext token persisted |
| `caller_keys` | `user_id` FK CASCADE | metadata only (`display_head/tail`, timestamps); **plaintext never** | cascade on user delete; regeneration invalidates prior key instantly | account lifetime | SHA-256 hash lookup; plaintext shown once, never persisted |
| `endpoints` | `user_id` FK CASCADE | yes (metadata: connector, canonical base_url, note, enabled, fetch flag, timestamps) | cascade on user delete | account lifetime | canonical base_url only; no secret |
| `endpoint_keys` | `endpoint_id` FK CASCADE | metadata only (`display_head/tail`, note, enabled, timestamps); **ciphertext/secret never** | cascade on endpoint delete (and thus user delete) | account lifetime | AES-256-GCM envelope; display fragments only in lists/logs/export |
| `fetched_models` | `endpoint_key_id` FK CASCADE | yes (upstream model id, provider, fetched_at, status) | cascade on key delete (and thus user delete) | account lifetime | upstream ids bounded/validated at fetch time |
| `models` | `user_id` FK CASCADE | yes (provider, model, full_name, route_strategy, silent_retry, timestamps) | cascade on user delete | account lifetime | opaque strings; no injection surface |
| `model_bindings` | `model_id`/`endpoint_key_id` FK CASCADE | yes (endpoint_key_id, upstream_model_id, ord) | cascade on model/key delete (and thus user delete) | account lifetime | opaque ids |
| `request_logs` | `user_id` FK CASCADE; `endpoint_key_id` FK SET NULL | yes (bounded metadata summary; **no content**) | cascade on user delete; late `RecordRequest` uses `INSERT ... SELECT ... WHERE EXISTS users` (atomic no-op) | **30-day retention cleanup**; at-most-once by `attempt_id` partial unique index | metadata + bounded `error_diag` only; content never persisted. Token values use the neutral four-bucket form (`uncached_input_tokens`/`cache_write_input_tokens`/`cache_read_input_tokens`/`output_tokens`) with legacy prompt/completion/total mirrors; `endpoint_base_url` is the bounded dispatch-time canonical base-URL snapshot (owner-visible metadata, sanitized at the persistence boundary); `route_kind` distinguishes `personal`/`charity`, and the charity rail writes `charity_reservation_id` plus the three charge columns (`original_charge`/`user_charge`/`donor_reward`, neutral defaults on personal rows). The admin-station CSV/JSON export is metadata-only, unpaginated with fail-closed row/byte bounds and per-cell formula-injection sanitization; the user station has no export. The user projection JOINs current notes of the caller's own resources (empty after deletion); the admin/steward projection never selects notes or the user-chosen model name, and a charity consumer's rows reference no donor resource |
| `user_issues` | `user_id` FK CASCADE | yes (kind, message, ref, created_at, resolved) | cascade on user delete; late `RecordUserIssue`/`FailFetch` use `INSERT ... SELECT ... WHERE EXISTS users` (atomic no-op) | hard cap `MaxUserIssuesPerUser` + resolve state | bounded/sanitized message/ref via `diagnostic.BoundTo` |
| `admin_alerts` | `subject_user_id` **NO FK** (by design) | no (admin-facing, not the user's export) | explicit `DELETE FROM admin_alerts WHERE subject_user_id=?` inside `DeleteUserAccount`; late `RecordAdminAlert`/`RecordAdminAlertBounded` use `INSERT ... SELECT ... WHERE EXISTS users` (atomic no-op) | hard total cap `MaxAdminAlertsTotal` + per-kind pending cap + resolve state; **resolved alerts retained 30 days then removed by `CleanupResolvedAlerts` (startup + every 6h via `runMaintenanceSweep`); pending never removed by retention** | bounded/sanitized message/ref; no secret |
| `user_activity_daily` | `user_id` FK CASCADE | yes (`activity_daily` section: the user's own per-day summary — day key, product_active, four-bucket token counters, checkins, console_writes, reserved game columns; export package `schema_version` 2) | cascade on user delete; account deletion recomputes every affected day's site rollup from the surviving rows in the same transaction | **400-day retention cleanup** (`CleanupActivityBefore`, startup + every 6h via `runMaintenanceSweep`) | counters and site-local day keys only — no model names, no request content; day keys never generated while the site timezone offset is unset |
| `site_activity_daily` | aggregate only (no user link; `distinct_product_users` is a count) | no (site-wide rollup, explicitly excluded from personal exports) | deletion-safe: each affected day is recomputed from surviving `user_activity_daily` rows inside the delete transaction; days with no remaining contributor are dropped | **400-day retention cleanup** (same sweep); site-day rows removed only after their last per-user row is gone | aggregates only; distinct-user counts below k=5 are suppressed to JSON null at the admin API boundary (never conflated with 0); no user-level drill-down exists |
| `site_config` | **no user link** (operator runtime config; a strict display/legal/toggle subset is public) | n/a (never part of an account export) | n/a (survives account deletion) | bounded known-key set + `alert_prefs_*` namespace with typed/range/control validation | secrets are forbidden by policy, not inferred by the text validator; operators must never place credentials in these fields; public projection is an explicit allowlist |
| `donations` / `donation_keys` / `donation_reviews` | `donations.user_id` FK CASCADE; keys/reviews reach users transitively through `donations` (`reviewer_user_id` FK SET NULL) | yes (`donations` section of the donor's own export: safe metadata only — status, bounded base-URL snapshot, description, review note, expiry, per-key display fragments and charity limits as decimal strings, and the append-only review history; **no secret, ciphertext, or key note field exists on any exported row by construction**; export package `schema_version` 2) | donor account deletion removes claims FIRST inside `DeleteUserAccount`, then lets the cascade remove donations/keys/reviews — no routable secret is ever kept alive by a claim; a reviewer's own account deletion only SET NULLs their reviewer id and keeps the audit rows | **account lifetime** (bounded audit history); no time-based purge — terminal records keep only safe snapshots (`endpoint_base_url`, `display_head/tail`); SET NULL + snapshot replaces cascade erasure so terminal review history survives underlying resource deletion | display head/tail fragments + status/description/limits only; no note, secret, or ciphertext column exists anywhere in this table family; the steward projection joins nothing user-sensitive |
| `charity_models` / `charity_model_bindings` / `charity_model_stats` / `charity_model_outcomes` | site-wide configuration objects (`created_by_user_id` FK SET NULL is the only user link) | no (management-plane configuration, not personal data; explicitly excluded like other site resources) | model delete cascades bindings/stats/outcomes in one statement family; account deletion only SET NULLs `created_by_user_id`; bindings referencing a donation key cascade automatically when that key's donation cascades | fixed 100-slot ring buffer per model (`sample_count ≤ 100`), bounded by construction; binding count follows the donated-key supply | success bit + slot index only; prices/discounts are server-set integers; no request content, no donor identity beyond opaque nullable creator id |
| `charity_reservations` | `user_id` FK CASCADE (consumer); `donor_user_id`/`charity_model_id`/`donation_key_id` FK SET NULL | partial: the consumer's export carries their own reservation summaries (model/state/pricing/reserves/charges as decimal strings, four-bucket tokens, timestamps) with NO donation-key id or base URL; the donor's export carries per-consumption reward summaries only (model + reward amount + timestamps, never consumer identity or request data) | in-flight rows converge INSIDE the deletion transaction before the cascade (reserved→released refund, dispatched→committed unknown), so a late settlement callback observes a terminal state and is an atomic no-op; terminal rows keep safe snapshots with donor/key/model ids NULLed when those accounts/resources die | **400-day retention**: terminal (`committed`/`released`) rows past `finalized_at` cutoff are removed by the periodic sweep in bounded batches; in-flight rows are never age-removed | bounded snapshots only (model full name, base URL, upstream model id); no secret/ciphertext/note column exists; amounts are server-set integer milli-credits; the user-station log projection blanks donor resources on `route_kind='charity'` rows |

## Adding a new user-associated table/column — required steps

When a task adds a new user-associated table or a new column to one:

1. **Schema**: add the table/column to `internal/db/schema.go` with the ownership invariant
   in a SQL comment. Use `REFERENCES users(id) ON DELETE CASCADE` for direct user links, or
   a transitive FK that reaches `users`. If the new column is a user reference that must
   survive a late callback against a deleted user (so a FK would fail the insert), use the
   **no-FK + `INSERT ... SELECT ... WHERE EXISTS users`** pattern instead, and document why.
2. **Delete**: confirm the cascade reaches the new table, OR add an explicit cleanup in
   `Store.DeleteUserAccount`. Add or reuse the atomic suppression helper for any late-write
   path that targets the new table (`RecordRequest` / `RecordUserIssue` /
   `RecordAdminAlert` / `RecordAdminAlertBounded` are the shared helpers).
3. **Export**: add the new data to `ExportService.BuildExport`'s whitelist (or record an
   explicit policy exclusion). Never project secrets/ciphertext/content.
4. **Retention**: state the retention policy (account lifetime, a periodic cleanup, or a
   bounded cap) and wire any periodic cleanup.
5. **Privacy**: update the zh/en privacy text to describe the new data, and ensure every
   sink (log, error, CSV, HTML, export) bounds/sanitizes untrusted or sensitive content via
   the shared `internal/diagnostic` boundary.
6. **Tests**: add a deletion-cascade assertion for the new table to the account-deletion
   test suite (`assertUserGone` enumerates the user-associated tables), and a late-callback
   suppression case if the table has a late-write path.
7. **This table**: add a row above so the next task sees the coverage at a glance.

## Linearization rule

Account deletion and late callbacks (usage/log/issue/alert/callback writes) MUST be
linearized by an atomic conditional write, never by a read-then-write. The shared
single-writer SQLite connection (`Store.DB` has `SetMaxOpenConns(1)` + WAL +
`busy_timeout`) serializes transactions; whichever of {delete, late write} wins the writer
lock commits first, and the loser sees the committed state:

- a late write after the delete commits → `INSERT ... SELECT ... WHERE EXISTS users`
  inserts zero rows (no-op, no orphan, no resurrection);
- a delete after a late write committed → the late-written row is removed by the cascade
  (or by the explicit orphan cleanup for no-FK tables).

There is no interleaving that produces an orphan, a resurrection, or a cross-user write.
The account-deletion test suite (`TestDeleteVsLateCallbacksLinearized`,
`TestRepeatedDeleteAndCallbackShuffle`) proves this invariant under `-race` repeatedly.
