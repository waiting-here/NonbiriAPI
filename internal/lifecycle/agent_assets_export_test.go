package lifecycle

import (
	"bytes"
	"testing"
)

func TestPersonalExportExcludesAgentAdministratorAssets(t *testing.T) {
	f := newLifecycleTestFixture(t, 100)
	const private = "administrator-asset-sentinel"
	for _, statement := range []string{
		`INSERT INTO ai_agent_conversations(id,actor_id,actor_role,actor_label,created_at,updated_at) VALUES(1,7,'level5','administrator-asset-sentinel',1,1)`,
		`INSERT INTO ai_agent_messages(conversation_id,generation,role,content,created_at) VALUES(1,1,'user','administrator-asset-sentinel',1)`,
		`INSERT INTO ai_agent_memories(kind,content,updated_at) VALUES('chat','administrator-asset-sentinel',1)`,
		`INSERT INTO ai_agent_memory_history(kind,before_content,after_content,revision,actor_id,actor_label,created_at) VALUES('chat','','administrator-asset-sentinel',1,7,'administrator-asset-sentinel',1)`,
		`INSERT INTO agent_requests(client_request_id,source,kind,actor_id,actor_role,actor_label,input_json,state,created_at) VALUES('administrator-asset-sentinel','steward_chat','model',7,'level5','administrator-asset-sentinel','{}','pending',1)`,
	} {
		if _, err := f.store.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	coordinator := mustNewLifecycleCoordinator(t, f.config)
	body, err := coordinator.Export(t.Context(), 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(private)) || bytes.Contains(body, []byte("ai_agent")) || bytes.Contains(body, []byte("agent_requests")) {
		t.Fatal("administrator assets entered personal export")
	}
}
