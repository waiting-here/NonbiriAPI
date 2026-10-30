# Prepare existing storage

The server initializes an absent database as schema 43 and opens only the exact
current schema. Older, newer, unknown and incomplete storage is rejected before
migration, repair or recovery writes. The service, startup scripts and deployment
helpers must not run this upgrader implicitly. A developer or deployer must
preserve a matching recovery set and deliberately invoke it before the new
server starts. An existing zero-byte or partial database is not a fresh instance.

`nonbiri-upgrade-v1` is the separate, one-time offline converter for the following
exact source structures:

| Schema | Source commit |
| --- | --- |
| 16 | `82f1711b674425745a768f781e0e0c34dcb37ac3` |
| 17 | `117c0cb009ab285451a3e0238ab97b383faebde3` |
| 18 | `0f02e60704934b589b51ca91eb34bac9c5e77c02` |
| 19 | `c4145513b1ca5bd20f0236763c247b8bd5c66df2` |
| 20 | `51a1e6156cb164802b22fdd881a4ac2040dd1904` |
| 21 | `bd49471c3e4da5a94bd85292805b17b318f50ddd` |
| 22 | `ffe31ab3918ec96151b62533fd1ead4c22c466bd` |
| 23 | `75044b0e685e0f2c3a0d41b19be6e54fd54d7b5b` |
| 24 | `6f4ee67206eb6fb3a3f345ba06b2d85aba5abc83` |
| 25 | `2b4f4f62ed5b01c247fcc3f79005358ef0237933` |
| 26 | `361cb204e888de51901eecc993d3d52b1d156d3a` |
| 33 | `a7ea261babd424470ff67d1d1b9a119c29437d2e` |
| 34 | `8576d726e49bd76db6d191914cb4f3637b25f493` |
| 35 | `2df56fe8814e6a9968380fc8461942065eed3f12` |
| 36 | `1fc9527f0c9023542dfc7decf59789733a6a834f` |
| 37 | `4413e15527dff68e42386ca4841c38fc93911539` |
| 38 | `de51645924a488d742baad5ff1a35f6569f22746` |
| 39 | `31f18116afe12c50a48bca87fe48c1a7c01ba796` |
| 40 | `753a64a8e375632759e452ad9f1a222c84417422` |
| 41 | `5c721cd00b1ebcc402f0b85ce02aa9014498aa97` |
| 42 | `cd07f2ccde4d98b130c3c6c71e905bd4a6fd06d3` |

The machine-readable descriptor is authoritative for these exact source commits
and their pinned manifest hashes. All supported sources convert atomically to
schema 43. Schemas 27–32 and unregistered layouts are not supported sources.

The executable that last used a database may differ from the listed commit if
its stored application identity, generation, schema version and complete
structure match. Unknown or modified structures are rejected. The server does
not import the converter or its historical conversion chain, and has no startup
migration path.

Build the converter from the same reviewed source as the target server:

```sh
CGO_ENABLED=0 go build -trimpath -o nonbiri-upgrade-v1 ./cmd/nonbiri-upgrade-v1
./nonbiri-upgrade-v1 --schema-info --json
./nonbiriapi maintenance schema-info --json
```

Both metadata commands need no database or environment. The converter reports
its exact sources in `offline_conversions` and target schema 43. The server
reports current and minimum schema 43, `startup_policy="exact"` and
`upgrade_tool="nonbiri-upgrade-v1"`, with empty `source_schema_versions`,
`source_manifest_hashes` and `offline_conversions` arrays. The converter
retains that target policy and adds its 21 offline-source entries. These describe
different executables; the empty server list does not remove the converter or
authorize a deployment helper to call it automatically.

