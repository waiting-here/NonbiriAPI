# Data Lifecycle Checklist (Generation 2 export / delete / retention / privacy)

> Status: **v1.0.0-beta.1** — this checklist describes the implemented
> Generation 2 export, deletion, retention, and privacy boundary. It replaces
> the alpha.3 coverage list.
>
> Canonical DDL: `internal/db/schema.go`; manifest: `internal/db/schema_manifest.go`;
> current `GenerationTwoSchemaHash`: `34fc2bcb44be4a3ff230fd71ba84f21c566d8343490039657ca33eb47201288c`.
> Normative lifecycle values come from the frozen beta.1 data-lifecycle contract.

The table cells below describe the version contract for each exact Generation 2 table
family. The registered routes, export builder, deletion coordinator, retention workers,
and bilingual privacy text are covered by their implementation and contract tests.
Schema presence alone never creates a route or expands a response.

Every user-associated table or column MUST be checked in the same task that introduces
it. This includes direct `users(id)` links, transitive foreign keys, and no-FK user
references used for late-callback suppression.

## The four checks

For every table/column, answer all four. A missing answer is an acceptance gap.

1. **Export** — Include the owner's safe slice in the beta.1 account export, or record
   an explicit policy exclusion. Never export secrets/ciphertext, CallerKey plaintext
   or verifier, fingerprints, dispatch/idempotency/worker material, Debug bodies,
   device tokens, raw IP, raw upstream data, or another participant's identity/result.
   Amounts are canonical decimal strings; bounds fail closed rather than truncating.
2. **Delete** — Use the canonical cascade where it is sufficient. Where a reference
   must survive for settlement or audit, perform the prescribed final transaction and
   de-identify it. Any no-FK late write uses an atomic `INSERT ... SELECT ... WHERE
   EXISTS users` (or an equivalent conditional write), never read-then-write.
3. **Retention** — Apply the exact frozen deadline/cap below. All timestamps are
   server UTC seconds and `now >= deadline` is the boundary. Cleanup failure remains
   retryable and must not be reported as already deleted.
4. **Privacy** — The release zh/en text and every sink (JSON, CSV, HTML, log, error,
   header, and Debug projection) must match the safe projection. Bound and sanitize
   untrusted text at the persistence boundary; structured secrets are never copied
   into reports or diagnostics.

Runtime-only state is part of the same lifecycle boundary even when it has no table:
the shared per-account SSE ring is capped at 256 events, 512 KiB, and five minutes
(first limit wins), disconnect does not extend it, and an expired replay produces only
`gap` plus an authoritative snapshot. A Debug session is capped at ten idle minutes,
one absolute hour, 4 MiB / 32 traces / 128 events per account, and 64 sessions /
128 MiB globally; the Debug v2 ring additionally caps each session at 128 events,
each event at 512 KiB, and each session at two subscribers. L5 admin-safe client
state is cleared immediately on 401/403, demotion, ban, logout, or session replacement.

## Generation 2 canonical table-family coverage

