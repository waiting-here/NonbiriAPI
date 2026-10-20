package v1

const endpointBackoffSchema = `CREATE TABLE endpoint_backoff_policies (
 connector_type TEXT NOT NULL CHECK(length(connector_type) BETWEEN 1 AND 64),
 base_url TEXT NOT NULL CHECK(length(base_url) BETWEEN 1 AND 4096),
 revision INTEGER NOT NULL CHECK(revision>=1),
 rules_json TEXT NOT NULL CHECK(length(CAST(rules_json AS BLOB))<=8192 AND json_valid(rules_json) AND json_type(rules_json)='array' AND json_array_length(rules_json)<=8),
 PRIMARY KEY(connector_type,base_url)
) STRICT;
ALTER TABLE dispatch_smart_backoff_facts ADD COLUMN policy_revision INTEGER NOT NULL DEFAULT 0 CHECK(policy_revision>=0);
ALTER TABLE dispatch_smart_backoff_facts ADD COLUMN rules_json TEXT NOT NULL DEFAULT '[]' CHECK(length(CAST(rules_json AS BLOB))<=8192 AND json_valid(rules_json) AND json_type(rules_json)='array' AND json_array_length(rules_json)<=8);
ALTER TABLE dispatch_smart_backoff_facts ADD COLUMN rule_index INTEGER NOT NULL DEFAULT 0 CHECK(rule_index BETWEEN 0 AND 8);
`
