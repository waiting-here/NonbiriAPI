# Database upgrades

## Supported databases

The service opens only the exact current schema **41**, or initializes current
storage when the database and its sidecars are absent. An existing zero-byte,
partial or unknown database is not a fresh instance. Older and newer schemas are
rejected before migration, repair, WAL checkpoint or unfinished-work recovery.
The startup error identifies the required version and, when readable, the actual
version, with the explicit upgrade entry point. Current-schema identity,
structure, configuration and encryption-root checks remain required.

The standalone Go upgrader accepts exact registered schema **16–26** and
**33–40** sources and targets schema 41. Schemas 27–32 and unregistered layouts
are not supported. A developer or deployer must deliberately invoke the matching
tool; the service, startup scripts and deployment helpers must not run
`--apply` implicitly. Preserve the complete matching recovery set before any
upgrade. `--check` is an explicit offline audit; `--apply` performs the
conversion. The service does not import the historical conversion chain.

Upgrades are one-way. A server expecting an older or different schema cannot
open the upgraded database; no schema downgrade or reverse migration is provided. A newer or unrecognized source is
also rejected by the upgrader. Failure or cancellation rolls back the conversion.
Do not replace newly accepted data with an old snapshot.

SQLite identity remains `application_id=0x4E425249`, `user_version=2`. Account
exports use format **31**, independently of the storage schema. Schema versions
advance when storage changes require it, not with every software patch.

```sh
./nonbiriapi maintenance schema-info --json
./nonbiri-upgrade-v1 --schema-info --json
```

Both commands read compiled metadata without loading application configuration
or opening a database. Their format-version-1 descriptors identify the same
target schema and hashes. The service reports `schema_version=41`,
`minimum_schema_version=41`, `startup_policy="exact"` and
`upgrade_tool="nonbiri-upgrade-v1"`, with empty `source_schema_versions`,
`source_manifest_hashes` and `offline_conversions` arrays. The upgrader adds
19 `offline_conversions` entries, one for each supported source, with its tool
name, source commit, source version, pinned manifest hash and target version.
An empty service list does not remove the separate upgrader.

Deployment tooling must honor the exact-startup policy and stop on mismatches,
leaving the operator an opportunity to back up and invoke the tool explicitly.
It must not infer permission to migrate from the upgrader's advertised sources,
parse Go files or derive schema versions from array lengths.

## Schema changes

Schema 41 adds driver profiles and inherited workflow/operator bindings, safe
per-request driver snapshots, one encrypted ChatGPT connection, personal
assistant memories and their change history, and management operation
records/items/rollups/cleanup jobs. The explicit 40-to-41 step adds these ten
tables without rewriting existing configuration, credentials, conversations,
shared memories, business history or content settings. The registered schema-40
source is `753a64a8e375632759e452ad9f1a222c84417422`.