| Canonical tables (exact names from `schema.go`) | Export contract | Delete / late-write contract | Retention | Privacy and adapter owner |
|---|---|---|---|---|
| `users`; `user_deletion_markers`; `sessions`; `policy_audits`; `admin_alerts` | Export only account-safe `users` fields and language/preferences. Markers, session material, policy audits, and admin alerts are excluded from ordinary account export. | `users` is the deletion root. The marker exists only during the coordinated delete trigger; sessions cascade. Actor references in audits are de-identified/SET NULL as required. `admin_alerts.subject_user_id` needs explicit delete cleanup and atomic late-write suppression. | Users remain for account lifetime. Markers are transaction-scoped. Sessions use idle/absolute TTL cleanup. Level/config policy-audit actors are de-identified after 90 days and their minimum facts retain 365 days. Resolved-alert cleanup must follow its bounded owner policy. | No credentials or raw tokens; bounded alert/audit text and eventual actor de-identification. Components: authentication/role authority, alert projection, and lifecycle coordinator. |
| `user_activity_daily`; `site_activity_daily`; `site_usage_totals`; `config_revisions`; `site_config` | Export only the user's own daily activity. Site aggregates, usage totals, revisions, and operator configuration are not account-export data. | User-day rows cascade; affected `site_activity_daily` rows are recomputed from surviving user rows in the same delete transaction, and an empty day is deleted. `site_usage_totals`, `config_revisions`, and `site_config` survive account deletion and reset only at the destructive fresh boundary. | User and site daily activity is retained 400 days. Totals, revisions, and the bounded config catalog live for the current database generation. | Aggregates contain no model names, request content, or identity drill-down. `site_config` is an explicit public allowlist and forbids credentials by policy. Components: activity aggregation, configuration, and lifecycle coordinator. |
| `caller_keys`; `checkins`; `mainstream_channels`; `endpoints`; `endpoint_key_secrets`; `endpoint_keys`; `endpoint_key_suspensions`; `model_discovery_evidence`; `model_catalog_entries`; `model_pair_catalog`; `models`; `model_bindings` | Export language/check-in history and endpoint/model/binding safe metadata, including the endpoint's safe custom/mainstream origin. Never export CallerKey plaintext/verifier, secret ciphertext, fingerprint, internal channel category/revision, or raw discovery diagnostics. | Account-owned resources cascade where possible. Endpoint-key deletion must first account for an already-claimed attempt; terminal claims clear secret references. Mainstream channel retirement never rewrites endpoint provenance. Report suspensions follow the case; discovery evidence/catalog is atomically replaced, not appended as an unbounded history. | Account-owned resources last for account lifetime. Retired mainstream channel facts remain for the current generation while referenced. Orphaned secret ciphertext is physically cleaned no later than one hour after the last non-terminal reference disappears. Discovery keeps only current evidence/catalog; in-flight work is bounded by its operation. | Canonical URLs, safe channel names, and provider/model identifiers are bounded metadata; secrets and raw upstream bytes never enter a sink. Components: resource catalog, dispatch claims, report suspension, and lifecycle coordinator. |
| `charity_models`; `charity_model_bindings`; `charity_model_stats`; `charity_model_outcomes` | Management-plane configuration and the fixed outcome ring are excluded from ordinary account export. | `created_by_user_id` is de-identified/SET NULL; model deletion cascades bindings, stats, and outcomes. No donor identity is reconstructed from the public configuration. | Current-generation configuration; each model's stats/outcomes are a fixed 100-slot ring and do not become an event history. | Server-set model/pricing values only; no request content, secret, or donor identity. Components: charity configuration, resource catalog, and lifecycle coordinator. |
| `idempotency_records`; `logical_requests`; `dispatch_claims`; `request_logs`; `request_attempts`; `worker_checkpoints`; `accepted_operations` | Only the user's safe request-log slice is exportable. Idempotency keys/hashes, claims, attempts, worker/checkpoint data, and operation headers are internal and excluded. | Deletion blocks new claims and converges in-flight work in the same authority. Shared accepted work either receives the approved de-identified handoff or deletion fails atomically. Late callbacks are conditional no-ops. Terminal claims clear `secret_ref_id`; request/attempt rows retain only safe snapshots. | Idempotency is a fixed 24-hour window. Terminal request logs and attempts are retained 30 days. Claim ciphertext references are cleared at terminal and orphaned ciphertext is removed within one hour. Worker/checkpoint technical rows clear after terminal; non-terminal work is bounded by protocol/ledger deadlines. | No request/response body, secret, or raw upstream error; diagnostics and codes are bounded. Components: operation/worker primitives, claim/attempt service, safe log projection, and lifecycle coordinator. |
| `credit_accounts`; `credit_capacity`; `credit_operations`; `credit_entries`; `shared_pools`; `welfare_claims`; `thursday_periods`; `thursday_participants` | Export the user's own wallet/operation/claim and Thursday contribution/result slice only. Capacity, complete pool balances, and other users' ledger entries are excluded. | Account deletion first settles the user wallet to `external`, removes user-facing accounts, and SET NULLs surviving ledger references without rewriting economic facts. Welfare/Thursday settlement and participant de-identification use the same writer/CAS boundary; late callbacks cannot resurrect an account. | User ledger remains until account deletion. Shared ledger, pool, period, and settlement facts remain for the current generation; welfare claims follow account lifetime and Thursday facts run through the period/generation close. No age cleanup may break conservation. | Decimal-safe projections only; no cross-user identity, full pool distribution, or free-form secret/reason leakage. Components: ledger/capacity, activities/pools, and lifecycle coordinator. |
| `announcements`; `announcement_audits` | Not part of ordinary account export. Admin views receive only the role-allowed projection; audit rows never restore deleted content. | Permanent announcement deletion removes content immediately. Audit actor references are de-identified/SET NULL while action facts remain under their policy. Announcement epoch is never reused within a generation and changes on destructive fresh. | Content follows announcement state until withdrawal/expiry or permanent delete. Minimum action facts retain 365 days; identifiable actor data is removed after 90 days. A valid legal hold can extend only the exact approved aggregate and its end+400-day metadata window. | Rendered Markdown/text is bounded and sink-sanitized; audit reason is bounded and contains no secret. Components: announcement lifecycle, admin projection, retention, and legal-hold coordinator. |
| `donations`; `donation_keys`; `donation_key_memberships`; `donation_reviews`; `charity_reservations`; `donation_usage_reservations` | Donor export contains safe donation/key metadata, review history, limits, authorized/effective expiry, safe custom/mainstream source, and reward summaries. A consumer export contains its own reservation summary but no donation-key id/base URL; claims, source key IDs, report fingerprints, and ciphertext are never exported. | Donor deletion removes claims first and atomically transitions pending/approved work to the existing deleted path, clearing private identity/content while preserving approved safe facts. Each key expires independently; only the last live key ends the donation root. In-flight reservations converge before account cascade; terminal donor/key/model references are NULLed/de-identified as required. | Terminal donation safety records and terminal reservations are retained 400 days. A terminal donation key keeps its irreversible report fingerprint only until `ended_at + 90d`; this clock is not extended by legal hold. Non-terminal reservations are settled rather than age-removed. Claim-level usage follows the exact-once terminal lifecycle and cannot keep a deleted secret routable. | No donor identity in consumer projections, no note/secret/ciphertext/raw upstream, and only bounded model/price/source snapshots. Components: donation/charity, claims, ledger, and lifecycle coordinator. |
| `report_cases`; `report_materials`; `report_targets`; `report_decisions`; `report_rate_buckets`; `user_issues`; `user_issue_projection_state`; `maintenance_state`; `maintenance_events`; `legal_holds`; `legal_hold_audits`; `legal_hold_read_audits` | Reports, IP envelopes, cases, donation lineage, legal-hold data, maintenance audit, and admin projections are excluded from ordinary account export. A user may export only the user's own safe issue window. | Case/target/material deletion and suspension are coordinated with the resumable approval worker. A target may safely retain a case-local reference and bounded donation lineage after the physical key disappears. User issues and projection state cascade. Maintenance/legal-hold references are handled only by the environment-owned singleton administrator. A hold never blocks account revocation/deletion or business settlement. Late issue/alert writes are conditional. | Report material/case lifetime is fixed at `created_at + 90d`; accepting a public report has its own 24-hour idempotency window. A terminal donation key can match a new case for at most 90 days; an already-created target follows the case lifetime. Rate buckets expire at `window_start+20m` (IP/account/fingerprint) or `+2m` (global). Active issues cap at 4,096/account; closed issues are at most 1,000/account and 90 days from `resolved_at`. Maintenance actor data is 90 days after resolve, then minimum facts 400 days. Legal hold is closed to `maintenance_event|report_case|announcement_audit|donation|request_log`; creation irreversibly consumes the root marker, each hold is at most 365 days, and the same root cannot be reopened. Hold metadata/audits/read rollups retain 400 days after end. At `now >= retain_until` reads are semantic 404 and must not write a read rollup, regardless of delayed physical cleanup. | Treat case notes and source IP as high-sensitivity, bounded data; do not promise automatic secret redaction. Admin-only projections exclude secrets, fingerprints, descriptions, management notes, and unrelated identity. Components: reports, maintenance/role authority, safe projection, and lifecycle/legal coordinator. |
| `game_user_preferences`; `game_fishing_batches`; `game_fishing_outcomes`; `game_fishing_best`; `game_fishing_rank_facts`; `game_fishing_rank_aggregates` | Export tutorial/profile preference and the user's safe pending/terminal/best/rank slice. Internal request hashes, retry material, and other users' rankings are excluded. | User-owned rows cascade. Reserved fishing batches settle/release before delete; rank facts/aggregates are removed from public eligibility immediately, with no late ACK/recovery resurrection. | Pending batches are not age-removed and recover within their bounded retry policy. Terminal batches/outcomes and rolling rank facts are retained 30 days; personal best and rank aggregate remain for account lifetime. | Outcomes are server-generated typed values; no client text, secret, device, or raw IP. Components: Fishing and lifecycle coordinator. |
| `game_linklink_sessions`; `game_linklink_summaries`; `game_online_leases` | Export only the user's safe in-flight/30-day LinkLink summary. Leases and full board/path/random material are excluded. `game_online_leases` is shared by LinkLink and RPS. | Active sessions converge at the absolute deadline; terminal handling removes board/path/lease material. Account deletion removes sessions and leases; no shared tombstone or late action can recreate a game. | In-flight sessions are deadline-bound and not age-purged before convergence. Terminal summaries are retained 30 days. Leases are process/game control only: client renewal is 5 seconds and server TTL is 15 seconds, and a lease never extends business retention. | No board entropy, device/IP, or cross-user identity in export/logs. Components: LinkLink, shared game leases, and lifecycle coordinator. |
| `game_rps_queue`; `game_rps_sessions`; `game_rps_seats`; `game_rps_user_slots`; `game_rps_pending_results`; `game_rps_summaries`; `game_rps_summary_seats`; `game_rps_fun_stats`; `game_rps_rank_facts`; `game_rps_rank_aggregates` | Export private pending results, the user's 30-day summary slice, and account-lifetime fun aggregate. Do not export queue/slot/session internals, hidden gestures, other seats, or complete rank data. | Queue reservations release atomically. During deletion a seat becomes the prescribed de-identified state; terminal processing first writes all economic/shared/private projections in one transaction and then removes in-flight session/seat/slot rows. ACK, matcher, and late actions are CAS no-ops. | Queue is bounded by the 30–120 second mode deadline. Pending results remain until a real ACK or account deletion. Shared summaries and rank facts are retained 30 days; fun aggregates remain for account lifetime. Summary seats lose identity on account deletion; aggregate/rank rows are removed from public eligibility. | Hidden gesture envelopes and device/IP comparison values never leave the game service; public ranks are per mode with no cross-mode aggregate. Components: RPS, ledger, shared lease/SSE primitives, and lifecycle coordinator. |

