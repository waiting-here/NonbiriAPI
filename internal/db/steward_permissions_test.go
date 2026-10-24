package db

import (
	"context"
	"strings"
	"testing"
)

func restoreSchema35ForTest(t *testing.T, store *Store) {
	t.Helper()
	old := strings.ReplaceAll(strings.ReplaceAll(StewardAuditRolesSQL, "'admin','level5','level6'", "'admin','level6'"), "'admin','steward5','steward6'", "'admin','steward6'")
	hostileMustExec(t, store.DB(), strings.ReplaceAll(old, "level IN (5,6)", "level=6"))
	hostileMustExec(t, store.DB(), strings.ReplaceAll(EndpointTagsSQL, ",'user_gateway','steward_gateway','invalid_entry'", ""))
	hostileMustExec(t, store.DB(), `DROP TABLE steward_permission_audits; DROP TABLE steward_permissions; UPDATE schema_state SET version=35 WHERE id=1`)
}

func TestStewardPermissionStartupUpgradePreservesExistingAccounts(t *testing.T) {
	path := bootstrapTestPath(t, "steward-permissions.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO risk_client_rules(id,name,status,enabled,revision,conditions_json,created_by_role,updated_by_role,created_at,updated_at) VALUES('historical-rule','Historical rule','suspected',1,3,'[]','admin','level6',1,2);
INSERT INTO client_rule_auto_bans(rule_id,enabled,duration_seconds,revision,updated_at) VALUES('historical-rule',1,60,4,2);
INSERT INTO discord_blacklist(discord_id,reason,created_at) VALUES('123456789012345678','Historical restriction',1);
INSERT INTO discord_blacklist_origins(discord_id,first_actor_kind) VALUES('123456789012345678','steward6');
INSERT INTO discord_blacklist_events(id,discord_id,operation_key,actor_kind,reason_codes_json,safe_note,created_at) VALUES(17,'123456789012345678','historical-operation','steward6','[]','Historical note',1);
INSERT INTO admin_endpoint_tags(base_url,tag) VALUES('https://historical.example/v1','community_charity');`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(strings.ReplaceAll(EndpointTagsSQL, ",'user_gateway','steward_gateway','invalid_entry'", "")); err != nil {
		t.Fatal(err)
	}
	oldRoles := strings.ReplaceAll(strings.ReplaceAll(StewardAuditRolesSQL, "'admin','level5','level6'", "'admin','level6'"), "'admin','steward5','steward6'", "'admin','steward6'")
	if _, err := store.DB().Exec(strings.ReplaceAll(oldRoles, "level IN (5,6)", "level=6")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`DROP TABLE steward_permission_audits; DROP TABLE steward_permissions; UPDATE schema_state SET version=35 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version, policies, audits int
	if err := store.DB().QueryRow(`SELECT version,(SELECT COUNT(*) FROM steward_permissions),(SELECT COUNT(*) FROM steward_permission_audits) FROM schema_state WHERE id=1`).Scan(&version, &policies, &audits); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion || policies != 0 || audits != 0 {
		t.Fatal("unexpected migration state", version, policies, audits)
	}
	if err := validateGenerationTwoManifest(context.Background(), store.DB()); err != nil {
		t.Fatal(err)
	}
	var preserved bool
	if err := store.DB().QueryRow(`SELECT EXISTS(SELECT 1 FROM risk_client_rules r JOIN client_rule_auto_bans a ON a.rule_id=r.id WHERE r.id='historical-rule' AND r.revision=3 AND r.created_by_role='admin' AND r.updated_by_role='level6' AND r.conditions_json='[]' AND a.revision=4 AND a.duration_seconds=60)
AND EXISTS(SELECT 1 FROM admin_endpoint_tags WHERE base_url='https://historical.example/v1' AND tag='community_charity')
AND EXISTS(SELECT 1 FROM discord_blacklist_origins o JOIN discord_blacklist_events e USING(discord_id) WHERE o.first_actor_kind='steward6' AND e.id=17 AND e.actor_kind='steward6' AND e.operation_key='historical-operation' AND e.reason_codes_json='[]' AND e.safe_note='Historical note')`).Scan(&preserved); err != nil || !preserved {
		t.Fatal("historical audit rows changed", preserved, err)
	}
	rows, err := store.DB().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("migration foreign key violation", rows.Err())
	}
}
