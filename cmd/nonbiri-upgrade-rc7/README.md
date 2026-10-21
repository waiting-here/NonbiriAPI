# Convert rc.7 storage

`nonbiri-upgrade-rc7` is the narrow wrapper around the
[v1 offline converter](../nonbiri-upgrade-v1/README.md). It accepts only the
exact rc.7 schema 16 source from `82f1711b674425745a768f781e0e0c34dcb37ac3`, or
the current schema 31 database for a no-change `current` result. Schema 17–30
sources require `nonbiri-upgrade-v1`; normal server startup accepts only fresh
storage or schema 31.

```sh
CGO_ENABLED=0 go build -trimpath -o nonbiri-upgrade-rc7 ./cmd/nonbiri-upgrade-rc7
./nonbiri-upgrade-rc7 --check --database /path/to/database.sqlite
./nonbiri-upgrade-rc7 --apply --database /path/to/database.sqlite
```

Use the same encryption root and reviewed target source as the server. Follow
the linked converter guide for the stopped-service snapshot, rehearsal,
retired environment variables and recovery boundaries. `--database` defaults
to `NONBIRI_DB_PATH`; `--timeout` defaults to `30m`. The JSON status format,
transactional rollback, data preservation and exclusive-access requirements
are identical to the main converter.
