# Database upgrades

## Supported databases

The stable baseline is schema **17**. The application accepts a new database or a
registered stable schema from v1.0.0 onward. Later v1.x binaries must upgrade any
earlier v1.x schema through the complete forward chain; intermediate binaries are
not required. An older binary is not required to read a newer database.

SQLite identity remains `application_id=0x4E425249`, `user_version=2`. Account
exports use format **19**, independently of the storage schema. An unknown
identity, version or structural manifest is rejected before writes. Schema 16
requires the separate rc.7 converter and is not a normal startup source.

```sh
./nonbiriapi maintenance schema-info --json
```

This command reads no environment or database. Its machine-readable descriptor
contains the current identity, minimum supported version, registered migration
sources and offline conversions. Deployment tools should use it instead of
parsing Go source or deriving a version from the number of migrations.

## Stable migrations

`internal/db/generation_two.sql` is the complete fresh schema. The migration
registry declares explicit source and target versions, structural manifests and
transactional SQL or Go conversion callbacks. Released steps are immutable.

For a change to persistent semantics:

1. Register a forward step from the previous stable version. Preserve stored
   operator choices and historical facts; seed only missing configuration.
2. Update fresh DDL and seeds to produce the same target. Update the structural
   manifest, pinned hashes and configuration golden together.
3. Test populated supported sources, fresh/upgrade equality, cancellation,
   injected failure, retry, unknown-source rejection and repeated startup.
   Exercise successive steps as a chain, including data-only conversions.

All required steps run atomically. Validation checks the registered source before
writes and the target before commit. A failed or cancelled chain rolls back.
A database rollback restores its complete matching stopped recovery set; an old
binary alone cannot reverse a migration.

Current-version startup verifies the key-check singleton, current structure and
required configuration, then recovers in-flight work. Credentials are decrypted
when used. Missing or invalid key-check material is an error, never a reason to
silently create new material. Full credential and historical accounting audits
belong to `nonbiriapi maintenance verify` on a stopped database or consistent copy.

## One-time rc.7 conversion

Build `./cmd/nonbiri-upgrade-rc7` from the same commit as the target application.
The converter accepts the registered rc.7 schema 16 identity and exact structure;
the reference source is `82f1711b674425745a768f781e0e0c34dcb37ac3`. The database
manifest determines eligibility, not a guess based on a release label.

Use `--check` on an isolated consistent copy, then `--apply` with the service
stopped and its existing master key available. The converter acquires exclusive
database access and transforms schema 16 to 17 in one transaction. A matching
schema 17 is a no-op; structural drift, an unknown source or a wrong key is
rejected. Failure or cancellation rolls back.

The conversion preserves accounts, encrypted credentials, configuration, instance
legal text, game progress, activities, balances and financial receipts. It moves
remaining prerelease backfills out of normal startup. Existing database Gateway
configuration remains authoritative; legacy environment import runs only when its
import marker is absent. Remove obsolete environment keys after that step.

See the [converter instructions](../cmd/nonbiri-upgrade-rc7/README.md) for commands
and the [deployment procedure](deployment.md#manual-same-generation-deployment-procedure)
for the single recovery snapshot and cutover. The source and synthetic fixtures
contain no instance addresses, keys or production records. Earlier prereleases
are outside this converter's scope.

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
