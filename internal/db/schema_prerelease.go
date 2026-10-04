package db

import _ "embed"

// Prerelease imports are separate from the permanent stable migration chain.
const (
	preLedgerRetentionManifestHash = "e2f99944f596dda1702a76de6c7ba540761bd1d27f748c437968df0ecc1cf452"
	preQueryIndexesManifestHash    = "79baedb87e0c3f8732cf68ed00b7686504032e17f147af84f21887d835c9207b"
	preStorageVersionManifestHash  = "77d032cdaed225d9c317c07062f0fd431de71e9a5898e61db4f7d2fc926b0919"
)

//go:embed migrations/pre_release/ledger_retention.sql
var ledgerRetentionSQL string

//go:embed migrations/pre_release/history_indexes.sql
var historyIndexesSQL string

//go:embed migrations/pre_release/storage_baseline.sql
var storageBaselineSQL string

func preReleaseSchemaBridges() []schemaBridge {
	return []schemaBridge{
		{preLedgerRetentionManifestHash, ledgerRetentionSQL + historyIndexesSQL + storageBaselineSQL},
		{preQueryIndexesManifestHash, historyIndexesSQL + storageBaselineSQL},
		{preStorageVersionManifestHash, storageBaselineSQL},
	}
}
