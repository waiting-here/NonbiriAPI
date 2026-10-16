# Convert rc.7 storage to the current stable schema

`nonbiri-upgrade-rc7` converts the schema 16 database used by rc.7 to schema 18,
including the intermediate schema 17 conversion in the same transaction.
It accepts the exact SQLite identity and structure published at
`82f1711b674425745a768f781e0e0c34dcb37ac3`. The deployed executable need not have
that commit if its database has the same identity and structure. Other
prerelease schemas and locally modified structures are rejected. Existing
stable schema 17 databases upgrade through normal server startup; this tool
does not accept them as prerelease sources.

Build the tool from the same reviewed source as the target server:

```sh
CGO_ENABLED=0 go build -trimpath -o nonbiri-upgrade-rc7 ./cmd/nonbiri-upgrade-rc7
./nonbiriapi maintenance schema-info --json
```

The metadata command reads compiled constants without environment loading or
database access. `source_schema_versions` describes normal stable migrations;
`offline_conversions` describes separate, one-time conversion tools.

1. Rehearse on a consistent copy of the intended source, using its existing
   encryption root and the same reviewed target binaries.
2. Stop the service. Take the deployment's complete recovery snapshot, including
   database sidecars, configuration and referenced application files. Keep the
   previous executable with that recovery set.
3. Supply exactly one existing key source, `NONBIRI_MASTER_KEY` or
   `NONBIRI_MASTER_KEY_FILE`, through the protected deployment environment.
   HTTP, OAuth and administrator configuration are unnecessary.
4. Check and apply the conversion:

   ```sh
   ./nonbiri-upgrade-rc7 --check --database /path/to/database.sqlite
   ./nonbiri-upgrade-rc7 --apply --database /path/to/database.sqlite
   ```

5. Remove retired environment keys after conversion, then start the target
   server once and check readiness and the affected pages. Do not restore the
   previous snapshot automatically after accepting new production writes.

`--database` defaults to `NONBIRI_DB_PATH`; `--timeout` defaults to `30m`.
Exactly one of `--check` and `--apply` is required. Standard output is JSON with
`status`, `source_schema_version` and `target_schema_version`; statuses are
`ready`, `converted` and `current`. Failures return a nonzero exit status without
printing database contents or environment values.

Checking opens the database read-only and verifies schema and encryption
material. SQLite may create WAL coordination sidecars. Applying requires
exclusive access and converts in one transaction; cancellation or failure
rolls back both conversion stages. A matching schema 18 database returns
`current` without changes.
The tool creates no additional backup. Recovery uses the deployment snapshot.

The conversion preserves business, configuration, legal, encrypted credential,
game and ledger records. It fills missing continuity identities and credential
review digests, initializes historical usage totals only when their marker is
unset, and imports `NONBIRI_GATEWAY_MODEL_CAPABILITIES` only when the old import
marker is absent. Existing database model policies remain authoritative.
It also preserves prior abuse and idempotency facts while enabling the
redemption-code window; the three new ban settings are inserted only if absent.
The stable migration enables the additional upstream protocol identities and
milk-crab presentation. Existing fishing probabilities and outcomes remain
unchanged: accepted batches retain a zero milk-crab probability, and existing
length records remain blue-fish records. The new site probability defaults to
10%, capped by the remaining probability after the configured blue-fish share;
an explicit stored value is preserved.
Remove that variable and `NONBIRI_SMTP_HOST`, `NONBIRI_SMTP_PORT`,
`NONBIRI_SMTP_USER`, `NONBIRI_SMTP_PASS`, `NONBIRI_SMTP_FROM`, and
`NONBIRI_SMTP_TLS` after a successful conversion.

Run `scripts/check-upgrade.sh` to generate a populated synthetic database using
the pinned rc.7 program, convert it, compare retained facts and resume the
economy projection. No production database or instance configuration belongs
in the tool, fixtures or repository.