The 42-to-43 step adds the bounded administrator operation-diagnostic table,
managed donation service-health capture and read indexes for dispatched charity
outcomes and active fishing ranks. It preserves existing credentials, settings,
history and service state; it neither resets failure counters nor backfills
diagnostics. The accepted schema-42 manifest is
`7118778b4ef5db7a41f899406a647d6cb3a2676ad86cf1de6d1fc8d4c6ee20ec`.
See [database upgrades](../../docs/database-upgrades.md#schema-changes) for the
historical steps, which retain their original source/target identities.

The schema-37 upgrade step preserves the effective Chat/Responses support
of existing personal and charity models by normalizing their legacy interface
bits once; selections saved at schema 37 or later remain independent on subsequent starts.

Upgrades are one-way: a server expecting an older or different schema cannot
open the upgraded database. No schema downgrade or reverse migration is provided,
and newer database versions are rejected before writes. Recovering an older
schema requires a complete matching stopped snapshot; switching binaries cannot
reverse the conversion.

1. Rehearse on a consistent copy of the intended source using its existing
   encryption root and the reviewed target binaries.
2. Stop the service and take the deployment's complete recovery snapshot,
   including database sidecars, configuration and referenced application files.
   Keep the previous executable with that recovery set.
3. Supply exactly one existing key source, `NONBIRI_MASTER_KEY` or
   `NONBIRI_MASTER_KEY_FILE`, through the protected deployment environment.
   HTTP, OAuth and administrator settings are unnecessary for the converter.
4. After the recovery set is ready, deliberately choose the required action.
   For a full offline source audit:

   ```sh
   ./nonbiri-upgrade-v1 --check --database /path/to/database.sqlite
   ```

   To perform the upgrade:

   ```sh
   ./nonbiri-upgrade-v1 --apply --database /path/to/database.sqlite
   ```

5. Complete the manual release switch and start the target server once, check
   readiness and exercise affected pages. Do not retry a deployment path that
   still identifies the source through the old server's descriptor. Recovery
   uses the retained deployment snapshot; an old snapshot must not automatically
   replace new writes accepted by the target service.

Use `--check` instead of `--apply` for an explicit full offline audit. It is not
a mandatory duplicate pass over a rehearsal copy immediately before conversion.

`--database` defaults to `NONBIRI_DB_PATH`; `--timeout` defaults to `30m`.
Exactly one of `--check` and `--apply` is required. Standard output is JSON
containing `tool`, `status`, `source_schema_version`,
`target_schema_version`, `target_manifest_hash` and
`supported_source_schema_versions`. Status `ready` means an accepted source
passed check without conversion, `converted` means apply committed, and
`current` means the exact target was already present. Errors return a nonzero exit
status without printing database content or environment values.

Checking opens the source read-only and performs a credential scan, SQLite
`quick_check` and complete foreign-key checks, in addition to exact structure
and encryption-root validation. SQLite may create WAL coordination sidecars.
Applying normally rechecks the source under exclusive access and performs all
changes in one transaction. Registered migration impacts determine credential
and relationship checks, including direct incoming children of rebuilt tables
from both source and target metadata. Unchanged historical data is not audited;
schema 16 retains full validation. Final structure, required configuration, key
material and affected references must validate before commit. Failure or cancellation
rolls back the conversion and finishes closing handles before a retry. A
matching final database returns `current`. The tool creates no backup or
automatic repair.

Stderr carries JSON-line progress events with `phase`, `state`, total and phase
elapsed milliseconds, source/target schema versions, migration version and table.
Five-second heartbeats report elapsed time, not a completion percentage or row
count, and never extend `--timeout`. Stdout remains the final result JSON. Losing
the progress reader does not cancel the conversion; explicit termination signals
still request cancellation and rollback.

Business, configuration, legal, encrypted credential, game and ledger facts
are preserved. Schema 19 and 20 sources keep their MFA requirements, enrollments and proofs unchanged.
Earlier sources receive default-off MFA policies without enrollments or proofs.
Schema 20 adds four covering indexes for request-log filtering; it changes no
business records. Schema 21 widens endpoint tags with `noncommercial_gateway`
and model-discovery outcomes with `region` and `unavailable`, preserving
existing rows. The current application produces account export format 31, including own
stored/effective steward permissions and retained policy history. Schema 36
rebuilds the risk-rule/automatic-ban and blacklist audit tables with existing
rows, labels, indexes and constraints preserved; new actions can record actual
level-5 actors. Schema 37 adds independent assistant request and workflow assets;
request cleanup does not remove conversation originals, memories or token usage.
Automatic-review evidence follows its associated donation lifecycle. See the
[upgrade contract](../../docs/database-upgrades.md).

Schema 17 additionally receives the protocol and fishing presentation changes
already present in schema 18. Existing probabilities and outcomes remain:
accepted batches have zero milk-crab probability, and previous length records
remain blue-fish records. The new site probability is 10%, capped by the
remaining probability after the configured blue-fish share; an explicit stored
value is preserved.

Schema 16 first receives the rc.7 conversion: missing continuity identities and
credential-review digests are filled, historical usage totals are initialized
only when their marker is unset, and `NONBIRI_GATEWAY_MODEL_CAPABILITIES` is
imported only when its old import marker is absent. Existing database model
policies remain authoritative. Prior abuse and idempotency facts are retained;
the three redemption-ban settings are inserted only if absent. After this
conversion, remove the retired Gateway variable and `NONBIRI_SMTP_HOST`,
`NONBIRI_SMTP_PORT`, `NONBIRI_SMTP_USER`, `NONBIRI_SMTP_PASS`,
`NONBIRI_SMTP_FROM` and `NONBIRI_SMTP_TLS` from the deployment environment.

The [rc.7 command](../nonbiri-upgrade-rc7/README.md) remains a narrow wrapper
accepting schema 16 and current schema 43. Both commands use one engine.
`scripts/check-upgrade.sh` creates a populated synthetic source with the pinned
rc.7 program, converts it, compares retained facts and resumes economy checks.
The offline suite also covers the registered schema 17–26 fixtures. No
production database or instance configuration belongs in the repository.

Schema 42 adds the independent encrypted Codex connection, preserves existing
driver snapshots while allowing the new provider, and adds bounded request
failure/delivery observations plus management-detail search terms and snapshots.
The explicit 41-to-42 step preserves existing credentials, configuration and
history. It neither connects an account nor invents missing historical names,
changes or failure evidence. Account export remains format 31: it includes the
existing safe request summaries, not these administrator assets or raw diagnostics.
The registered schema-41 source is `5c721cd00b1ebcc402f0b85ce02aa9014498aa97`.

The schema-41 step adds driver profiles/bindings, per-request source snapshots,
one encrypted ChatGPT connection, personal assistant memories/history, and
management operation records/items/rollups/cleanup jobs. Existing settings,
credentials and retained histories are preserved; no subscription login or
historical management-event backfill is performed. These administrator assets
remain outside account export 31. See the [schema changes](../../docs/database-upgrades.md#schema-changes).

The schema-40 step gives each key one service owner and each model list one
endpoint/key pair. Legacy cross-service credentials and shared catalogs are
split while retaining endpoint-key, binding and donation-use identities,
budgets and approval state. Copied secrets use independent encryption contexts.
Account export 31 adds safe catalog ownership and fetch-history metadata.

The schema-39 step preserves manual endpoint tags, extends their vocabulary and
adds persistent wildcard rules plus a derived domain index. Existing approvals,
bindings, permissions and content settings are preserved. Subsequent rule writes
change effective labels without rewriting each matched endpoint.

The schema-38 step adds five foreign-key indexes for charity-model references
in reservations, dispatch receipts, affinities and managed-header state. This
avoids unrelated history scans during model deletion while preserving existing
rows, foreign-key actions and accounting. See the
[schema changes](../../docs/database-upgrades.md#schema-changes).
