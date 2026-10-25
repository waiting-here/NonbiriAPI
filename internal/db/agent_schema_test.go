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
