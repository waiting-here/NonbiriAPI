# Prepare existing storage for v1.1.0

The v1.1.0 server creates fresh schema 32 databases and opens exact schema 32
databases. `nonbiri-upgrade-v1` is the separate, one-time offline converter for
the following exact source structures:

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
| 27 | `0fa834ba935649cdf7a141800b5f733d9aaaaf89` |
| 28 | `904c2c677b748a9fa8c9a39edbd4e109ca3fcd56` |
| 29 | `b7699638c8e09709d2a319a754e9eb17a6aa2bf0` |
| 30 | `d899f295ce22ecf298c9051fc04930224b64d194` |
| 31 | `3f7c7ee41cd5f327b129d6b6ba7bbfe9e39323ce` |

The machine-readable descriptor is authoritative for these exact source commits
and their pinned manifest hashes. All supported sources convert atomically to
schema 32.

The executable that last used a database may differ from the listed commit if
its stored application identity, generation, schema version and complete
structure match. Unknown or modified structures are rejected. The server does
not import or embed the converter, historical DDL or historical manifests.

Build the converter from the same reviewed source as the target server:

```sh
CGO_ENABLED=0 go build -trimpath -o nonbiri-upgrade-v1 ./cmd/nonbiri-upgrade-v1
./nonbiri-upgrade-v1 --schema-info --json
./nonbiriapi maintenance schema-info --json
```

Both metadata commands need no database or environment. The converter reports
its exact sources in `offline_conversions`. The server reports current schema
32, with empty source and offline-conversion lists.

1. Rehearse on a consistent copy of the intended source using its existing
   encryption root and the reviewed target binaries.
2. Stop the service and take the deployment's complete recovery snapshot,
   including database sidecars, configuration and referenced application files.
   Keep the previous executable with that recovery set.
3. Supply exactly one existing key source, `NONBIRI_MASTER_KEY` or
   `NONBIRI_MASTER_KEY_FILE`, through the protected deployment environment.
   HTTP, OAuth and administrator settings are unnecessary for the converter.
4. Run the conversion:

   ```sh
   ./nonbiri-upgrade-v1 --apply --database /path/to/database.sqlite
   ```

5. Start the target server once, check readiness and exercise affected pages.
   Recovery uses the retained deployment snapshot; an old snapshot must not
   automatically replace new writes accepted by the target service.

Use `--check` instead of `--apply` for an explicit full offline audit. It is not
a mandatory duplicate pass over a rehearsal copy immediately before conversion.

`--database` defaults to `NONBIRI_DB_PATH`; `--timeout` defaults to `30m`.
Exactly one of `--check` and `--apply` is required. Standard output is JSON
containing `status`, `source_schema_version` and `target_schema_version`.
Statuses are `ready`, `converted` and `current`. Errors return a nonzero exit
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
existing rows. The current application produces account export format 28.

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
accepting schema 16 and current schema 32. Both commands use one engine.
`scripts/check-upgrade.sh` creates a populated synthetic source with the pinned
rc.7 program, converts it, compares retained facts and resumes economy checks.
The offline suite also covers the registered schema 17–31 fixtures. No
production database or instance configuration belongs in the repository.
