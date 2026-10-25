package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func oldStewardPolicyForTest(t *testing.T) string {
	t.Helper()
	grants := map[string]bool{}
	for i, key := range schema36StewardPermissionKeys {
		grants[key] = i%2 == 0
	}
	body, err := json.Marshal(grants)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestAgentPolicyStartupMigrationPreservesAuthorityAndAudit(t *testing.T) {
	path := bootstrapTestPath(t, "agent-policy.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	user := hostileInsertUser(t, store.DB(), "policy-target", 0, 0)
	original := oldStewardPolicyForTest(t)
	hostileMustExec(t, store.DB(), `INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,9,?,'{"saved":"17"}','enhanced',10,20)`, user, original)
	hostileMustExec(t, store.DB(), `INSERT INTO steward_permission_audits(user_id,from_revision,to_revision,previous_policy,next_policy,created_at) VALUES(?,8,9,?,?,20)`, user, original, original)
	dropAgentFoundationForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=36")
	if hash, err := SchemaManifestHash(t.Context(), store.DB()); err != nil || hash != Schema36ManifestHash {
		t.Fatal(hash, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		store, err = Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		var body, limits, scope, previous, next string
		var revision, created, updated int64
		err = store.DB().QueryRow(`SELECT permissions,limits,donation_scope,revision,created_at,updated_at FROM steward_permissions WHERE user_id=?`, user).Scan(&body, &limits, &scope, &revision, &created, &updated)
		if err != nil || revision != 9 || created != 10 || updated != 20 || limits != `{"saved":"17"}` || scope != "enhanced" {
			t.Fatal("policy facts changed", err)
		}
		var before, after map[string]bool
		json.Unmarshal([]byte(original), &before)
		json.Unmarshal([]byte(body), &after)
		if len(after) != 51 {
			t.Fatal("not current", after)
		}
		for key, value := range before {
			if after[key] != value {
				t.Fatal("authority changed", key)
			}
		}
		for _, key := range []string{"ai.chat", "ai.review.read", "ai.memory.edit"} {
			if after[key] {
				t.Fatal("new grant enabled", key)
			}
		}
		if err = store.DB().QueryRow("SELECT previous_policy,next_policy FROM steward_permission_audits WHERE user_id=?", user).Scan(&previous, &next); err != nil || previous != original || next != original {
			t.Fatal("audit rewritten", err)
		}
		if err = store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAgentPolicyUpgradeFailureRollsBackAndNewerSchemaRejects(t *testing.T) {
	path := bootstrapTestPath(t, "agent-policy-failure.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	valid := hostileInsertUser(t, store.DB(), "valid-policy", 0, 0)
	invalid := hostileInsertUser(t, store.DB(), "invalid-policy", 0, 0)
	original := oldStewardPolicyForTest(t)
	for _, p := range []struct {
		id   int64
		body string
	}{{valid, original}, {invalid, "{}"}} {
		hostileMustExec(t, store.DB(), `INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,'{}',1,1)`, p.id, p.body)
	}
	dropAgentFoundationForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=36")
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("invalid policy migrated")
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var body string
	if err = raw.QueryRow("SELECT permissions FROM steward_permissions WHERE user_id=?", valid).Scan(&body); err != nil || body != original {
		t.Fatal("partial policy update", body, err)
	}
	if hash, err := SchemaManifestHash(t.Context(), raw); err != nil || hash != Schema36ManifestHash {
		t.Fatal("DDL did not roll back", hash, err)
	}
	hostileMustExec(t, raw, "UPDATE steward_permissions SET permissions=? WHERE user_id=?", original, invalid)
	raw.Close()
	store, err = Open(path, vault)
	if err != nil {
		t.Fatal("retry", err)
	}
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=38")
	store.Close()
	before := snapshotBootstrapSources(t, path)
	if opened, err := Open(path, vault); err == nil {
		opened.Close()
		t.Fatal("newer database accepted")
	}
	assertBootstrapSourcesUnchanged(t, path, before)
}

func TestLedgerCompactionCurrentAndHistoricalBoundaries(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, _, present, err := historicalLedgerCompactionState(t.Context(), tx); err != nil || present {
		t.Fatal("historical pre-retention source", present, err)
	}
	if _, _, _, err := LedgerCompactionState(t.Context(), tx); err == nil {
		t.Fatal("current runtime accepted missing table")
	}
	if _, err = tx.Exec("CREATE TABLE credit_compaction(id INTEGER PRIMARY KEY,through_seq INTEGER,details_before INTEGER);INSERT INTO credit_compaction VALUES(1,23,45)"); err != nil {
		t.Fatal(err)
	}
	through, before, present, err := LedgerCompactionState(t.Context(), tx)
	if err != nil || !present || through != 23 || before != 45 {
		t.Fatal(through, before, present, err)
	}
	if _, err = tx.Exec("DELETE FROM credit_compaction"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = LedgerCompactionState(t.Context(), tx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing current singleton", err)
	}
}

func TestAgentWorkflowAssetsHaveIndependentLifetime(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	defer database.Close()
	actor := hostileInsertUser(t, database, "workflow-actor", 0, 0)
	donor := hostileInsertUser(t, database, "workflow-donor", 0, 0)
	hostileMustExec(t, database, `INSERT INTO donations(id,user_id,status,revision,description,review_note,created_at,updated_at) VALUES(100,?,'pending',1,'','',1,1)`, donor)
	hostileMustExec(t, database, `INSERT INTO ai_agent_settings(id,config_json,owner_id,owner_label,updated_at) VALUES(1,'{}',?,'former actor',1)`, actor)
	hostileMustExec(t, database, `INSERT INTO ai_agent_memories(kind,content,updated_at) VALUES('chat','permanent memory',1)`)
	hostileMustExec(t, database, `INSERT INTO ai_agent_memory_history(kind,before_content,after_content,revision,actor_id,actor_label,created_at) VALUES('chat','','permanent memory',1,?,'former actor',1)`, actor)
	hostileMustExec(t, database, `INSERT INTO ai_agent_conversations(id,actor_id,actor_role,actor_label,created_at,updated_at) VALUES(1,?,'level5','former actor',1,1)`, actor)
	hostileMustExec(t, database, `INSERT INTO ai_agent_messages(conversation_id,generation,role,content,created_at) VALUES(1,1,'user','permanent chat',1)`)
	for _, job := range []struct {
		id           int
		kind         string
		donation     any
		conversation any
	}{{1, "auto_review", 100, nil}, {2, "chat", nil, 1}} {
		hostileMustExec(t, database, `INSERT INTO ai_agent_jobs(id,kind,actor_id,actor_role,actor_label,conversation_id,donation_id,state,created_at,updated_at) VALUES(?,?,?,'level5','former actor',?,?,'completed',1,1)`, job.id, job.kind, actor, job.conversation, job.donation)
		hostileMustExec(t, database, `INSERT INTO ai_agent_model_results(request_id,job_id,kind,message,created_at) VALUES(?,?,'model','{}',1)`, job.id, job.id)
		hostileMustExec(t, database, `INSERT INTO ai_agent_plans(job_id,state,operations,preview,tool_call_ids,created_at) VALUES(?,'approved','[]','[]','[]',1)`, job.id)
	}
	hostileMustExec(t, database, `INSERT INTO ai_agent_review_events(donation_id,mode,created_at,updated_at) VALUES(100,'preview',1,1)`)
	hostileMustExec(t, database, `INSERT INTO agent_requests(id,client_request_id,source,kind,actor_id,actor_role,actor_label,input_json,state,created_at,completed_at) VALUES(1,'request-one','auto_review','model',?,'level5','former actor','{}','succeeded',1,1)`, actor)
	hostileMustExec(t, database, "DELETE FROM users WHERE id=?", actor)
	hostileMustExec(t, database, "DELETE FROM agent_requests WHERE id=1")
	var retained int
	if err := database.QueryRow("SELECT count(*) FROM ai_agent_model_results").Scan(&retained); err != nil || retained != 2 {
		t.Fatal("request cleanup removed results", retained, err)
	}
	hostileMustExec(t, database, "DELETE FROM donations WHERE id=100")
	for table, want := range map[string]int{"ai_agent_jobs": 1, "ai_agent_plans": 1, "ai_agent_model_results": 1, "ai_agent_review_events": 0, "ai_agent_messages": 1, "ai_agent_conversations": 1, "ai_agent_memory_history": 1, "ai_agent_memories": 1, "ai_agent_settings": 1} {
		if err := database.QueryRow("SELECT count(*) FROM " + table).Scan(&retained); err != nil || retained != want {
			t.Fatal(table, retained, want, err)
		}
	}
	rows, err := database.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("invalid references")
	}
}

func TestSchema36PolicyNormalizerRejectsUnexpectedFormat(t *testing.T) {
	original := oldStewardPolicyForTest(t)
	normalized, changed, err := normalizeSchema36StewardPolicy(original)
	if err != nil || !changed {
		t.Fatal(err)
	}
	again, changed, err := normalizeSchema36StewardPolicy(normalized)
	if err != nil || changed || again != normalized {
		t.Fatal("current policy changed", err)
	}
	var bits map[string]json.RawMessage
	json.Unmarshal([]byte(original), &bits)
	delete(bits, "users.read")
	bits["unexpected"] = json.RawMessage("false")
	bad, _ := json.Marshal(bits)
	if _, _, err := normalizeSchema36StewardPolicy(string(bad)); err == nil {
		t.Fatal("unknown source key accepted")
	}
	if reflect.DeepEqual(original, normalized) {
		t.Fatal("migration did not normalize")
	}
}
