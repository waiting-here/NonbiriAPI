package db

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestStorageContractsCompatibilityRegistryIsDetachedAndClosed(t *testing.T) {
	descriptor := GenerationTwoCompatibilityDescriptor()
	if descriptor.SchemaHash != GenerationTwoSchemaHash() || descriptor.ManifestHash != PinnedGenerationTwoManifestHash {
		t.Fatal("descriptor drift", descriptor)
	}
	seen := map[string]bool{}
	for _, hash := range descriptor.SourceManifestHashes {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != 32 || seen[hash] || hash == descriptor.ManifestHash {
			t.Fatal("invalid source registry", hash)
		}
		seen[hash] = true
	}
	if !seen["3f773b6dca01058f2296f437c3666afde92a74e8eeb861fa8637756dcd859481"] {
		t.Fatal("deployed source missing")
	}
	descriptor.SourceManifestHashes[0] = "unknown"
	if reflect.DeepEqual(descriptor, GenerationTwoCompatibilityDescriptor()) {
		t.Fatal("descriptor mutation changed registry")
	}
}

func TestStorageContractsRolePolicyDefaultAndMalformedPersistence(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	personal, charity, _ := seedBindingOrderFixture(t, database, 1)
	for _, scope := range []struct {
		table string
		id    int64
	}{{"models", personal}, {"charity_models", charity}} {
		var policy string
		if err := database.QueryRow("SELECT role_policy FROM "+scope.table+" WHERE id=?", scope.id).Scan(&policy); err != nil || policy != "{\"default_action\":\"native\",\"rules\":{}}" {
			t.Fatal("native default", policy, err)
		}
		for _, invalid := range []string{
			"{}", "null", "{\"default_action\":\"unknown\",\"rules\":{}}",
			"{\"default_action\":\"native\",\"rules\":[],\"extra\":1}",
			"{\"default_action\":\"native\",\"rules\":{},\"extra\":1}",
			"{\"default_action\":\"native\",\"default_action\":\"user\",\"rules\":{}}",
			"{\"default_action\":\"native\",\"rules\":{\"tool\":\"user\"}}",
			"{\"default_action\":\"native\",\"rules\":{\"critic\":1}}",
			"{\"default_action\":\"native\",\"rules\":{\"critic\":\"unknown\"}}",
			"{\"default_action\":\"native\",\"rules\":{\"critic\":\"user\",\"critic\":\"system\"}}",
			"{\"default_action\":\"native\",\"rules\":{\" critic\":\"user\"}}",
			"{\"default_action\":\"native\",\"rules\":{\"bad\\u0001role\":\"user\"}}",
			"{\"default_action\":\"native\",\"rules\":{\"" + strings.Repeat("界", 65) + "\":\"user\"}}",
		} {
			if _, err := database.Exec("UPDATE "+scope.table+" SET role_policy=? WHERE id=?", invalid, scope.id); err == nil {
				t.Fatalf("%s accepted %q", scope.table, invalid)
			}
		}
		rules := map[string]string{}
		for i := range 33 {
			rules[fmt.Sprintf("extra%d", i)] = "user"
		}
		invalid, _ := json.Marshal(map[string]any{"default_action": "native", "rules": rules})
		hostileMustFail(t, database, "UPDATE "+scope.table+" SET role_policy=? WHERE id=?", string(invalid), scope.id)
		hostileMustExec(t, database, "UPDATE "+scope.table+" SET role_policy=? WHERE id=?", "{\"default_action\":\"native\",\"rules\":{\"developer\":\"system\",\"critic\":\"user\"}}", scope.id)
	}
}