The legacy Chat Completions configuration remains the fallback driver. No
subscription account is connected by an upgrade. Existing custom templates and
tool descriptions are preserved; an exact prior built-in chat template or memory-tool description
can adopt the current defaults. Management operation
recording begins after upgrade; it does not reconstruct past actions. New
management assets remain outside account export 31. Operator-account deletion
does not erase personal memories or management action snapshots. Their explicit
cleanup rules differ from donation-linked automatic-review evidence; see the
[data lifecycle checklist](data-lifecycle-checklist.md#management-assistant-assets).

Schema 40 assigns keys to services and model catalogs to individual endpoint/key
pairs. The offline conversion splits cross-service credentials and shared catalog
histories, re-encrypting copied secrets with independent contexts. Existing
endpoint-key, grant, binding and donation-use identities remain stable, including
approved older versions and pending replacements. Unassigned endpoints and keys
receive a service so they remain manageable. Interrupted fetches become failed
attempts; their last successful results remain available.

`discovery_source_keys` records catalog ownership and `discovery_run_changes`
records successful fetch additions/removals. Both follow their parent's deletion
lifecycle. Export 31 includes the owner's catalog pair, last-success timestamp and
latest change counts. No credential plaintext is exported. Runtime requests reject
cross-service key references and cross-pair catalog attachment.

Schema 39 extends endpoint tags with `mainstream`, `mysterious` and
`other_third_party`, retaining original manual labels. `endpoint_tag_rules` and
`endpoint_tag_rule_tags` store global persistent rules. A derived
`endpoint_tag_domains` index is backfilled once and maintained with source-address
inserts/deletes; effective labels use indexed domain matches. Rule writes do not
scan and rewrite every matching endpoint. Rules and the derived index contain no
credentials, and account export stays at format 30. Upgrading preserves existing
approvals, bindings, steward policies and content settings.

Schema 38 adds covering foreign-key indexes for
`charity_reservations.charity_model_id`, `charity_dispatch_receipts.model_id`,
`charity_key_affinities.model_id`, `charity_header_sessions.model_id` and
`charity_header_uses.model_id`. Deleting a charity model can locate its references
without scanning unrelated retained history. Existing rows, foreign-key actions,
credentials, accounting and export format remain unchanged.

The upgrade to schema 37 normalizes legacy personal and charity model interface
bits once, preserving their effective Chat Completions and Responses support.
Old selections containing either interface keep both, with other interface bits,
model revisions and timestamps unchanged. Selections saved at schema 37 or later
are exact; reopening current storage does not expand them again. This runs only in the
explicit upgrader, not during service startup.

Schema 37 adds dedicated assistant transport settings, encrypted credentials,
durable request/response history, response chunks and independent token usage.
Workflow tables store configuration, two shared memories and their history,
one conversation per operator, original messages, jobs, plans, model results
and new-submission review events. These tables begin empty; prior requests or
conversation content are not reconstructed. Chat and automatic review default
to disabled. Request-log cleanup is explicit and does not delete conversations,
memories or usage. Review jobs and their evidence follow the associated donation
lifecycle. See [data lifecycle](data-lifecycle-checklist.md#management-assistant-assets).

Existing custom steward policies gain `ai.chat,ai.review.read,ai.memory.edit` as
false, preserving their prior 48 grants, six limits, donation scope, revision,
timestamps and audit history. The live catalog has 51 actions; accounts without
a custom policy use defaults with only `ai.chat` enabled among the three.
The separate chat switch and driver readiness remain required.

Schema 36 adds per-account `steward_permissions` and revisioned
`steward_permission_audits`. Existing level-5 accounts keep their legacy default
permissions and standard donation scope until explicitly configured. Scope shares
the policy revision, CAS and audit. Audits use bounded 90-day cleanup;
target-account deletion cascades, while editor deletion clears its actor reference.
Account export 30 adds the owner's effective/stored policy and retained policy
history without editor identities. Effective, stored and retained historical
policies include donation scope.

The same atomic step rebuilds `risk_client_rules`, `client_rule_auto_bans`,
`discord_blacklist_origins` and `discord_blacklist_events` to admit actual
level-5 audit labels. It copies and restores every existing row, preserves IDs,
revisions, reason history and actor labels, and recreates their indexes, foreign
keys, capacity and immutability triggers. Foreign-key enforcement remains on;
the dependent automatic-ban table is rebuilt with its parent. The blacklist
parent and original reason remain unchanged. Retained-read audit guards accept
actual levels 5 and 6 after live domain authorization. These changes do not
reclassify historical actions or grant permission by their stored role label.

The same schema-36 transaction rebuilds `admin_endpoint_tags` with its original
rows, primary key and tag/base-URL index preserved. Its closed tag set adds
`user_gateway`, `steward_gateway` and `invalid_entry`; existing classifications
are not rewritten. Fresh schema and the offline conversion chain use the same
definitions. No intermediate unpublished schema-36 layout is an accepted source.

Schema 35 updates the model-discovery authorization trigger and catalog view so valid pending
and approved donations can fetch catalogs without separate sharing consent.
The exact donated credential version and existing ownership checks remain
enforced. It does not grant inference access to pending uses or rewrite stored
rows. The trigger, view and schema identity updates are atomic.

Schema 34 adds complete `endpoint_key_id` indexes on `request_logs`,
`dispatch_claims`, `report_targets` and `donation_keys`. These avoid unrelated
history scans during endpoint-key deletion, shortening the write transaction
that can otherwise delay session reads and other writes. Foreign-key actions,
retained rows, credentials, accounting and account export 29 are unchanged.
The schema-34 migration validates the exact schema-33 source and encryption root,
builds all four indexes and validates its target within the upgrade transaction.
Modified source structures and partial upgrades are rejected.

Schema 33 introduces service connections, independently versioned credentials,
explicit endpoint grants, independent discovery sources and ordered model targets.
Donation uses record separately approved and pending versions for each native
service. Each existing endpoint becomes its own connection; identical secret
bytes are not automatically merged. Existing endpoint/key/binding identifiers,
permissions, budgets, consumption, review evidence and encrypted material are
preserved. Legacy service selections are mapped explicitly, without probing an
upstream or requiring existing donations to be approved again.

Current personal grants and approved charity uses may select different versions
of one credential. Live discovery runs and accepted claims retain their exact
authorization until completion. Removing an entry leaves an independent
credential available to its owner; explicit credential deletion and account
removal revoke its usable grants. Unreferenced versions release encrypted
material through the common retention boundary.

New failure streaks, smart backoff and Retry-After state are scoped to the donated
use, entry, native service and credential version. Existing key-wide gates retain
their original state and trial ownership. Service rows do not duplicate the
original key budget or create cross-entry quota pools.

The same schema includes Gateway v3/v4, expanded direct-inference operations,
standard modality/search usage, caller and donor pricing decisions, personal
managed headers, anonymous pressure observations and minimal caller-parameter
error receipts. Historical rows receive unknown or empty usage metadata, never
invented classifications. Existing economic snapshots and balances remain intact.

Light charity RPM and light concurrency default to 30 and 15. Existing heavy
limits are preserved; nullable per-user overrides inherit their site defaults.
Cloudflare forwarding defaults to enabled for personal models and disabled for
charity models. Endpoint-level supplemental backoff rules start empty and affect
future dispatches. Existing operator choices and instance legal overrides remain.

Account export 29 adds safe owner-scoped connections, credential/version metadata,
grants, discovery sources and donation uses with their current approved health.
It retains personal managed-header settings, configured/effective user limits and
safe aggregate modality/search usage. It excludes secret material, fingerprints,
review HMACs, generated session headers, deployment settings and internal dispatch
authorizations. See [service connections](service-connections.md) and the
[data lifecycle](data-lifecycle-checklist.md).

The registered offline sources are schemas 16–26 and 33–40 with their exact
manifests. Other intermediate database layouts are not supported sources.

Schema 26 adds shared channel binding presets and their ordered upstream-model
mappings. Existing channels start without a preset. Saving or applying a preset
does not rewrite existing bindings, enable keys or alter donation approval.
The preset structures contain no account identity.

The same upgrade removes per-key smart-backoff profiles and supplemental rules,
including the profile copied into dispatch facts. All charity keys use built-in
recognition, including keys previously disabled or restricted to other periods.
Existing recovery deadlines, reasons, sources, generation/episode identities,
trial ownership and HTTP acceptance facts are preserved. Header cooldowns are
unchanged. Startup uses the existing claim recovery path to finish interrupted
trials; removing settings does not make a waiting key immediately available.
Account export 24 omits the two configuration fields and retains safe current
state. New classifications use built-in recognition; an existing episode may
still report its historical supplemental-rule source.
Historical idempotency receipts retain their stored outcome and original expiry;
replay removes retired configuration fields from the response.

Schema 26 also adds recurring weekly discount configuration to charity models
and price templates. Existing records start with it disabled; fixed-window
discounts and accepted request price snapshots are preserved.

Schema 25 adds per-donated-key smart-backoff configuration, a current waiting or
trial episode and claim-bound recovery facts. Smart backoff defaults to off for
existing keys at that version, with no supplemental rules or active episode. Account export 23 added the safe
configuration and current state. Existing retry-header cooldowns, failure counts,
credentials and accounting retain their meanings.

`internal/db/generation_two.sql` and registered extensions form the complete fresh
schema. Registered source manifests and transformations are maintained for the
standalone tool under `internal/upgrade/`, with shared DDL and validation helpers
in `internal/db`. The normal service has no startup migration path. Released
source definitions are immutable.

For a change to persistent semantics:

1. Provide an explicit forward step from the previous supported version. Preserve stored
   operator choices and historical facts; seed only missing configuration.
2. Update fresh DDL and seeds to produce the same target. Update the structural
   manifest, pinned hashes and configuration golden together.
3. Test populated supported sources, fresh/upgrade equality, cancellation,
   injected failure, retry, unknown-source rejection and repeated startup.
   Exercise successive steps as a chain, including data-only conversions.

All required offline steps run atomically. Validation checks the registered source before
writes and the target before commit. A failed or cancelled chain rolls back.
A database rollback restores its complete matching stopped recovery set; an old
binary alone cannot reverse a migration.

Current-version startup verifies the key-check singleton, current structure and
required configuration, then recovers in-flight work. Credentials are decrypted
when used. Missing or invalid key-check material is an error, never a reason to
silently create new material. Full credential and historical accounting audits
belong to `nonbiriapi maintenance verify` on a stopped database or consistent copy.

Schema 18 adds the Gemini, Responses and Decisions protocol identifiers and
mutually exclusive milk-crab fishing presentations. Existing fish lengths remain
blue-fish facts; accepted batches keep their original probabilities and get zero
milk-crab probability. Missing milk-crab configuration defaults to 10%, reduced
only when needed to keep the existing blue-fish probability and a combined 100%
limit. Encrypted credentials, financial encodings and existing content IDs do not
change. Export format 20 adds milk-crab lengths and the new protocol identifiers.

Schema 19 adds encrypted account authenticator seeds, temporary enrollment,
per-account failed-code budgets and session-bound MFA proofs. Six level policies
default to disabled. These authentication records are excluded from account
exports; export format remains 20.

Schema 20 adds covering indexes for endpoint-key, endpoint-URL, upstream-model
and charity-model log filtering. Stored facts, MFA state and export format remain
unchanged. Log browsing uses a separate pool of at most two read-only SQLite
connections after validated startup; business writes and held-log read audits
stay on the writer. Authorization and page contents share one read snapshot.
Shutdown drains readers before closing the writer and releasing database ownership.

Schema 21 adds the manual `noncommercial_gateway` endpoint classification and the
`region` and `unavailable` model-discovery failure classes. It widens the two
existing constraints while preserving all saved tags and discovery outcomes.
No new personal data is collected; account exports remain format 20.

Schema 22 adds donated-key temporary cooldowns and claim-bound response-header
observations. Export format 21 adds the owner's safe `rate_limited_until` field;
raw response headers and claim observations are not exported.

Schema 23 adds credential-redacted request/response header facts, capacity
accounting and the independent `request_header_budget_mib` setting, default 1024.
Headers follow request-root retention and holds; they remain excluded from
personal exports.

Schema 24 adds shared price templates, 90-day safe template audits and private
donation-history preferences. Export format 22 adds nullable `owner_hidden_at`
without filtering hidden donations out of the export. Migration seeds no model
prices or templates, preserves existing instance legal text and does not change
donation terminal clocks, expiry, balances or historical settlements.

## One-time conversion

Build `./cmd/nonbiri-upgrade-v1` from the same commit as the target application.
The converter accepts exact registered schema 16–26 and 33–40 structures. For rc.7,
the reference source is `82f1711b674425745a768f781e0e0c34dcb37ac3`. The database
manifest determines eligibility, not a guess based on a release label.

Stop the service and preserve one complete matching recovery set before an
upgrade; reuse a valid snapshot of the same unchanged stopped state. Invoke the
tool explicitly after that preparation. Use `--check` when a full offline
credential, integrity and foreign-key audit is needed; a valid audit need not be
repeated immediately before conversion. Ordinary `--apply` validates the exact
source, encryption-root identity, required configuration and migration-affected relationships, without scanning
unrelated historical tables. Run it with the service stopped and its existing
master key available. The converter acquires exclusive
database access and converts the source to schema 41 in one transaction. A matching
current schema is a no-op; structural drift, an unknown source or a wrong key is
rejected. Failure or cancellation rolls back.

Each compiled migration declares its new structures, table rebuilds, relation
changes and credential impact. Relationship checks include affected child tables
and direct incoming references to rebuilt parents from both source and target
schemas. Schema 16 retains full validation; schema 17 retains credential and
protocol/fishing migration checks. Missing classifications are rejected. Successful
ordinary conversion does not certify an unrelated full-database audit.

The command writes stage events and elapsed-time heartbeats to stderr and the
final JSON result to stdout. Heartbeats indicate that the process can report its
state; they do not claim row progress or extend the configured deadline.

The conversion preserves accounts, encrypted credentials, configuration, instance
legal text, game progress, activities, balances and financial receipts. All
registered historical backfills run only in this explicit conversion. Existing
database Gateway configuration remains authoritative; legacy environment import runs only when its
import marker is absent. Remove obsolete environment keys after that step.

See the [converter instructions](../cmd/nonbiri-upgrade-v1/README.md) for commands
and the [deployment procedure](deployment.md#manual-same-generation-deployment-procedure)
for the single recovery snapshot and cutover. The source and synthetic fixtures
contain no instance addresses, keys or production records. Earlier prereleases
are outside this converter's scope. `nonbiri-upgrade-rc7` remains a narrow wrapper
for schema 16 and an already-current database; other supported sources use the
general v1 converter.

## Content versions and accounting operations

Card, theme and pool-template IDs remain stable. The gacha catalog and
`internal/gacha/presets.json` have content versions independent of the database.
Catalog synchronization inserts missing templates without overwriting administrator
edits or opening a pool. New cards, templates and new periods of an existing
activity type do not require DDL. Eligibility, pity counters and claim records
remain attached to their existing IDs.

Accounting operation kinds and their association rules are registered in Go,
so adding a valid operation does not rebuild an enum-constrained ledger table.
Database constraints still enforce amount bounds, references, uniqueness,
idempotency, immutable facts and necessary state transitions. A new persistent
meaning still needs an explicit migration and lifecycle coverage.

Saved game encodings and simulation rules have their own versions. Retained
rounds must keep their exact rules, random state and settlement facts; closing an
entry does not retire saves, entitlements or receipts. See
[game modules](game-modules.md) and the
[data lifecycle checklist](data-lifecycle-checklist.md) for integration obligations.