The table is the version coverage statement. New fields remain excluded from every
wire/export until they are deliberately added to the relevant whitelist and tests.

## Linearization and lifecycle acceptance

Account deletion and late callback/ACK/settlement writes must be linearized by one
single-writer transaction. If the late write wins, deletion cascades or explicitly
cleans it; if deletion wins, the conditional late write inserts zero rows. Neither
order may resurrect an account, wallet, identity, public rank, secret, or query
projection. Each owner must test both orders, replay, DB busy, crash/restart, and the
deadline boundary (`-1`, exact, `+1` second). The cross-domain lifecycle pass must also test 401/403, logout,
account-switch cache clearing, and export exclusion across every domain.

## Operator snapshot and destructive-fresh boundary

Complete operator snapshots are outside the account-export contract. They may contain
all private data, encrypted credentials, configuration, game state, and correlation
indexes, and must stay paired with the exact release, environment, master key, unit,
manifest, and checksums. Snapshot retention/deletion is operator-controlled and must
be disclosed by the instance policy; beta.1 does not promise per-user erasure inside
historical snapshots.

Generation 2 accepts a fresh database only when main/WAL/SHM are all absent; an
existing 0-byte main, alpha.3/unknown generation, bad header/identity/manifest/secret
envelope/config, or an unsafe path fails closed. There is no migration, old-data
import, automatic repair, or compatibility schema. A current database is validated
before any source write and before writable open. Destructive fresh starts with
maintenance on and registration/game/activity off, and does not merge a source
snapshot. Re-activating an old copy is an operator event that must disclose its data
cutoff and repeat any needed revocation/configuration; it is not an online deletion
guarantee.

## Change discipline

Any new table, column, route, or retention value requires an explicit export
whitelist/exclusion, synchronous deletion and late-write behavior, an exact retention
sweep or cap, bilingual privacy review, and fixed-clock/cascade tests before release.
