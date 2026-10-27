package db

import "strings"

// ServiceKeyScopeSQL gives all revisions of a catalog one endpoint-key owner.
// A detached legacy source acquires its owner when first used by a key.
var ServiceKeyScopeSQL = `CREATE TABLE discovery_source_keys (
 source_id INTEGER PRIMARY KEY REFERENCES discovery_sources(id) ON DELETE CASCADE,
 endpoint_key_id INTEGER NOT NULL REFERENCES endpoint_keys(id) ON DELETE CASCADE
) STRICT;
CREATE INDEX idx_discovery_source_keys_key ON discovery_source_keys(endpoint_key_id,source_id);
CREATE TABLE discovery_run_changes (
 run_id INTEGER PRIMARY KEY REFERENCES discovery_runs(id) ON DELETE CASCADE,
 added_count INTEGER NOT NULL CHECK(added_count BETWEEN 0 AND 10000),
 removed_count INTEGER NOT NULL CHECK(removed_count BETWEEN 0 AND 10000)
) STRICT;
` + strings.ReplaceAll(DonationDiscoveryAuthorizationSQL,
	"WHERE sr.owner_user_id=a.owner_user_id AND sr.credential_id=a.credential_id AND sr.credential_version_id=a.credential_version_id\n AND sr.canonical_base_url",
	"WHERE sr.owner_user_id=a.owner_user_id AND sr.credential_id=a.credential_id AND sr.credential_version_id=a.credential_version_id\n AND EXISTS(SELECT 1 FROM discovery_source_keys scope WHERE scope.source_id=ds.id AND scope.endpoint_key_id=dk.endpoint_key_id)\n AND sr.canonical_base_url")
