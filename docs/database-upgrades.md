# Database upgrades

## Supported databases

v1.4.3 uses schema 57. Stop the existing instance and preserve its
complete recovery set before running the explicit offline conversion.

| Starting state | How to use v1.4.3 |
| --- | --- |
| Fresh deployment | Start with the database file and SQLite sidecars absent. The service initializes schema 57. |
| Exact current schema 57 | Start the service with its matching configuration and master key. |
| Exact v1.4.2 schema 56 | Use the v1.4.3 offline upgrader to extend caller-error receipts and preserve fractional reservation discounts. |
| Exact v1.4.0 / v1.4.1 schema 55 | The same converter also adds sign-in presentation settings. |
| Exact v1.3.12 schema 54 | The same converter also applies the model-usage conversion. |
| Earlier supported database | Use the v1.3.12 upgrader first, then convert its schema 54 database using v1.4.3. |

For sites using the former Whale Haven defaults of 28 g per meal and 35 g per visit coin,
conversion adds a settings revision with a rate of 28 g per coin. Earlier meals
use the settings effective at settlement. Custom meal sizes and coin rates keep
their configured values.

The converter identifies each source layout by its schema manifest.

| Source reference | Schema | Manifest SHA-256 |
| --- | --- | --- |
| v1.3.12 (`03456c51`), shared by v1.3.10–v1.3.12 | 54 | `ca682daf10a780b8ab38d4095301324a1f007af3fb2ac159fbbbf0daef0107b5` |
| v1.4.1 (`b1700f25`), shared by v1.4.0 and v1.4.1 | 55 | `a4c97524571580d8b513789c2a4d1b1ec80c1bdedec8d1eace14bcdcbc291ee4` |
| v1.4.2 (`1bb01bdb`) | 56 | `5bfd5783118b05d6a6970bdf269bf6f3816aeeeed152ca0fcaa916b33a425358` |

The v1.3.12 tool accepts its registered schema 16–26 and 33–53 sources.
Consult that tool's source matrix before attempting the first leg.
Unregistered, damaged and newer layouts require separate investigation.
[v1.3.12 upgrade guide](https://github.com/waiting-here/NonbiriAPI/blob/v1.3.12/docs/database-upgrades.md)

The service opens only exact current storage. A startup mismatch reports the
required version, the actual version when readable and the upgrade entry point.
An empty placeholder file, partial database or orphaned sidecar is an error.
The SQLite application ID stays `0x4E425249` and its `user_version` stays 2.

## One-time conversion

1. Stop the service. Preserve the database and any sidecars, matching master
   key, environment, application and deployment configuration as a complete
   recovery set.
2. Use the standalone upgrader built from the same revision as the target
   service. Supply the existing master key through the protected deployment
   environment and run the conversion with exclusive access.
3. Start the target on the converted database. Current-schema validation and
   unfinished-work recovery run before normal service resumes.

```sh
./nonbiri-upgrade-v1 --apply --database /path/to/database.sqlite
```

Use `--check` for an optional offline audit of credentials, integrity and
foreign keys. Conversion checks the source and validates the target before
committing. A failed or cancelled conversion rolls back. Successful upgrades
go forward only. Preserve any new data accepted after reopening. Service startup
and deployment hooks never invoke `--apply`.

The following commands print their compiled identities without loading
configuration or opening storage. The service declares exact startup at 57.
The upgrader lists the accepted source versions and their release identities.

```sh
./nonbiriapi maintenance schema-info --json
./nonbiri-upgrade-v1 --schema-info --json
```

## Schema changes

Schema 57 extends the caller-error receipt constraint to accept the numeric
Google error code `400` for a verified HTTP 400 input rejection. Conversion
copies the existing receipts and reinstates their dispatch and immutability guards.

Schema 57 also adds a nullable integer basis-point snapshot to charity reservations. Older reservations retain the integer percentage recorded at admission.

Schema 56 adds optional campaign covers and featured sign-in milestones.
Existing campaigns keep the original illustration and have no featured days.
The conversion preserves reward amounts, progress, sign-ins and awards.

Schema 55 adds cumulative call and credit limits and usage for each donation
key and exact upstream-model pair. The conversion preserves total, input and
output token counters and reservations. It reconstructs calls, credits and
outstanding reservations from surviving receipts attributed to the model.
Migrated model rows carry `historical_costs_partial`. Earlier usage without
reliable attribution stays at key scope. Whole-key counters and account balances
retain their recorded values.

Schema 55 also adds model-scoped Google quota waits, per-attempt failure and
backoff records, and Whale Haven settings, balances, homes, visits and receipts.
Model waits start empty. Conversion derives historical log details only where
retained evidence supports them. Whale Haven and cash-out both start closed.
Existing game saves and recorded decisions keep their original rules and facts.

Account export advances from format 36 to 38. It includes the owner's model
consumption limits and usage plus Whale Haven balances, home when adopted and
currency movements. This version exports `nonbiriapi-account-export-v38.json`.
[Account export contract](api-contract.md#9-export-privacy-and-account-deletion)
[Earlier schema changes](https://github.com/waiting-here/NonbiriAPI/blob/v1.3.12/docs/database-upgrades.md#schema-changes)

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
