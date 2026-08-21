# Data Lifecycle Checklist (export / delete / retention / privacy)

> Status: **active** — current table describes the shipped alpha.1 schema. Future rows must be added in the same change that introduces them; planning text alone is not current coverage.
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
| `sessions` | `user_id` FK CASCADE | no (transient) | cascade on user delete; idle+absolute TTL purge | idle/absolute TTL (`PurgeExpiredSessions` at startup + every 6h) | opaque hash only; no plaintext token persisted |
| `caller_keys` | `user_id` FK CASCADE | metadata only (`display_head/tail`, timestamps); **plaintext never** | cascade on user delete; regeneration invalidates prior key instantly | account lifetime | SHA-256 hash lookup; plaintext shown once, never persisted |
| `endpoints` | `user_id` FK CASCADE | yes (metadata: connector, canonical base_url, note, enabled, fetch flag, timestamps) | cascade on user delete | account lifetime | canonical base_url only; no secret |
| `endpoint_keys` | `endpoint_id` FK CASCADE | metadata only (`display_head/tail`, note, enabled, timestamps); **ciphertext/secret never** | cascade on endpoint delete (and thus user delete) | account lifetime | AES-256-GCM envelope; display fragments only in lists/logs/export |
| `fetched_models` | `endpoint_key_id` FK CASCADE | yes (upstream model id, provider, fetched_at, status) | cascade on key delete (and thus user delete) | account lifetime | upstream ids bounded/validated at fetch time |
| `models` | `user_id` FK CASCADE | yes (provider, model, full_name, route_strategy, silent_retry, timestamps) | cascade on user delete | account lifetime | opaque strings; no injection surface |
| `model_bindings` | `model_id`/`endpoint_key_id` FK CASCADE | yes (endpoint_key_id, upstream_model_id, ord) | cascade on model/key delete (and thus user delete) | account lifetime | opaque ids |
| `request_logs` | `user_id` FK CASCADE; `endpoint_key_id` FK SET NULL | yes (bounded metadata summary; **no content**) | cascade on user delete; late `RecordRequest` uses `INSERT ... SELECT ... WHERE EXISTS users` (atomic no-op) | **30-day retention cleanup**; at-most-once by `attempt_id` partial unique index | metadata + bounded `error_diag` only; content never persisted. Token values use the neutral four-bucket form (`uncached_input_tokens`/`cache_write_input_tokens`/`cache_read_input_tokens`/`output_tokens`) with legacy prompt/completion/total mirrors; `route_kind`/`endpoint_base_url`/`error_source`/`charity_reservation_id` and the charge columns are provisioned metadata for later rails and carry neutral defaults until then. The admin-station CSV/JSON export is metadata-only, unpaginated with fail-closed row/byte bounds and per-cell formula-injection sanitization; the user station has no export. The user projection JOINs current notes of the caller's own resources (empty after deletion); the admin/steward projection never selects notes or the user-chosen model name, and a charity consumer's rows reference no donor resource |
| `user_issues` | `user_id` FK CASCADE | yes (kind, message, ref, created_at, resolved) | cascade on user delete; late `RecordUserIssue`/`FailFetch` use `INSERT ... SELECT ... WHERE EXISTS users` (atomic no-op) | hard cap `MaxUserIssuesPerUser` + resolve state | bounded/sanitized message/ref via `diagnostic.BoundTo` |
| `admin_alerts` | `subject_user_id` **NO FK** (by design) | no (admin-facing, not the user's export) | explicit `DELETE FROM admin_alerts WHERE subject_user_id=?` inside `DeleteUserAccount`; late `RecordAdminAlert`/`RecordAdminAlertBounded` use `INSERT ... SELECT ... WHERE EXISTS users` (atomic no-op) | hard total cap `MaxAdminAlertsTotal` + per-kind pending cap + resolve state; **resolved alerts retained 30 days then removed by `CleanupResolvedAlerts` (startup + every 6h via `runMaintenanceSweep`); pending never removed by retention** | bounded/sanitized message/ref; no secret |
| `site_config` | **no user link** (operator runtime config; a strict display/legal/toggle subset is public) | n/a (never part of an account export) | n/a (survives account deletion) | bounded known-key set + `alert_prefs_*` namespace with typed/range/control validation | secrets are forbidden by policy, not inferred by the text validator; operators must never place credentials in these fields; public projection is an explicit allowlist |

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