func TestStorageContractsPersonalBatchIdentityProgressAndDeletion(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	user := hostileInsertUser(t, database, "automation-owner", 0, 0)
	batch := hostileOID("pab_")
	insert := "INSERT INTO personal_automation_batches(id,user_id,actor_scope_hash,root_key_hash,request_hash,kind,target_id,item_count,created_at,expires_at) VALUES(?,?,zeroblob(32),zeroblob(32),zeroblob(32),'key_import',1,2,0,86400)"
	hostileMustExec(t, database, insert, batch, user)
	hostileMustFail(t, database, insert, hostileOIDVariant("pab_", 'B', 'Q'), user)
	hostileMustFail(t, database, "UPDATE personal_automation_batches SET expires_at=86401 WHERE id=?", batch)
	step := "INSERT INTO personal_automation_steps(batch_id,user_id,item_index,status,result_json,created_at) VALUES(?,?,?,'success','{\"outcome\":\"created\",\"endpoint_key_id\":\"7\"}',?)"
	hostileMustExec(t, database, step, batch, user, 0, 1)
	hostileMustFail(t, database, step, batch, user, 0, 2)
	hostileMustFail(t, database, step, batch, user, 2, 2)
	hostileMustFail(t, database, step, batch, user, 1, 86400)
	hostileMustFail(t, database, "UPDATE personal_automation_steps SET result_json='{}' WHERE batch_id=?", batch)
	// Generic recovery only deletes accepted idempotency facts; safe child results
	// remain independent of that transient state and have no stored request body.
	hostileMustExec(t, database, "INSERT INTO idempotency_records(scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at) VALUES('personal_automation',zeroblob(32),zeroblob(32),zeroblob(32),'completed',200,x'7B7D',0,86400)")
	hostileMustExec(t, database, "DELETE FROM idempotency_records WHERE state='accepted'")
	var results int
	if err := database.QueryRow("SELECT count(*) FROM personal_automation_steps").Scan(&results); err != nil || results != 1 {
		t.Fatal("confirmed child lost", results, err)
	}
	hostileMustExec(t, database, "INSERT INTO user_deletion_markers(user_id) VALUES(?)", user)
	hostileMustFail(t, database, step, batch, user, 1, 2)
	hostileMustExec(t, database, "DELETE FROM user_deletion_markers WHERE user_id=?", user)
	hostileMustExec(t, database, "DELETE FROM users WHERE id=?", user)
	for _, table := range []string{"personal_automation_batches", "personal_automation_steps"} {
		var rows int
		if err := database.QueryRow("SELECT count(*) FROM " + table).Scan(&rows); err != nil || rows != 0 {
			t.Fatal("deleted owner retained personal mapping", table, rows, err)
		}
	}
	hostileMustFail(t, database, insert, hostileOIDVariant("pab_", 'C', 'Q'), user)
	if err := foreignKeyCheck(context.Background(), database); err != nil {
		t.Fatal(err)
	}
}

func TestStorageContractsLakePeriodsStayUnpublishedAndRejectOverlap(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	var published int
	if err := database.QueryRow("SELECT count(*) FROM lake_notes_periods WHERE status='published'").Scan(&published); err != nil || published != 0 {
		t.Fatal("activity started without explicit setup", published, err)
	}
	period := hostileOID("lnp_")
	hostileMustExec(t, database, "INSERT INTO lake_notes_periods(id,name,revision,status,starts_at,ends_at,created_at,updated_at) VALUES(?,'Lake',1,'draft',0,100,0,0)", period)
	hostileMustFail(t, database, "UPDATE lake_notes_periods SET status='published' WHERE id=?", period)
	hostileMustExec(t, database, "UPDATE lake_notes_periods SET entry_fee_milli=0,status='published' WHERE id=?", period)
	next := hostileOIDVariant("lnp_", 'B', 'Q')
	hostileMustFail(t, database, "INSERT INTO lake_notes_periods(id,name,revision,status,starts_at,ends_at,entry_fee_milli,created_at,updated_at) VALUES(?,'Overlap',1,'published',99,200,0,0,0)", next)
	hostileMustExec(t, database, "INSERT INTO lake_notes_periods(id,name,revision,status,starts_at,ends_at,entry_fee_milli,created_at,updated_at) VALUES(?,'Adjacent',1,'published',100,200,0,0,0)", next)
	hostileMustFail(t, database, "UPDATE lake_notes_periods SET starts_at=99 WHERE id=?", next)
	hostileMustExec(t, database, "INSERT INTO lake_notes_exchange_settings(period_id,direction,source_lot,target_lot) VALUES(?,'general_to_coin',?,?)", period, hostileBlob16(1), hostileBlob16(2))
	var enabled int
	if err := database.QueryRow("SELECT enabled FROM lake_notes_exchange_settings WHERE period_id=?", period).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatal("exchange enabled without explicit setup", enabled, err)
	}
}

func TestStorageContractsEngineExpansionKeepsContentImmutable(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	level := hostileOID("ffl_")
	hostileMustExec(t, database, "INSERT INTO fatfish_levels VALUES(?,'Level','','{}',1,NULL,0,0)", level)
	for _, version := range []int{1, 2, 3} {
		id := fmt.Sprintf("ffv_%021dA", version)
		hostileMustExec(t, database, "INSERT INTO fatfish_level_versions VALUES(?,?,zeroblob(32),?,1,'{}',10,3,0)", id, level, version)
		hostileMustFail(t, database, "UPDATE fatfish_level_versions SET engine_version=1 WHERE id=?", id)
	}
	hostileMustFail(t, database, "INSERT INTO fatfish_level_versions VALUES(?,?,zeroblob(32),4,1,'{}',10,3,0)", hostileOID("ffv_"), level)
}
