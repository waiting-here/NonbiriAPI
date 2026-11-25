# Upgrade an existing database

Use the v1.4.3 tool on a stopped database matching **schema 57** (earlier v1.4.3 development),
**schema 56** (v1.4.2),
**schema 55** (v1.4.0 or v1.4.1) or the registered **schema 54** bridge.
It converts the database to schema 58. For an earlier registered source, use the
[v1.3.12 upgrade instructions](https://github.com/waiting-here/NonbiriAPI/blob/v1.3.12/docs/database-upgrades.md#one-time-conversion)
first. The service initializes a new database on fresh deployments.

Preserve the complete matching recovery set and supply the existing
`NONBIRI_MASTER_KEY` or `NONBIRI_MASTER_KEY_FILE` through the protected
deployment environment. Use the executable built alongside the target service.

```sh
./nonbiri-upgrade-v1 --apply --database /path/to/database.sqlite
```

| Invocation | Result |
| --- | --- |
| `--schema-info --json` | Print the compiled target and accepted source identities without opening the database. |
| `--check --database FILE` | Perform a read-only offline audit. |
| `--apply --database FILE` | Validate and convert the source in one transaction. An exact target database can be reopened without reapplying changes. |

The database path defaults to `NONBIRI_DB_PATH` and the timeout to 30 minutes.
Standard output carries the JSON result. `ready` means an accepted source
passed the check, `converted` means the transaction committed and `current`
means the target structure was already present. Errors return a nonzero exit
code. The service and deployment helpers leave migration to the operator.
See [database upgrades](../../docs/database-upgrades.md) for source identity,
preserved data and recovery.
