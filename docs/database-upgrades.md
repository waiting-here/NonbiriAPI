# Database upgrades

## Supported databases

The application accepts a new database, current schema **35**, or the exact
published schema **33/34** manifests. These receive atomic upgrades during
startup, before request handling and unfinished-work recovery. Schema 33 adds
history indexes; schema 34 updates the donation catalog authorization trigger and catalog view.
Index creation can take longer on large histories; allow the startup stage to finish.
Cancellation or failure rolls the upgrade back. No table rebuild or external
database conversion is needed for this step.

Existing exact schema 16–26 databases require the separate one-time converter.
The normal service contains current DDL, validation and the schema 33/34 startup steps;
it does not import the historical conversion chain. Later v1.x releases must provide
a complete upgrade path from every earlier stable v1.x database. An older binary
is not required to read a newer database.

SQLite identity remains `application_id=0x4E425249`, `user_version=2`. Account
exports use format **29**, independently of the storage schema. An unknown
identity, version or structural manifest is rejected before writes.

```sh
./nonbiriapi maintenance schema-info --json
./nonbiri-upgrade-v1 --schema-info --json
```

These commands load no application configuration or database. Each machine-readable descriptor
contains the current identity and accepted versions. The application descriptor
reports schema 35, minimum schema 33 and the exact accepted source versions and
manifest hashes, with no offline conversions. The converter descriptor lists its
exact offline sources and target. Deployment tools use both
descriptors instead of parsing Go source or deriving versions from array lengths.

## Schema changes

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
Startup validates the exact schema 33 source and encryption root, builds all four
indexes and validates the target within one transaction. Modified source
structures and partial upgrades are rejected.

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

Only published schema 16–26 sources are registered for offline conversion.
Unreleased intermediate database layouts are not supported sources.

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

`internal/db/generation_two.sql` is the complete fresh schema. The schema 33
source manifest and index-only startup step live in `internal/db`; older source
manifests and transformations live under `internal/upgrade/`, outside the normal
application dependency graph. Released source definitions are immutable.

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
The converter accepts exact registered schema 16–26 structures. For rc.7,
the reference source is `82f1711b674425745a768f781e0e0c34dcb37ac3`. The database
manifest determines eligibility, not a guess based on a release label.

Use `--check` when a full offline credential, integrity and foreign-key audit is
needed. Ordinary `--apply` validates the exact source, encryption-root identity,
required configuration and migration-affected relationships, without scanning
unrelated historical tables. Run it with the service stopped and its existing
master key available. The converter acquires exclusive
database access and converts the source to schema 35 in one transaction. A matching
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
legal text, game progress, activities, balances and financial receipts. It moves
remaining prerelease backfills out of normal startup. Existing database Gateway
configuration remains authoritative; legacy environment import runs only when its
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
