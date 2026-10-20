# Database upgrades

## Supported databases

The v1.0.5 development application accepts a new database or the current schema **25**.
Existing exact schema 16–24 databases require the separate one-time converter.
The normal service contains only current DDL and validation; it does not import
or embed historical schemas or conversion code. Later v1.x releases must provide
a complete upgrade path from every earlier stable v1.x database. An older binary
is not required to read a newer database.

SQLite identity remains `application_id=0x4E425249`, `user_version=2`. Account
exports use format **23**, independently of the storage schema. An unknown
identity, version or structural manifest is rejected before writes.

```sh
./nonbiriapi maintenance schema-info --json
./nonbiri-upgrade-v1 --schema-info --json
```

These commands load no application configuration or database. Each machine-readable descriptor
contains the current identity and accepted versions. The application descriptor
accepts only schema 25 and has empty migration and conversion lists. The converter
descriptor lists its exact offline sources and target. Deployment tools use both
descriptors instead of parsing Go source or deriving versions from array lengths.

## Schema changes

Schema 25 adds per-donated-key smart-backoff configuration, a current waiting or
trial episode and claim-bound recovery facts. Smart backoff defaults to off for
existing keys, with no supplemental rules or active episode. Account export 23 adds the safe
configuration and current state. Existing retry-header cooldowns, failure counts,
credentials and accounting retain their meanings.

`internal/db/generation_two.sql` is the complete fresh schema. Historical source
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
The converter accepts exact registered schema 16–24 structures. For rc.7,
the reference source is `82f1711b674425745a768f781e0e0c34dcb37ac3`. The database
manifest determines eligibility, not a guess based on a release label.

Use `--check` when a full offline credential, integrity and foreign-key audit is
needed. Ordinary `--apply` validates the exact source, encryption-root identity,
required configuration and migration-affected relationships, without scanning
unrelated historical tables. Run it with the service stopped and its existing
master key available. The converter acquires exclusive
database access and converts the source to schema 25 in one transaction. A matching
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
